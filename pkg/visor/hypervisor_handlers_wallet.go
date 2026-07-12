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
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

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
const walletNodeShim = `<script>(function(){` +
	`var rf=window.fetch?window.fetch.bind(window):null;if(!rf){return;}` +
	`function node(){try{return localStorage.getItem("skywire-coin-node")||"";}catch(e){return "";}}` +
	`function pathOf(u){try{return new URL(u,location.href).pathname;}catch(e){return String(u);}}` +
	`window.fetch=function(input,init){` +
	`var url=(typeof input==="string")?input:(input&&input.url)||"";` +
	`var m=/^\/(wallet\/)?api\/v[12]\//.exec(pathOf(url));` +
	`if(!m){return rf(input,init);}` +
	`var target=m[1]?url:url.replace(pathOf(url),"/wallet"+pathOf(url));` +
	`init=init||{};` +
	`var h=new Headers((init&&init.headers)||(typeof input!=="string"&&input&&input.headers)||undefined);` +
	`var n=node();if(n){h.set("X-Skywire-Coin-Node",n);}` +
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

// walletNodeProxy forwards a wallet node-API call to the skycoin node over the
// visor's dmsg client (the server-side equivalent of the wasm wallet's
// fetchDmsg shim). rest is the path after /wallet/ (e.g. "api/v1/health").
func (hv *Hypervisor) walletNodeProxy(w http.ResponseWriter, r *http.Request, rest string) {
	if hv.visor == nil {
		http.Error(w, "visor not ready", http.StatusServiceUnavailable)
		return
	}
	// Node selection: the operator's configured node (X-Skywire-Coin-Node,
	// "<pk>:<port>") when present + valid, else the deployment default.
	node := walletNodeDmsg
	if n := normalizeCoinNode(r.Header.Get("X-Skywire-Coin-Node")); n != "" {
		node = n
	}
	url := node + "/" + rest
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
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
	resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{
		URL:    url,
		Method: r.Method,
		Header: header,
		Body:   body,
	})
	if err != nil {
		http.Error(w, "node unreachable over dmsg: "+err.Error(), http.StatusBadGateway)
		return
	}
	if ct := resp.Header["Content-Type"]; ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body) //nolint:errcheck
}

// normalizeCoinNode validates an operator-supplied coin-node selection and
// returns it as a dmsg:// URL, or "" if it isn't a well-formed dmsg address.
// Accepts "<pk>:<port>" or "dmsg://<pk>:<port>" (pk = 66 hex chars). Rejecting
// anything else keeps the header from being used to point the proxy at an
// arbitrary non-dmsg URL.
func normalizeCoinNode(n string) string {
	n = strings.TrimSpace(n)
	n = strings.TrimPrefix(n, "dmsg://")
	if n == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(n)
	if err != nil || len(host) != 66 {
		return ""
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ""
	}
	for _, c := range host {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return ""
		}
	}
	return "dmsg://" + n
}
