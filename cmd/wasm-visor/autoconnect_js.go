//go:build js && wasm

// Package main — in-browser public autoconnect over WS.
//
// A browser leaf can't accept inbound transports, but it CAN dial out. To join
// the mesh (so routes can form) it periodically fetches the public-visor list
// from service discovery, resolves each peer's reachable endpoint from the
// address resolver, and dials a transport. It tries the direct carriers first —
// WS (to the peer's WS-on-stcpr-port; http pages only, since https blocks plain
// ws:// as mixed content) and WT (QUIC on the peer's unified transport port,
// cert-hash pinned) — then falls back to WebRTC, a NAT-traversing DataChannel
// signalled over dmsg that reaches ANY visor when the direct carriers can't
// (the common case on an https page or against a NAT'd peer).
// See docs/design/wasm-public-autoconnect.md.
//
// Both service-discovery and address-resolver are reached with net/http-free,
// signed HTTP-over-dmsg (the std-Go http.Client/dmsghttp path deadlocks under
// wasm's single thread), mirroring tpdclient.NewDmsg's auth flow.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/httpauthclient"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	autoconnectInterval = 90 * time.Second
	// Direct carriers (WT always; WS on insecure pages) are what ONLY a publicly
	// reachable visor can accept. The wasm visor makes them to MANY public visors
	// — its analog of a native public visor's stcpr-to-the-whole-fleet mesh
	// participation — so it can carry/relay routes like a first-class node, not
	// just hold a handful of links.
	autoconnectMaxDirect = 32
	// WebRTC (NAT-traversing DataChannel over dmsg) is reserved on a SEPARATE,
	// smaller budget for peers no direct carrier can reach (NAT'd / WT-less
	// visors). Kept apart so WebRTC — heavier, and something ANY visor can accept
	// — never crowds out a direct transport slot to a public visor.
	autoconnectMaxWebRTC = 6
	// After a peer fails ALL carriers (direct then WebRTC), don't re-dial it for
	// this long — avoids hammering unreachable peers + spamming net::ERR_*.
	// Cleared on any success.
	autoconnectFailCooldown = 10 * time.Minute
)

// pk -> time of last all-carriers-failed dial. Persists across cycles (the loop
// is a single goroutine, so no lock needed under single-threaded wasm).
var autoconnectFailed = map[cipher.PubKey]time.Time{}

