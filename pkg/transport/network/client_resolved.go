//go:build !tinygo

// Package network pkg/transport/network/client_resolved.go
//
// The address-resolver-backed carrier machinery (stcpr/sudph/quic), split out of
// client.go behind //go:build !tinygo. It imports pkg/.../addrresolver (net/http
// + packetfilter → quic-go) and operates raw UDP/TCP — none of which fit the
// TinyGo/browser target, where only dmsg (and stcp, AR-free) carriers exist. The
// TinyGo build gets makeResolvedClient as an unsupported-error stub in
// client_tinygo.go.
package network

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// makeResolvedClient builds an address-resolver-backed carrier (STCPR/SUDPH/QUIC).
// ClientFactory.ARClient is typed `any` to keep addrresolver out of the TinyGo
// graph (see client.go); the type-assert here recovers the concrete client on
// native. A nil AR yields a client whose dials fail at resolve time — fine,
// because a dmsg-only deployment never asks for these carriers.
func (f *ClientFactory) makeResolvedClient(netType types.Type, generic *genericClient, port int) (Client, error) {
	ar, _ := f.ARClient.(addrresolver.APIClient)
	resolved := &resolvedClient{genericClient: generic, ar: ar}
	// Unified transport port: a type rides the master socket only when its
	// per-type port (the `port` arg) is 0; a non-zero per-type port breaks it out
	// onto its own socket (see sharedUDPConnFor / stcprSharedListenerFor).
	switch netType {
	case types.STCPR:
		c := newStcpr(resolved, port)
		if lis := f.stcprSharedListenerFor(port); lis != nil {
			c.(*stcprClient).sharedListener = lis
		}
		return c, nil
	case types.SUDPH:
		c, err := makeSudphClient(resolved, port)
		if err != nil {
			return nil, err
		}
		if sc := f.sharedUDPConnFor(protoSUDPH, port); sc != nil {
			c.(*sudphClient).sharedConn = sc
		}
		return c, nil
	case types.QUIC:
		c, err := makeQuicClient(resolved, port)
		if err != nil {
			return nil, err
		}
		c.(*quicClient).table = f.QUICTable // static PK→addr pins (AR-free dial)
		if sc := f.sharedUDPConnFor(protoQUIC, port); sc != nil {
			c.(*quicClient).sharedConn = sc
		}
		return c, nil
	}
	return nil, fmt.Errorf("cannot initiate client, type %s not supported", netType)
}

// resolvedClient is a wrapper around genericClient,
// for the types of transports that use address resolver service
// to resolve addresses of remote visors
type resolvedClient struct {
	*genericClient
	ar addrresolver.APIClient
}

type dialFunc func(ctx context.Context, addr string) (net.Conn, error)

