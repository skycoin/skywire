// Package visor pkg/visor/hypervisor_handlers_wallet.go — the HV-served skycoin
// wallet ("wallet HV-served" mode, docs/design/gui-app-serving-modes.md).
//
// The native hypervisor serves the embedded skycoin-web static bundle at
// /wallet/ and proxies its node API (/api/v1|v2, /csrf) to the configured
// skycoin node over the visor's dmsg client — NO skycoin-web process, NO
// listening port. This is the native equivalent of the wasm visor's /wallet/:
// the wallet UI is static + client-side crypto, wallets live in browser
// storage, and only the node connection crosses the mesh — here proxied
// server-side by the HV instead of by the browser's fetchDmsg shim.
//
// Running the actual skycoin-web app (internal/external, own port, disk
// wallets, server-side multi-coin) stays the opt-in "power" mode; this is the
// zero-config default so native == wasm.
package visor

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/skycoin/skywire/pkg/btcgateway"
)

var (
	nativeBtcGatewayOnce sync.Once
	nativeBtcGatewayInst *btcgateway.Gateway
)

// nativeBtcGateway is the HV's shared BTC electrum gateway for the native
// HV-served wallet. A nil dialer means clearnet egress — a host visor reaches
// public electrum servers directly; the per-request electrum URL comes from the
// X-Skywire-Btc-Backend header (set by the wallet shim from
// localStorage['skywire-btc-backend']).
func nativeBtcGateway() *btcgateway.Gateway {
	nativeBtcGatewayOnce.Do(func() { nativeBtcGatewayInst = btcgateway.New(nil) })
	return nativeBtcGatewayInst
}

// walletNodeDmsg is the skycoin node the HV-served wallet talks to by DEFAULT:
// the deployment node addressed over dmsg (same PK:port as
// services-config.json prod.skycoin_node_dmsg and the wasm wallet's
// defaultCoinNode). Overridden per-request by the X-Skywire-Coin-Node header
// (set from localStorage['skywire-coin-node'] by the injected shim — the
// wallet tab's Node config panel writes that key). TODO(config): also source
// the default from config + support multiple nodes (multi-coin /coin/N).
const walletNodeDmsg = "dmsg://039a6d1e3c237f5f05b78ec19e9f31a007f84835d7ef1e812876102281d1db74c1:6420"

// walletNodeShim is injected into the HV-served wallet index (right after
// <base>). The skycoin-web GUI fetches the node API at absolute /api/v1|v2
// paths; under <base href="/wallet/"> those still resolve to the origin root,
// which would miss this handler. The shim overrides fetch to (a) rewrite
// /api/... → same-origin /wallet/api/... (this handler), and (b) tag the
// operator-selected node (localStorage['skywire-coin-node'], "<pk>:<port>") as
// X-Skywire-Coin-Node so walletNodeProxy dials it over dmsg. Empty selection →
// the deployment default. This is the native twin of the wasm fetchDmsg shim;
// same localStorage key, so node config is unified across both visors.
// It ALSO intercepts the wallet's BTC API (/v1/btc/*) and tags the operator-
// selected electrum backend (localStorage['skywire-btc-backend'], "ssl://host:
// port") as X-Skywire-Btc-Backend, so the HV's in-process BTC gateway
// (pkg/btcgateway) reaches it — keys + signing stay in the browser, only chain
// queries cross. Same localStorage keys as the wasm shim + the config panel.
const walletNodeShim = `<script>(function(){` +
	`var rf=window.fetch?window.fetch.bind(window):null;if(!rf){return;}` +
	`function ls(k){try{return localStorage.getItem(k)||"";}catch(e){return "";}}` +
	`function pathOf(u){try{return new URL(u,location.href).pathname;}catch(e){return String(u);}}` +
	`window.fetch=function(input,init){` +
	`var url=(typeof input==="string")?input:(input&&input.url)||"";` +
	`var p=pathOf(url);` +
	`var mn=/^\/(wallet\/)?api\/v[12]\//.exec(p);` +
	`var mb=/^\/(wallet\/)?v1\/btc\//.exec(p);` +
	`if(!mn&&!mb){return rf(input,init);}` +
	`var pre=(mn&&mn[1])||(mb&&mb[1]);` +
	`var target=pre?url:url.replace(p,"/wallet"+p);` +
	`init=init||{};` +
	`var h=new Headers((init&&init.headers)||(typeof input!=="string"&&input&&input.headers)||undefined);` +
	`if(mn){var n=ls("skywire-coin-node");if(n){h.set("X-Skywire-Coin-Node",n);}}` +
	`else{var b=ls("skywire-btc-backend");if(b){h.set("X-Skywire-Btc-Backend",b);}}` +
	`init.headers=h;return rf(target,init);` +
	`};})();</script>`

