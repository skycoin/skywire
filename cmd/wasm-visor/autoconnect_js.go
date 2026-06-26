//go:build js && wasm

// Package main — in-browser public autoconnect over WS.
//
// A browser leaf can't accept inbound transports, but it CAN dial out. To join
// the mesh (so routes can form) it periodically fetches the public-visor list
// from service discovery, resolves each peer's reachable IP from the address
// resolver, and dials a WebSocket transport to that peer's WS-on-stcpr-port
// (phase 2). WS — not WebRTC — because WS is the transport a *public* visor
// exposes by being publicly reachable (docs/design/wasm-public-autoconnect.md).
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
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	autoconnectInterval  = 90 * time.Second
	autoconnectMaxVisors = 4
)

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

	have := map[cipher.PubKey]bool{}
	n := 0
	tpM.WalkTransports(func(mt *transport.ManagedTransport) bool {
		if mt.Entry.Label == transport.LabelAutomatic {
			have[mt.Remote()] = true
			n++
		}
		return true
	})

	vlog(fmt.Sprintf("ws-autoconnect: SD returned %d visors (%d existing auto-tps)", len(services), n))

	added := 0
	for i := range services {
		if n+added >= autoconnectMaxVisors {
			break
		}
		pk := services[i].Addr.PubKey()
		if pk == selfPK || have[pk] {
			continue
		}
		port := services[i].Addr.Port()
		vlog(fmt.Sprintf("ws-autoconnect: resolving %s:%d", pk.Hex()[:8], port))
		// 2. resolve the peer's reachable IP via the AR (authenticated).
		addr, err := ar.resolveStcpr(ctx, pk)
		if err != nil || addr == "" {
			vlog(fmt.Sprintf("ws-autoconnect: AR resolve %s: %v", pk.Hex()[:8], err))
			continue
		}
		host := addr
		if h, _, e := net.SplitHostPort(addr); e == nil {
			host = h
		}
		url := fmt.Sprintf("ws://%s:%d/", host, port)
		// 3. dial WS (register the endpoint, then SaveTransport dials it).
		wsTable.SetAddr(pk, url)
		dctx, dcancel := context.WithTimeout(ctx, 25*time.Second)
		_, derr := tpM.SaveTransport(dctx, pk, types.WS, transport.LabelAutomatic)
		dcancel()
		if derr != nil {
			vlog(fmt.Sprintf("ws-autoconnect: WS dial %s (%s): %v", pk.Hex()[:8], url, derr))
			continue
		}
		added++
		vlog(fmt.Sprintf("ws-autoconnect: +WS transport to %s via %s", pk.Hex()[:8], url))
	}
	if added > 0 {
		vlog(fmt.Sprintf("ws-autoconnect: established %d WS transport(s) (%d candidates)", added, len(services)))
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