// dialVisor uses address resovler to obtain network address of the target visor
// and dials that visor address(es)
// dial process is specific to transport type and is provided by the client
func (c *resolvedClient) dialVisor(ctx context.Context, rPK cipher.PubKey, dial dialFunc) (net.Conn, error) {
	visorData, err := c.ar.Resolve(ctx, string(c.netType), rPK)
	if err != nil {
		return nil, fmt.Errorf("resolve PK: %w", err)
	}
	c.log.Debugf("Resolved PK %v to visor data %v", rPK, visorData)

	// For self-connections (rPK == local PK), always try local addresses first
	// to avoid NAT hairpinning issues when connecting via public IP
	isSelfConnection := rPK == c.lPK

	// Check if the remote visor shares our public IP (same NAT)
	// In this case, we should try LAN addresses first to avoid NAT hairpinning issues
	samePublicIP := false
	localPublicIP := c.ar.LocalPublicIP()
	if localPublicIP != "" && visorData.RemoteAddr != "" {
		remoteIP := visorData.RemoteAddr
		// Extract just the IP if RemoteAddr includes a port
		if host, _, err := net.SplitHostPort(remoteIP); err == nil {
			remoteIP = host
		}
		samePublicIP = localPublicIP == remoteIP
		if samePublicIP {
			c.log.Debugf("Remote visor shares same public IP (%s), trying LAN addresses first", localPublicIP)
		}
	}

	if visorData.IsLocal || isSelfConnection || samePublicIP {
		if isSelfConnection {
			c.log.Debug("Detected self-connection, trying local addresses to avoid NAT issues")
		}
		// Get the public IP to filter it out from LAN addresses
		remotePublicIP := visorData.RemoteAddr
		if host, _, err := net.SplitHostPort(remotePublicIP); err == nil {
			remotePublicIP = host
		}
		for _, host := range visorData.Addresses {
			// Skip loopback addresses unless it's a self-connection
			if !isSelfConnection && (host == "127.0.0.1" || host == "::1") {
				continue
			}
			// Skip the public IP - we'll fall back to it anyway
			if host == remotePublicIP {
				continue
			}
			addr := net.JoinHostPort(host, visorData.Port)
			c.log.Debugf("Trying LAN address: %s", addr)
			conn, err := dial(ctx, addr)
			if err == nil {
				c.log.Debugf("Successfully connected via LAN address: %s", addr)
				return conn, nil
			}
			c.log.WithError(err).Debugf("Failed to dial %s, trying next address", addr)
		}
	}

	// Happy Eyeballs (#1525 Phase 3): when AR has both a v6 and v4
	// public address for the peer, try v6 first; fall back to v4 on
	// any v6 failure (refused, timeout, unreachable). Sequential —
	// no parallel race, no double-noise-handshake risk. The v6 attempt
	// gets a bounded window (v6HeadStart) so a black-holed v6 route
	// doesn't extend the overall dial materially beyond the v4 path's
	// own connect time.
	//
	// Backward-compat: v6-only and v4-only peers are dispatched
	// directly (no head-start cost). The pre-#1525 single-stack
	// behavior — "RemoteAddrV6 is empty" — falls through the
	// addrV6 == "" branch and dials RemoteAddr exactly like before.
	addrV4 := canonicalAddr(visorData.RemoteAddr, visorData.Port)
	addrV6 := canonicalAddr(visorData.RemoteAddrV6, visorData.Port)
	switch {
	case addrV6 != "" && addrV4 != "":
		c.log.Debugf("Happy Eyeballs: dual-stack %s (v6) / %s (v4)", addrV6, addrV4)
		return happyEyeballsDial(ctx, addrV6, addrV4, dial, c.log)
	case addrV6 != "":
		c.log.Debugf("Dialing v6-only public address: %s", addrV6)
		return dial(ctx, addrV6)
	case addrV4 != "":
		c.log.Debugf("Dialing v4-only public address: %s", addrV4)
		return dial(ctx, addrV4)
	default:
		return nil, fmt.Errorf("visor data has neither RemoteAddr nor RemoteAddrV6")
	}
}

// v6HeadStart bounds the v6 attempt in happyEyeballsDial. Modeled
// on RFC 8305's 250ms head-start, widened to 1s to give a slow but
// reachable v6 route a fair chance before falling back to v4. A
// black-holed v6 route still bounds the extra latency to this much.
const v6HeadStart = time.Second

// canonicalAddr returns "" when raw is empty (lets the caller branch
// on "v6 unavailable"), otherwise appends port when raw is bare-host.
// Mirrors the inline check the pre-#1525 dialer did so the v4/v6
// branches stay symmetric.
func canonicalAddr(raw, port string) string {
	if raw == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(raw); err != nil {
		return net.JoinHostPort(raw, port)
	}
	return raw
}

// happyEyeballsDial tries v6 first with a v6HeadStart-bounded sub-ctx,
// falls back to v4 on any v6 failure. Sequential by design: one
// socket + one noise handshake in flight at a time. Caller-supplied
// ctx still bounds the overall dial; the v6 sub-ctx only narrows the
// v6 attempt's deadline within ctx's window.
func happyEyeballsDial(ctx context.Context, addrV6, addrV4 string, dial dialFunc, log *logging.Logger) (net.Conn, error) {
	v6Ctx, cancel := context.WithTimeout(ctx, v6HeadStart)
	conn, v6Err := dial(v6Ctx, addrV6)
	cancel()
	if v6Err == nil {
		log.Debugf("Happy Eyeballs: v6 connected: %s", addrV6)
		return conn, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("happy eyeballs: ctx done during v6 attempt: %w", ctx.Err())
	}
	log.WithError(v6Err).Debugf("Happy Eyeballs: v6 %s failed, falling back to v4 %s", addrV6, addrV4)
	conn, v4Err := dial(ctx, addrV4)
	if v4Err != nil {
		return nil, fmt.Errorf("happy eyeballs: v6 (%s) failed: %v; v4 (%s) failed: %w", addrV6, v6Err, addrV4, v4Err)
	}
	log.Debugf("Happy Eyeballs: v4 fallback connected: %s", addrV4)
	return conn, nil
}
