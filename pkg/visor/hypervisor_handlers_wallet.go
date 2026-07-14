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

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"

	"github.com/skycoin/skywire/pkg/btcgateway"
	"github.com/skycoin/skywire/pkg/wasmhv/browseui"
)

var (
	nativeBtcGatewaysMu sync.Mutex
	nativeBtcGateways   = map[string]*btcgateway.Gateway{}
)

// nativeBtcGateway is the HV's BTC electrum gateway for the given skysocks exit.
// exitPK == "" (or the local visor's own PK) means clearnet egress: a nil dialer
// so the host visor reaches public electrum servers over its own default route
// (the zero-config native default — self-egress). A non-empty REMOTE exit PK
// routes the electrum connection through that visor's skysocks-server for IP
// privacy (the native twin of the wasm wallet's required skysocks exit). The
// per-request electrum URL comes from X-Skywire-Btc-Backend; the exit from
// X-Skywire-Btc-Proxy — both set by the wallet shim from localStorage
// (skywire-btc-backend / skywire-btc-proxy, written by the config page).
//
// Gateways are cached per exit so the electrum backends (and their long-lived
// per-server connections) are reused across chain queries.
func (hv *Hypervisor) nativeBtcGateway(exitPK string) *btcgateway.Gateway {
	exitPK = strings.TrimSpace(exitPK)
	nativeBtcGatewaysMu.Lock()
	defer nativeBtcGatewaysMu.Unlock()
	if g, ok := nativeBtcGateways[exitPK]; ok {
		return g
	}
	var g *btcgateway.Gateway
	if exitPK != "" && hv.visor != nil {
		var pk cipher.PubKey
		if err := pk.Set(exitPK); err == nil && pk != hv.visor.conf.PK {
			g = btcgateway.New(hv.visor.skysocksDialFunc(pk))
		}
	}
	if g == nil { // empty / self / unparseable exit → clearnet self-egress
		g = btcgateway.New(nil)
	}
	nativeBtcGateways[exitPK] = g
	return g
}