// walletHandler serves /wallet/* : node API calls are proxied to the skycoin
// node over dmsg; everything else is the embedded static wallet bundle (the
// index's <base href> rewritten to /wallet/ so relative asset + /api paths
// resolve under the mount and land back on this handler).
func (hv *Hypervisor) walletHandler() http.HandlerFunc {
	uiFS, fsErr := HypervisorUIFS()
	var fileServer http.Handler
	if fsErr == nil {
		fileServer = http.FileServer(http.FS(uiFS))
	}
	return func(w http.ResponseWriter, r *http.Request) {
		rest := chi.URLParam(r, "*")
		// Node API (/wallet/api/v1|v2/*) → proxy to the node over dmsg. The
		// wallet's crypto is client-side; only these calls cross the mesh.
		if strings.HasPrefix(rest, "api/v1/") || strings.HasPrefix(rest, "api/v2/") {
			hv.walletNodeProxy(w, r, rest)
			return
		}
		// BTC API (/wallet/v1/btc/*) → the in-process electrum gateway. The
		// browser derives + signs BTC itself; only chain queries come here. The
		// electrum server (X-Skywire-Btc-Backend, set by the shim) is dialed on
		// the clearnet by the native visor (nil dialer).
		if strings.HasPrefix(rest, "v1/btc/") {
			r.URL.Path = "/" + rest
			nativeBtcGateway().ServeHTTP(w, r)
			return
		}
		if fsErr != nil {
			http.Error(w, "wallet UI not embedded in this build", http.StatusNotFound)
			return
		}
		// index.html: rewrite <base href="/"> → "/wallet/" so the SPA's
		// relative asset + API URLs resolve under the mount.
		if rest == "" || rest == "index.html" {
			hv.serveWalletIndex(w, r, uiFS)
			return
		}
		// Static asset: serve wallet/<rest> from the embedded UI FS.
		r.URL.Path = "/wallet/" + rest
		fileServer.ServeHTTP(w, r)
	}
}

// serveWalletIndex serves the embedded wallet index.html with its <base href>
// rewritten from the vendored root ("/") to the mount ("/wallet/").
func (hv *Hypervisor) serveWalletIndex(w http.ResponseWriter, _ *http.Request, uiFS fs.FS) {
	b, err := fs.ReadFile(uiFS, "wallet/index.html")
	if err != nil {
		http.Error(w, "wallet UI not embedded in this build", http.StatusNotFound)
		return
	}
	b = []byte(strings.Replace(string(b), `<base href="/">`, `<base href="/wallet/">`+walletNodeShim, 1))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b) //nolint:errcheck
}

