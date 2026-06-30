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
	autoconnectInterval  = 90 * time.Second
	autoconnectMaxVisors = 4
	// After all carriers to a peer fail (unreachable / NAT'd / no WT listener),
	// don't re-dial it for this long. Without it the loop re-dials the same dead
	// public visors every cycle — each failed WT dial makes the browser log a
	// net::ERR_CONNECTION_REFUSED, and the 4-visor budget is burned on known-dead
	// peers instead of reaching further down the SD list to a reachable one.
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

	// Count DISTINCT visors we already auto-connect to (a visor may hold both a
	// WS and a WT transport — see below — so count peers, not transports).
	have := map[cipher.PubKey]bool{}
	tpM.WalkTransports(func(mt *transport.ManagedTransport) bool {
		if mt.Entry.Label == transport.LabelAutomatic {
			have[mt.Remote()] = true
		}
		return true
	})
	n := len(have)

	// On an HTTPS page the browser blocks plain ws:// (mixed content), and a
	// public visor has no wss endpoint (no domain/CA) — so a WS transport to it
	// can never form. WebTransport (QUIC, cert-hash pinned) is the only
	// HTTPS-viable DIRECT carrier, so dial WT only there; an http:// page still
	// dials both for redundancy.
	https := pageHTTPS()

	vlog(fmt.Sprintf("ws-autoconnect: SD returned %d visors (%d existing auto-peers)", len(services), n))

	added := 0
	skippedCooldown := 0
	for i := range services {
		if n+added >= autoconnectMaxVisors {
			break
		}
		pk := services[i].Addr.PubKey()
		if pk == selfPK || have[pk] {
			continue
		}
		// Skip peers that failed all carriers recently — they're unreachable
		// (NAT'd / no WT listener); re-dialing only spams net::ERR_* and wastes
		// the visor budget. Skipping them silently lets the loop reach a
		// reachable peer further down the list. Cooldown expiry → re-tried.
		if ts, bad := autoconnectFailed[pk]; bad {
			if time.Since(ts) < autoconnectFailCooldown {
				skippedCooldown++
				continue
			}
			delete(autoconnectFailed, pk)
		}
		port := services[i].Addr.Port()
		vlog(fmt.Sprintf("ws-autoconnect: resolving %s:%d", pk.Hex()[:8], port))

		// A browser can carry WS (TCP) and WT (QUIC). On an http page dial BOTH to
		// each public visor for redundancy + path diversity (the browser analog of
		// a native visor making stcpr+sudph to the same peer); on an HTTPS page
		// only WT is reachable (ws:// is mixed-content-blocked). Either succeeding
		// counts the visor as connected.
		gotAny := false

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
					gotAny = true
					vlog(fmt.Sprintf("ws-autoconnect: +WS transport to %s via %s", pk.Hex()[:8], url))
				}
			} else if err != nil {
				vlog(fmt.Sprintf("ws-autoconnect: AR stcpr resolve %s: %v", pk.Hex()[:8], err))
			}
		}

		// WT: a separate QUIC endpoint with its own UDP port + pinned self-signed
		// cert, both learned from the AR's WT record (https://host:port/skywire).
		url, certHash, werr := ar.resolveWT(ctx, pk)
		switch {
		case werr != nil:
			vlog(fmt.Sprintf("ws-autoconnect: AR wt resolve %s: %v", pk.Hex()[:8], werr))
		case url == "":
			vlog(fmt.Sprintf("ws-autoconnect: %s has no WT entry in AR (404) — skip", pk.Hex()[:8]))
		default:
			wtTable.SetEntry(pk, network.WTEntry{URL: url, CertHash: certHash})
			dctx, dcancel := context.WithTimeout(ctx, 25*time.Second)
			_, derr := tpM.SaveTransport(dctx, pk, types.WT, transport.LabelAutomatic)
			dcancel()
			if derr != nil {
				vlog(fmt.Sprintf("ws-autoconnect: WT dial %s (%s): %v", pk.Hex()[:8], url, derr))
			} else {
				gotAny = true
				vlog(fmt.Sprintf("ws-autoconnect: +WT transport to %s via %s", pk.Hex()[:8], url))
			}
		}

		// WebRTC: the universal fallback. A DataChannel signalled over dmsg, it
		// traverses NAT (ICE/STUN) and reaches ANY visor — NAT'd or not, http or
		// https — so it's what actually connects a browser tab when the direct
		// carriers can't: WS is mixed-content-blocked on https, and WT only
		// reaches peers that are both publicly UDP-reachable and on the
		// unified-transport-port build. Heavier than a direct QUIC transport, so
		// it's a fallback: only dialled when WS/WT didn't already form one.
		if !gotAny {
			dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
			_, derr := tpM.SaveTransport(dctx, pk, types.WEBRTC, transport.LabelAutomatic)
			dcancel()
			if derr != nil {
				vlog(fmt.Sprintf("ws-autoconnect: WebRTC dial %s: %v", pk.Hex()[:8], derr))
			} else {
				gotAny = true
				vlog(fmt.Sprintf("ws-autoconnect: +WebRTC transport to %s", pk.Hex()[:8]))
			}
		}

		if gotAny {
			added++
			delete(autoconnectFailed, pk)
		} else {
			// All carriers failed — back off this peer (see autoconnectFailCooldown).
			autoconnectFailed[pk] = time.Now()
		}
	}
	if added > 0 {
		vlog(fmt.Sprintf("ws-autoconnect: connected %d new visor(s) over WS/WT (%d candidates)", added, len(services)))
	}
	if skippedCooldown > 0 {
		vlog(fmt.Sprintf("ws-autoconnect: skipped %d unreachable peer(s) in cooldown", skippedCooldown))
	}
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