// walletNodeDefault is the skycoin node the HV-served wallet talks to by DEFAULT,
// sourced from the embedded services-config.json (prod.skycoin_node_dmsg) — the
// same value the wasm wallet's coinNodeDefault() uses. Overridden per-request by
// the X-Skywire-Coin-Node header (from localStorage['skywire-coin-node'], set by
// the wallet config panel). Falls back to the node.skycoin.com vhost form if the
// deployment config field is empty. TODO(config): multi-coin /coin/N.
func walletNodeDefault() string {
	if n := strings.TrimSpace(dmsg.Prod.SkycoinNode); n != "" {
		return n
	}
	return "https://node.skycoin.com.aong2hr4en7v6bnxr3az5hzruad7qsbv27xr5ajioyicfaor3n2mc.dmsg"
}

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
	`function searchOf(u){try{return new URL(u,location.href).search;}catch(e){return "";}}` +
	`function hostOf(u){try{var x=new URL(u,location.href);return (x.host&&x.host!==location.host)?(x.protocol+"//"+x.host):"";}catch(e){return "";}}` +
	// BTC = any path containing /v1/btc/ — the raw /v1/btc/health probe AND
	// skycoin-web's apiService form /api/v1/btc/{balance,history,send} (which
	// also matches the node regex, so BTC is classified FIRST and wins). No
	// skycoin node endpoint contains /v1/btc/.
	`var mb=/\/v1\/btc\//.test(p);` +
	`var mn=!mb&&/^\/(wallet\/)?api\/v[12]\//.test(p);` +
	`if(!mn&&!mb){return rf(input,init);}` +
	`init=init||{};` +
	`var h=new Headers((init&&init.headers)||(typeof input!=="string"&&input&&input.headers)||undefined);` +
	`var target;` +
	`if(mb){` +
	// Normalize any /v1/btc/ path (bare or /api/v1/btc/) to /wallet/v1/btc/… so
	// the server routes it to the in-process electrum gateway, not the node.
	`var tail=p.slice(p.indexOf("/v1/btc/"));target=location.origin+"/wallet"+tail+searchOf(url);` +
	`var b=ls("skywire-btc-backend");if(b){h.set("X-Skywire-Btc-Backend",b);}` +
	`var xp=ls("skywire-btc-proxy");if(xp){h.set("X-Skywire-Btc-Proxy",xp);}` +
	`}else{` +
	// Node API: skycoin-web addresses a custom node (Settings → Nodes /
	// customNodeUrls) with an ABSOLUTE url — strip the host, keep the /api path +
	// query, and re-point at /wallet/api here; the chosen node travels in
	// X-Skywire-Coin-Node so walletNodeProxy dials it (its Nodes GUI is
	// authoritative; a same-origin/relative /api path falls back to the legacy
	// config-panel key, else the deployment mesh default).
	`var mnx=/^\/(wallet\/)?api\/v[12]\//.exec(p);var pre=mnx&&mnx[1];` +
	`target=pre?url:(location.origin+"/wallet"+p+searchOf(url));` +
	`var nn=hostOf(url)||ls("skywire-coin-node");if(nn){h.set("X-Skywire-Coin-Node",nn);}` +
	`}` +
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
		// The ONE wallet config page (mode / coin nodes / BTC / skysocks exit),
		// embedded via iframe by the ☰ wallet window AND the Angular wallet tab.
		if rest == "config" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte(browseui.WalletConfigHTML)) //nolint:errcheck
			return
		}
		// Node API (/wallet/api/v1|v2/*) → proxy to the node over dmsg. The
		// wallet's crypto is client-side; only these calls cross the mesh.
		if strings.HasPrefix(rest, "api/v1/") || strings.HasPrefix(rest, "api/v2/") {
			hv.walletNodeProxy(w, r, rest)
			return
		}
		// BTC API (/wallet/v1/btc/*) → the in-process electrum gateway. The
		// browser derives + signs BTC itself; only chain queries come here. The
		// electrum server (X-Skywire-Btc-Backend) is reached either directly on
		// the clearnet (self-egress, the default) or — when the operator sets a
		// skysocks exit (X-Skywire-Btc-Proxy) — routed through that visor for IP
		// privacy. Both headers are set by the shim from localStorage.
		if strings.HasPrefix(rest, "v1/btc/") {
			r.URL.Path = "/" + rest
			hv.nativeBtcGateway(r.Header.Get("X-Skywire-Btc-Proxy")).ServeHTTP(w, r)
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
		backend = walletNodeDefault()
	}
	path := "/" + rest
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	// Server-side request log — the native parity of the wasm shim's [wallet]
	// skywireLog lines and of the standalone `skywire skycoin web` proxy logs.
	// Lets a native-HV operator see what wallet traffic is crossing the mesh
	// (visible at -sl debug) even before a wallet is unlocked.
	hv.logger.WithField("backend", backend).Debugf("[wallet] node %s %s", r.Method, path)
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
			hv.logger.WithError(err).Warnf("[wallet] node clearnet unreachable: %s", backend)
			http.Error(w, "node unreachable: "+err.Error(), http.StatusBadGateway)
			return
		}
		hv.logger.Debugf("[wallet] node %s %s → %d", r.Method, path, resp.StatusCode)
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
	pk, port, vhost, rerr := hv.visor.resolveBrowseHost(walletBackendStrip(backend), 0)
	if rerr != nil {
		http.Error(w, "coin node resolve failed: "+rerr.Error(), http.StatusBadGateway)
		return
	}
	// A "<name>.<pk>.dmsg" backend (e.g. node.skycoin.com.<pk>.dmsg) resolves to a
	// vhost; send it as Host so the destination visor's port-80 forward (Caddy,
	// --preserve-host) vhost-routes /api to the skycoin node — the same Host the
	// wasm wallet's fetchDmsg sets, and what clearnet uses.
	if vhost != "" {
		header["Host"] = vhost
	}
	resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{
		URL:    fmt.Sprintf("dmsg://%s:%d%s", pk.Hex(), port, path),
		Method: r.Method,
		Header: header,
		Body:   body,
	})
	if err != nil {
		hv.logger.WithError(err).Warnf("[wallet] node dmsg unreachable: %s", backend)
		http.Error(w, "node unreachable over dmsg: "+err.Error(), http.StatusBadGateway)
		return
	}
	hv.logger.Debugf("[wallet] node %s %s → %d", r.Method, path, resp.StatusCode)
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
