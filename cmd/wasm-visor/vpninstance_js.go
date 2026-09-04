//go:build js && wasm

// cmd/wasm-visor/vpninstance_js.go — the in-tab VPN client instance.
//
// The true VPN client for the browser: the visor dials the vpn-server's route
// group itself (the same DialRoutes machinery the skysocks-lite instances
// use, so the packet-level mux/SACK dataplane carries it), runs the standard
// VPN handshake, and the "TUN" is the gVisor userspace netstack
// (pkg/vpn/netstack_tun.go). Everything dialed through vpnDial originates
// INSIDE that stack and egresses at the vpn-server — the server cannot tell
// this client from a native one. The carriers (websocket/webrtc/wt in JS) are
// outside the Go dial path by construction, so the tunnel can never swallow
// its own underlay.
//
// JS surface:
//
//	skywireVisor.vpnStart("<server-pk>")                 → Promise<"connected">
//	skywireVisor.vpnStop()                               → Promise<"stopped">
//	skywireVisor.vpnStatus()                             → {running, server, status, sinceSec}
//	skywireVisor.vpnFetch("http://host/path")            → Promise<{status, contentType, body}>
//
// vpnFetch exists for validation and simple consumers: plain HTTP GET whose
// TCP (and DNS, over UDP :53 to the tunnel resolver) rides the VPN.
package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/vpn"
)

// vpnDNSAddr is the resolver queried through the tunnel for vpnFetch's name
// resolution. Quad9 mirrors the vpn-server's own default DNS posture; the
// query rides the VPN like any other UDP flow.
const vpnDNSAddr = "9.9.9.9:53"

var (
	vpnMu     sync.Mutex
	vpnCl     *vpn.Client
	vpnConn   net.Conn
	vpnServer cipher.PubKey
	vpnState  = "stopped" // stopped | connecting | running | error: ...
	vpnSince  time.Time
)

func vpnSetState(s string) {
	vpnMu.Lock()
	vpnState = s
	vpnMu.Unlock()
	emitProxyLog("vpn", "[vpn-client] "+s)
}

// jsVPNStart(serverPKHex) dials the vpn-server route group, runs the VPN
// handshake, and resolves once the netstack tunnel is up.
func jsVPNStart(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return promise(func() (interface{}, error) { return nil, errors.New("vpnStart(serverPK)") })
	}
	pkHex := args[0].String()
	return promise(func() (interface{}, error) {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			return nil, fmt.Errorf("bad server pk: %w", err)
		}
		if rtr == nil {
			return nil, errors.New("not booted; call boot() first")
		}
		vpnMu.Lock()
		if vpnCl != nil {
			vpnMu.Unlock()
			return nil, errors.New("vpn already running — vpnStop() first")
		}
		vpnMu.Unlock()

		vpnSetState("connecting")
		dctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		conn, err := rtr.DialRoutes(dctx, pk, 0, routing.Port(skyenv.VPNServerPort), router.DefaultDialOptions())
		if err != nil {
			vpnSetState("error: dial: " + err.Error())
			return nil, fmt.Errorf("route dial to vpn-server: %w", err)
		}

		cl := vpn.NewClientEmbedded(vpn.ClientConfig{ServerPK: pk})
		vpnMu.Lock()
		vpnCl, vpnConn, vpnServer, vpnSince = cl, conn, pk, time.Now()
		vpnMu.Unlock()

		go func() {
			err := cl.ServeConn(conn)
			vpnMu.Lock()
			stillOurs := vpnCl == cl
			vpnMu.Unlock()
			if !stillOurs {
				return // superseded by stop/restart
			}
			if err != nil {
				vpnSetState("error: session: " + err.Error())
			} else {
				vpnSetState("stopped")
			}
			vpnMu.Lock()
			if vpnCl == cl {
				vpnCl, vpnConn = nil, nil
			}
			vpnMu.Unlock()
		}()

		// Resolve once the handshake finished and the netstack is configured —
		// TunReady flips after the server assigns the tunnel address.
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if cl.TunReady() {
				vpnSetState("running")
				return "connected", nil
			}
			vpnMu.Lock()
			gone := vpnCl != cl
			st := vpnState
			vpnMu.Unlock()
			if gone || strings.HasPrefix(st, "error:") {
				return nil, errors.New(st)
			}
			time.Sleep(200 * time.Millisecond)
		}
		return nil, errors.New("vpn handshake timed out (30s)")
	})
}

