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
//	skywireVisor.vpnStart(["<server-pk>"])               → Promise<"connected <pk>"> (no arg = auto-select from SD)
//	skywireVisor.vpnStop()                               → Promise<"stopped">
//	skywireVisor.vpnStatus()                             → {running, server, status, sinceSec}
//	skywireVisor.vpnFetch("http://host/path")            → Promise<{status, contentType, body}>
//
// vpnFetch exists for validation and simple consumers: plain HTTP GET whose
// TCP (and DNS, over UDP :53 to the tunnel resolver) rides the VPN.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/skycoin/skywire/pkg/visor/visorcore"
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
	// vpnEgress: when true AND the tunnel is running, the page's default
	// clearnet path (fetchClearnet with no pinned exit) rides the VPN instead
	// of the skysocks proxy chain — the "wrap everything" mode. Set via
	// vpnStart's options ({egress:true}); cleared on stop.
	vpnEgress bool
)

// vpnEgressActive reports whether default clearnet traffic should ride the
// tunnel right now (mode on AND session running).
func vpnEgressActive() bool {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	return vpnEgress && vpnCl != nil && vpnState == "running"
}

func vpnSetState(s string) {
	vpnMu.Lock()
	vpnState = s
	vpnMu.Unlock()
	emitProxyLog("vpn", "[vpn-client] "+s)
}

// vpnCool tracks servers that failed dial or handshake so auto-selection
// skips them for a while — the VPN analogue of the proxy pool's exit
// cooldown. 10 minutes: long enough to rotate the pool, short enough that a
// transiently unreachable server comes back.
const vpnCooldown = 10 * time.Minute

var (
	vpnCoolMu sync.Mutex
	vpnCool   = map[cipher.PubKey]time.Time{}
)

func vpnServerCooled(pk cipher.PubKey) bool {
	vpnCoolMu.Lock()
	defer vpnCoolMu.Unlock()
	return time.Now().Before(vpnCool[pk])
}

func vpnCoolServer(pk cipher.PubKey) {
	vpnCoolMu.Lock()
	vpnCool[pk] = time.Now().Add(vpnCooldown)
	vpnCoolMu.Unlock()
}

// vpnDialServer dials the server's route group with the proxy chain's retry
// discipline: up to attempts full tries (the first dial after a cold boot
// routinely times out while transports are still forming — the retry lands),
// and the "already being initialized" rejection is treated as the transient
// it is (a concurrent dial on the same route-group descriptor) rather than a
// failure.
func vpnDialServer(pk cipher.PubKey, attempts int) (net.Conn, error) {
	var lastErr error
	for a := 0; a < attempts; a++ {
		dctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		conn, err := rtr.DialRoutes(dctx, pk, 0, routing.Port(skyenv.VPNServerPort), router.DefaultDialOptions())
		for r := 0; err != nil && strings.Contains(err.Error(), "already being initialized") && r < 4; r++ {
			select {
			case <-time.After(600 * time.Millisecond):
			case <-dctx.Done():
			}
			conn, err = rtr.DialRoutes(dctx, pk, 0, routing.Port(skyenv.VPNServerPort), router.DefaultDialOptions())
		}
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
		vpnSetState(fmt.Sprintf("connecting: dial attempt %d/%d to %s failed: %v", a+1, attempts, pk.Hex(), err))
	}
	return nil, lastErr
}