// walletNodeProxy forwards a wallet node-API call to the configured coin
// backend using the SAME resolving fetch the native browser uses — no bespoke
// routing. A mesh host (<pk>[:port] / <pk>.dmsg / alias / name.skynet) goes via
// BrowseFetch (resolve + dmsg/skynet); a clearnet URL goes via BrowseClearnet
// with the self exit (the local visor does the egress). The mesh-vs-clearnet
// dispatch is the same one browse.js makes. rest is the path after /wallet/.
func (hv *Hypervisor) walletNodeProxy(w http.ResponseWriter, r *http.Request, rest string) {
	if hv.visor == nil {
		http.Error(w, "visor not ready", http.StatusServiceUnavailable)
		return
	}
	backend := strings.TrimSpace(r.Header.Get("X-Skywire-Coin-Node"))
	if backend == "" {
		backend = walletNodeDmsg
	}
	path := "/" + rest
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20)) //nolint:errcheck
	}

	header := map[string]string{}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		header["Content-Type"] = ct
	}
	if csrf := r.Header.Get("X-CSRF-Token"); csrf != "" {
		header["X-CSRF-Token"] = csrf
	}

	if walletBackendIsClearnet(backend) {
		// Clearnet → BrowseClearnet with the self exit: the visor does the
		// egress itself (the parity path the native browser already uses).
		u := backend
		if l := strings.ToLower(u); !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
			u = "http://" + u
		}
		resp, err := hv.visor.BrowseClearnet(BrowseClearnetRequest{
			ExitPK: hv.visor.conf.PK,
			Method: r.Method,
			URL:    strings.TrimRight(u, "/") + path,
			Body:   body,
		})
		if err != nil {
			http.Error(w, "node unreachable: "+err.Error(), http.StatusBadGateway)
			return
		}
		walletWriteResp(w, resp.StatusCode, resp.Header, resp.Body)
		return
	}

	// Mesh coin node: resolve the host with the SAME resolver the iframe browser
	// uses (bare "<pk>[:port]", the readable "<name>.<pk>.dmsg[:port]" alias,
	// "alias.dmsg", …) via resolveBrowseHost, then dmsg-HTTP over the
	// AUTHORITATIVE dmsg client. We reuse the resolver but NOT BrowseFetch's
	// fetch step: BrowseFetch dials over the secondary dmsg client (v.dmsgHTTP /
	// dmsgDC), which has session conflicts on the coin node (see Visor.DmsgHTTP);
	// v.dmsgC (DmsgHTTP) has stable sessions.
	pk, port, rerr := hv.visor.resolveBrowseHost(walletBackendStrip(backend), 0)
	if rerr != nil {
		http.Error(w, "coin node resolve failed: "+rerr.Error(), http.StatusBadGateway)
		return
	}
	resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{
		URL:    fmt.Sprintf("dmsg://%s:%d%s", pk.Hex(), port, path),
		Method: r.Method,
		Header: header,
		Body:   body,
	})
	if err != nil {
		http.Error(w, "node unreachable over dmsg: "+err.Error(), http.StatusBadGateway)
		return
	}
	walletWriteResp(w, resp.StatusCode, resp.Header, resp.Body)
}

// walletWriteResp writes a proxied backend response (shared by the dmsg and
// clearnet paths — both carry StatusCode + Header map + Body).
func walletWriteResp(w http.ResponseWriter, status int, header map[string]string, body []byte) {
	if ct := header["Content-Type"]; ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body) //nolint:errcheck
}

// walletBackendStrip drops the scheme (dmsg://, skynet://, http(s)://) + any
// trailing path, leaving the host[:port] BrowseFetch's resolver keys on.
func walletBackendStrip(b string) string {
	b = strings.TrimSpace(b)
	b = strings.TrimPrefix(b, "dmsg://")
	b = strings.TrimPrefix(b, "skynet://")
	return browseStripHost(b)
}

// walletBackendIsClearnet reports whether a backend is a plain clearnet host —
// i.e. NOT a mesh host (bare <pk>[:port], <pk>.dmsg, alias.dmsg, name.skynet).
// Same split browse.js makes: clearnet → BrowseClearnet (self exit), mesh →
// BrowseFetch.
func walletBackendIsClearnet(b string) bool {
	h := walletBackendStrip(b)
	if i := strings.LastIndexByte(h, ':'); i > 0 {
		h = h[:i]
	}
	lower := strings.ToLower(h)
	if strings.HasSuffix(lower, ".dmsg") || strings.HasSuffix(lower, ".skynet") {
		return false
	}
	if len(h) == 66 && isHexStr(h) {
		return false
	}
	return true
}

func isHexStr(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return s != ""
}