// jsVPNStop tears the session down.
func jsVPNStop(_ js.Value, _ []js.Value) interface{} {
	return promise(func() (interface{}, error) {
		vpnMu.Lock()
		cl, conn := vpnCl, vpnConn
		vpnCl, vpnConn = nil, nil
		vpnMu.Unlock()
		if cl == nil {
			return "already stopped", nil
		}
		cl.Close()
		if conn != nil {
			_ = conn.Close() //nolint:errcheck
		}
		vpnSetState("stopped")
		return "stopped", nil
	})
}

// jsVPNStatus reports the instance state (synchronous).
func jsVPNStatus(_ js.Value, _ []js.Value) interface{} {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	st := map[string]interface{}{
		"running": vpnCl != nil && vpnState == "running",
		"status":  vpnState,
	}
	if vpnCl != nil {
		st["server"] = vpnServer.Hex()
		st["sinceSec"] = int(time.Since(vpnSince).Seconds())
	}
	return js.ValueOf(st)
}

// vpnDial dials host:port through the tunnel, resolving names over the
// tunnel's own resolver first (the netstack speaks IP literals only).
func vpnDial(dialCtx context.Context, network, address string) (net.Conn, error) {
	vpnMu.Lock()
	cl := vpnCl
	vpnMu.Unlock()
	if cl == nil {
		return nil, errors.New("vpn is not running")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if _, perr := netip.ParseAddr(host); perr != nil {
		ip, rerr := vpnResolve(dialCtx, cl, host)
		if rerr != nil {
			return nil, fmt.Errorf("resolve %s over vpn: %w", host, rerr)
		}
		host = ip
	}
	return cl.NetstackDial(dialCtx, network, net.JoinHostPort(host, port))
}

// vpnResolve answers an A query over the tunnel (plain DNS to vpnDNSAddr).
// Deliberately minimal — one question, first A answer wins.
func vpnResolve(resolveCtx context.Context, cl *vpn.Client, host string) (string, error) {
	conn, err := cl.NetstackDial(resolveCtx, "udp", vpnDNSAddr)
	if err != nil {
		return "", err
	}
	defer conn.Close() //nolint:errcheck

	name, err := dnsmessage.NewName(strings.TrimSuffix(host, ".") + ".")
	if err != nil {
		return "", err
	}
	q := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: uint16(rand.Intn(65536)), RecursionDesired: true}, //nolint:gosec // query id, not a secret
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	packed, err := q.Pack()
	if err != nil {
		return "", err
	}
	if d, ok := resolveCtx.Deadline(); ok {
		_ = conn.SetDeadline(d) //nolint:errcheck
	} else {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	}
	if _, err := conn.Write(packed); err != nil {
		return "", err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	var resp dnsmessage.Message
	if err := resp.Unpack(buf[:n]); err != nil {
		return "", err
	}
	for _, ans := range resp.Answers {
		if a, ok := ans.Body.(*dnsmessage.AResource); ok {
			return netip.AddrFrom4(a.A).String(), nil
		}
	}
	return "", fmt.Errorf("no A record for %s", host)
}

// jsVPNFetch(url) does a plain-HTTP GET through the tunnel. Validation
// surface: proves TCP + DNS ride the VPN end to end.
func jsVPNFetch(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return promise(func() (interface{}, error) { return nil, errors.New("vpnFetch(url)") })
	}
	rawURL := args[0].String()
	return promise(func() (interface{}, error) {
		httpCl := &http.Client{
			Transport: &http.Transport{DialContext: vpnDial, DisableKeepAlives: true},
			Timeout:   45 * time.Second,
		}
		resp, err := httpCl.Get(rawURL) //nolint:noctx // promise-scoped; client carries the timeout
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close() //nolint:errcheck
		const bodyCap = 64 * 1024
		body := make([]byte, 0, 4096)
		buf := make([]byte, 4096)
		for len(body) < bodyCap {
			n, rerr := resp.Body.Read(buf)
			body = append(body, buf[:n]...)
			if rerr != nil {
				break
			}
		}
		return js.ValueOf(map[string]interface{}{
			"status":      resp.StatusCode,
			"contentType": resp.Header.Get("Content-Type"),
			"body":        string(body),
		}), nil
	})
}