// vpnTryServer runs one full connection attempt against pk: dial (with the
// retry discipline), handshake, netstack up. On success the session is
// installed as THE vpn instance and its serve goroutine runs until stop or
// failure.
func vpnTryServer(pk cipher.PubKey) error {
	vpnSetState("connecting to " + pk.Hex() + "…")
	conn, err := vpnDialServer(pk, 2)
	if err != nil {
		return fmt.Errorf("route dial to vpn-server: %w", err)
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
			return nil
		}
		vpnMu.Lock()
		gone := vpnCl != cl
		st := vpnState
		vpnMu.Unlock()
		if gone || strings.HasPrefix(st, "error:") {
			return errors.New(st)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Handshake never completed: tear this attempt down so the next
	// candidate starts clean.
	cl.Close()
	_ = conn.Close() //nolint:errcheck
	vpnMu.Lock()
	if vpnCl == cl {
		vpnCl, vpnConn = nil, nil
	}
	vpnMu.Unlock()
	return errors.New("vpn handshake timed out (30s)")
}

// jsVPNStart([serverPKHex]) connects the tunnel. With a PK it targets that
// server; with no arguments it auto-selects from service discovery
// (type=vpn) exactly like the proxy pool picks exits — direct-peer servers
// first, failed ones cooled — and tries candidates until one completes the
// handshake. The completed handshake + netstack-up IS the honest probe: a
// zombie vpn-server cannot fake the address assignment.
func jsVPNStart(_ js.Value, args []js.Value) interface{} {
	pkHex := ""
	if len(args) > 0 && args[0].Type() == js.TypeString {
		pkHex = args[0].String()
	}
	// Options object (2nd arg): {egress:true} turns on default-egress mode —
	// the page's un-pinned fetchClearnet traffic rides the tunnel while it
	// runs (see vpnEgressActive / jsFetchClearnet).
	egress := false
	if len(args) > 1 && args[1].Type() == js.TypeObject {
		egress = args[1].Get("egress").Truthy()
	}
	return promise(func() (interface{}, error) {
		if rtr == nil {
			return nil, errors.New("not booted; call boot() first")
		}
		vpnMu.Lock()
		if vpnCl != nil {
			vpnMu.Unlock()
			return nil, errors.New("vpn already running — vpnStop() first")
		}
		vpnMu.Unlock()

		var candidates []cipher.PubKey
		if pkHex != "" {
			var pk cipher.PubKey
			if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
				return nil, fmt.Errorf("bad server pk: %w", err)
			}
			candidates = []cipher.PubKey{pk}
		} else {
			sdPK, err := dmsgURLPK(visorcore.ResolveServices(nil).ServiceDiscoveryDmsg)
			if err != nil {
				return nil, fmt.Errorf("no service discovery configured: %w", err)
			}
			vpnSetState("selecting a vpn-server from service discovery…")
			candidates = pickServiceProviders(ctx, sdPK, "vpn", 4, cipher.PubKey{}, vpnServerCooled)
			if len(candidates) == 0 {
				vpnSetState("error: no vpn servers available from service discovery")
				return nil, errors.New("no vpn servers available from service discovery")
			}
		}

		var lastErr error
		for _, pk := range candidates {
			if err := vpnTryServer(pk); err != nil {
				lastErr = err
				vpnCoolServer(pk)
				vpnSetState(fmt.Sprintf("server %s failed (%v) — trying next", pk.Hex(), err))
				continue
			}
			vpnMu.Lock()
			vpnEgress = egress
			vpnMu.Unlock()
			return "connected " + pk.Hex(), nil
		}
		vpnSetState("error: all candidates failed: " + lastErr.Error())
		return nil, fmt.Errorf("all %d vpn-server candidate(s) failed, last: %w", len(candidates), lastErr)
	})
}

// jsVPNStop tears the session down.
func jsVPNStop(_ js.Value, _ []js.Value) interface{} {
	return promise(func() (interface{}, error) {
		vpnMu.Lock()
		cl, conn := vpnCl, vpnConn
		vpnCl, vpnConn = nil, nil
		vpnEgress = false
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
		"egress":  vpnEgress,
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

// vpnHTTPDo performs one HTTP request whose TCP (and DNS) ride the tunnel.
// Shared by vpnFetch and the default-egress hook in fetchClearnet.
func vpnHTTPDo(method, rawURL string, body []byte, headers map[string]string) (int, []byte, http.Header, error) {
	httpCl := &http.Client{
		Transport: &http.Transport{DialContext: vpnDial, DisableKeepAlives: true},
		Timeout:   120 * time.Second,
	}
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, rawURL, rd) //nolint:noctx // client carries the timeout
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpCl.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, b, resp.Header, nil
}

// jsVPNFetch(url) does a plain-HTTP GET through the tunnel. Validation
// surface: proves TCP + DNS ride the VPN end to end.
func jsVPNFetch(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return promise(func() (interface{}, error) { return nil, errors.New("vpnFetch(url)") })
	}
	rawURL := args[0].String()
	return promise(func() (interface{}, error) {
		status, b, hdr, err := vpnHTTPDo("GET", rawURL, nil, nil)
		if err != nil {
			return nil, err
		}
		const bodyCap = 64 * 1024
		if len(b) > bodyCap {
			b = b[:bodyCap]
		}
		return js.ValueOf(map[string]interface{}{
			"status":      status,
			"contentType": hdr.Get("Content-Type"),
			"body":        string(b),
		}), nil
	})
}