// startWSAutoconnect launches the WS public-autoconnect loop. No-op if any
// prerequisite (SD/AR url, dmsg, transport manager, ws table) is missing.
func startWSAutoconnect(ctx context.Context, sdDmsgURL, arDmsgURL string, pk cipher.PubKey, sk cipher.SecKey) {
	sdPK, e1 := dmsgURLPK(sdDmsgURL)
	arPK, e2 := dmsgURLPK(arDmsgURL)
	if e1 != nil || e2 != nil || dmsgC == nil || tpM == nil || wsTable == nil {
		vlog("ws-autoconnect: disabled (missing SD/AR url, dmsg, tm, or ws table)")
		return
	}
	ar := &arDmsg{dmsgC: dmsgC, host: arPK.Hex(), key: pk, sec: sk}
	go func() {
		// Let boot fully settle (dmsg sessions up) before the first pass.
		select {
		case <-ctx.Done():
			return
		case <-time.After(8 * time.Second):
		}
		t := time.NewTicker(autoconnectInterval)
		defer t.Stop()
		for {
			// Recover per-pass so a transient panic (e.g. a peer's malformed AR
			// reply) can't take down the whole tab.
			func() {
				defer func() {
					if r := recover(); r != nil {
						vlog(fmt.Sprintf("ws-autoconnect: recovered panic: %v", r))
					}
				}()
				wsAutoconnectOnce(ctx, sdPK, ar, pk)
			}()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	vlog("ws-autoconnect: started (WS to public visors; SD=" + sdPK.Hex()[:8] + " AR=" + arPK.Hex()[:8] + ")")
}

func wsAutoconnectOnce(ctx context.Context, sdPK cipher.PubKey, ar *arDmsg, selfPK cipher.PubKey) {
	// 1. public-visor list from SD (unauthenticated read over dmsg).
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	status, _, body, err := dmsgclient.FetchOverDmsg(fctx, dmsgC, "GET", sdPK.Hex(), "/api/services?type=visor", nil, nil)
	cancel()
	if err != nil || status != 200 {
		vlog(fmt.Sprintf("ws-autoconnect: SD fetch status=%d err=%v", status, err))
		return
	}
	var services []servicedisc.Service
	if err := json.Unmarshal(body, &services); err != nil {
		vlog("ws-autoconnect: SD decode: " + err.Error())
		return
	}

	// Classify existing automatic transports by carrier class. A peer already
	// reachable by a DIRECT carrier (WT/WS) is deliberately NOT a WebRTC
	// candidate — WebRTC is spent only where direct can't reach.
	haveDirect := map[cipher.PubKey]bool{}
	haveWebRTC := map[cipher.PubKey]bool{}
	tpM.WalkTransports(func(mt *transport.ManagedTransport) bool {
		if mt.Entry.Label != transport.LabelAutomatic {
			return true
		}
		if mt.Type() == types.WEBRTC {
			haveWebRTC[mt.Remote()] = true
		} else {
			haveDirect[mt.Remote()] = true
		}
		return true
	})

	// On an HTTPS page the browser blocks plain ws:// (mixed content), so WT is
	// the only direct carrier; on an insecure (http/localhost/file) page ws://
	// works too, so dial both to each public visor for path diversity.
	https := pageHTTPS()

	vlog(fmt.Sprintf("ws-autoconnect: SD returned %d public visors (have %d direct, %d webrtc peers)", len(services), len(haveDirect), len(haveWebRTC)))

	// --- Pass 1: DIRECT carriers (WT, +WS on insecure) to PUBLIC visors. ---
	// These are the transports only a publicly-reachable visor can accept, so
	// make them broadly (up to autoconnectMaxDirect). Peers no direct carrier
	// reaches are collected for the WebRTC pass rather than cooled down here.
	directPeers := len(haveDirect)
	addedDirect := 0
	skippedCooldown := 0
	var directFailed []cipher.PubKey
	for i := range services {
		if directPeers >= autoconnectMaxDirect {
			break
		}
		pk := services[i].Addr.PubKey()
		if pk == selfPK || haveDirect[pk] {
			continue
		}
		if ts, bad := autoconnectFailed[pk]; bad {
			if time.Since(ts) < autoconnectFailCooldown {
				skippedCooldown++
				continue
			}
			delete(autoconnectFailed, pk)
		}
		if dialDirect(ctx, ar, pk, services[i].Addr.Port(), https) {
			directPeers++
			addedDirect++
			delete(autoconnectFailed, pk)
		} else {
			directFailed = append(directFailed, pk)
		}
	}

	// --- Pass 2: WebRTC to the peers no direct carrier reached. ---
	// Today those are NAT'd / WT-less "public" visors; the same mechanism is how
	// the tab will reach genuinely non-public visors (discovered via public
	// peers' transport graphs) later. Separate budget so WebRTC never displaces a
	// direct transport. A peer that fails here too (or is over budget) is cooled
	// down so we don't re-dial direct to it every cycle.
	webrtcPeers := len(haveWebRTC)
	addedWebRTC := 0
	// WebRTC needs RTCPeerConnection, which is ABSENT in a Web Worker — where the
	// wasm runtime now runs (pkg/wasmhv/worker.js). Attempting it there fails every
	// dial with "syscall/js: call of Value.Get on undefined" and spams the log. Skip
	// the pass entirely when WebRTC is unavailable; the direct carriers (WT/WS) still
	// run. (Restoring WebRTC from the worker needs a main-thread PeerConnection
	// proxy — a separate follow-up.) Don't cool-down the peers here: they stay
	// eligible for a direct carrier next cycle.
	if !webrtcAvailable() {
		warnWebRTCUnavailableOnce()
	} else {
		for _, pk := range directFailed {
			if haveWebRTC[pk] {
				continue
			}
			if webrtcPeers < autoconnectMaxWebRTC && dialWebRTC(ctx, pk) {
				webrtcPeers++
				addedWebRTC++
				delete(autoconnectFailed, pk)
			} else {
				autoconnectFailed[pk] = time.Now()
			}
		}
	}

	if addedDirect > 0 || addedWebRTC > 0 {
		vlog(fmt.Sprintf("ws-autoconnect: +%d direct, +%d webrtc (%d public candidates)", addedDirect, addedWebRTC, len(services)))
	}
	if skippedCooldown > 0 {
		vlog(fmt.Sprintf("ws-autoconnect: skipped %d unreachable peer(s) in cooldown", skippedCooldown))
	}
}

// dialDirect attempts the browser's DIRECT carriers to a public visor — WT
// (QUIC, cert-hash pinned) always, plus WS (ws://ip:port over the stcpr cmux
// port) on an insecure page where mixed-content rules allow it. Returns true if
// either transport formed. These are the carriers only a publicly-reachable
// visor can accept.
func dialDirect(ctx context.Context, ar *arDmsg, pk cipher.PubKey, port uint16, https bool) bool {
	got := false

	// WS: rides the stcpr cmux port. Resolve the peer's stcpr IP, dial
	// ws://ip:<SD-advertised-port>/. Skipped on HTTPS (mixed content).
	if !https {
		if addr, err := ar.resolveStcpr(ctx, pk); err == nil && addr != "" {
			host := addr
			if h, _, e := net.SplitHostPort(addr); e == nil {
				host = h
			}
			url := fmt.Sprintf("ws://%s:%d/", host, port)
			wsTable.SetAddr(pk, url)
			dctx, dcancel := context.WithTimeout(ctx, 25*time.Second)
			_, derr := tpM.SaveTransport(dctx, pk, types.WS, transport.LabelAutomatic)
			dcancel()
			if derr != nil {
				vlog(fmt.Sprintf("ws-autoconnect: WS dial %s (%s): %v", pk.Hex()[:8], url, derr))
			} else {
				got = true
				vlog(fmt.Sprintf("ws-autoconnect: +WS transport to %s via %s", pk.Hex()[:8], url))
			}
		} else if err != nil {
			vlog(fmt.Sprintf("ws-autoconnect: AR stcpr resolve %s: %v", pk.Hex()[:8], err))
		}
	}

	// WT: a QUIC endpoint + pinned self-signed cert, learned from the AR's WT
	// record (https://host:port/skywire).
	url, certHash, werr := ar.resolveWT(ctx, pk)
	switch {
	case werr != nil:
		vlog(fmt.Sprintf("ws-autoconnect: AR wt resolve %s: %v", pk.Hex()[:8], werr))
	case url == "":
		// no WT entry in AR (404) — a WT-less public visor; WebRTC pass may reach it.
	default:
		wtTable.SetEntry(pk, network.WTEntry{URL: url, CertHash: certHash})
		dctx, dcancel := context.WithTimeout(ctx, 25*time.Second)
		_, derr := tpM.SaveTransport(dctx, pk, types.WT, transport.LabelAutomatic)
		dcancel()
		if derr != nil {
			vlog(fmt.Sprintf("ws-autoconnect: WT dial %s (%s): %v", pk.Hex()[:8], url, derr))
		} else {
			got = true
			vlog(fmt.Sprintf("ws-autoconnect: +WT transport to %s via %s", pk.Hex()[:8], url))
		}
	}
	return got
}

// webrtcAvailable reports whether the JS context exposes RTCPeerConnection. Present
// on the page main thread, ABSENT in a Web Worker (where the wasm runtime now runs,
// see pkg/wasmhv/worker.js) — so WebRTC dial + STUN can't run there without a
// main-thread PeerConnection proxy.
func webrtcAvailable() bool {
	return js.Global().Get("RTCPeerConnection").Truthy()
}

var warnedWebRTCUnavailable bool

// warnWebRTCUnavailableOnce logs the "WebRTC off in this context" notice a single
// time (not once per autoconnect cycle). Called from the single autoconnect
// goroutine, so the plain bool needs no lock.
func warnWebRTCUnavailableOnce() {
	if warnedWebRTCUnavailable {
		return
	}
	warnedWebRTCUnavailable = true
	vlog("ws-autoconnect: WebRTC unavailable in this context (no RTCPeerConnection — Web Worker); using direct carriers (WT/WS) only")
}

// dialWebRTC opens a WebRTC DataChannel transport (signalled over dmsg, NAT
// traversing) to a peer no direct carrier could reach. Returns true on success.
func dialWebRTC(ctx context.Context, pk cipher.PubKey) bool {
	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	_, derr := tpM.SaveTransport(dctx, pk, types.WEBRTC, transport.LabelAutomatic)
	dcancel()
	if derr != nil {
		vlog(fmt.Sprintf("ws-autoconnect: WebRTC dial %s: %v", pk.Hex()[:8], derr))
		return false
	}
	vlog(fmt.Sprintf("ws-autoconnect: +WebRTC transport to %s", pk.Hex()[:8]))
	return true
}

// arDmsg is a minimal authenticated address-resolver client over dmsg, mirroring
// tpdclient.NewDmsg: GET the per-key nonce, sign payload+nonce, send
// SW-Public/SW-Nonce/SW-Sig, retry once on nonce mismatch.
type arDmsg struct {
	dmsgC *dmsg.Client
	host  string
	key   cipher.PubKey
	sec   cipher.SecKey
	nonce httpauthclient.Nonce
}

func (a *arDmsg) resolveStcpr(ctx context.Context, target cipher.PubKey) (string, error) {
	status, body, err := a.authedGet(ctx, "/resolve/stcpr/"+target.Hex())
	if err != nil {
		return "", err
	}
	if status == 404 {
		return "", nil // no AR entry for this visor
	}
	if status != 200 {
		return "", fmt.Errorf("AR status %d", status)
	}
	var vd addrresolver.VisorData
	if err := json.Unmarshal(body, &vd); err != nil {
		return "", err
	}
	return vd.RemoteAddr, nil
}

// resolveWT resolves a peer's WebTransport endpoint + pinned cert hash from the
// AR's WT record (registered via BindWT). Returns the https://host:port/skywire
// URL and the SHA-256 cert hash to pin, or ("","",nil) when the peer has no WT
// entry (so the caller just skips WT for that peer).
func (a *arDmsg) resolveWT(ctx context.Context, target cipher.PubKey) (url, certHash string, err error) {
	status, body, err := a.authedGet(ctx, "/resolve/wt/"+target.Hex())
	if err != nil {
		return "", "", err
	}
	if status == 404 {
		return "", "", nil // no WT entry for this visor
	}
	if status != 200 {
		return "", "", fmt.Errorf("AR wt status %d", status)
	}
	var vd addrresolver.VisorData
	if err := json.Unmarshal(body, &vd); err != nil {
		return "", "", err
	}
	if vd.CertHash == "" || vd.RemoteAddr == "" {
		return "", "", nil
	}
	addr := vd.RemoteAddr
	if _, _, e := net.SplitHostPort(addr); e != nil && vd.Port != "" {
		addr = net.JoinHostPort(addr, vd.Port)
	}
	return "https://" + addr + "/skywire", vd.CertHash, nil
}

func (a *arDmsg) authedGet(ctx context.Context, path string) (int, []byte, error) {
	if a.nonce == 0 {
		n, err := a.fetchNonce(ctx)
		if err != nil {
			return 0, nil, err
		}
		a.nonce = n
	}
	status, body, err := a.signedGet(ctx, path)
	if err != nil {
		return 0, nil, err
	}
	if status == 401 && strings.Contains(string(body), "SW-Nonce") { // nonce mismatch
		n, err := a.fetchNonce(ctx)
		if err != nil {
			return 0, nil, err
		}
		a.nonce = n
		if status, body, err = a.signedGet(ctx, path); err != nil {
			return 0, nil, err
		}
	}
	if status >= 200 && status < 300 {
		a.nonce++
	}
	return status, body, nil
}

func (a *arDmsg) signedGet(ctx context.Context, path string) (int, []byte, error) {
	sig, err := httpauthclient.Sign(nil, a.nonce, a.sec)
	if err != nil {
		return 0, nil, err
	}
	headers := map[string]string{
		"SW-Public": a.key.Hex(),
		"SW-Nonce":  strconv.FormatUint(uint64(a.nonce), 10),
		"SW-Sig":    sig.Hex(),
	}
	status, _, body, err := dmsgclient.FetchOverDmsg(ctx, a.dmsgC, "GET", a.host, path, headers, nil)
	return status, body, err
}

func (a *arDmsg) fetchNonce(ctx context.Context) (httpauthclient.Nonce, error) {
	status, _, body, err := dmsgclient.FetchOverDmsg(ctx, a.dmsgC, "GET", a.host, "/security/nonces/"+a.key.Hex(), nil, nil)
	if err != nil {
		return 0, err
	}
	if status != 200 {
		return 0, fmt.Errorf("nonce status %d", status)
	}
	var nr struct {
		NextNonce uint64 `json:"next_nonce"`
	}
	if err := json.Unmarshal(body, &nr); err != nil {
		return 0, err
	}
	return httpauthclient.Nonce(nr.NextNonce), nil
}
