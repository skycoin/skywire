//go:build !mobile

// Package visor pkg/visor/hypervisor_handlers_wallet.go c3-vis-core
// wallet ("wallet HV-served" mode, docs/design/gui-app-serving-modes.md).
//
// The wallet is the dashboard's #/wallet page: skycoin-web's wallet module
// compiled into the dashboard, its crypto client-side in skycoin's TinyGo
// cipher, its wallets in browser storage. The hypervisor serves that cipher and
// proxies the wallet's node API (wallet/coins, wallet/coin/<index>/…) to the
// configured backend over the visor's dmsg client: NO skycoin-web process, NO
// listening port. The same handlers run in the tab's wasm hypervisor, so
// native == wasm.
//
// When the skycoin-web app runs in this process, wallet/coins and
// wallet/coin/<index>/… go to it instead, so the same page uses its node
// settings and its server-side wallet files.
package visor

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	wasmtinygo "github.com/skycoin/skycoin/src/skycoin-lite/wasm-tinygo"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/btcgateway"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/wallet/coins"
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
// X-Skywire-Btc-Proxy — both set by the dashboard wallet's interceptor from
// localStorage (skywire-btc-backend / skywire-btc-proxy, written by the config
// page).
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

// walletHandler serves /wallet/*: the wallet-config page the dashboard's
// wallet tab frames, the coin registry, and each coin's node API. The wallet
// page itself is the dashboard's #/wallet, so a browser opening the address it
// used to have here is sent there.
func (hv *Hypervisor) walletHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := r.PathValue("rest")
		// A running skycoin-web app answers the coin list and every coin's API
		// itself, including its server-side wallets, so the page uses those.
		if app := launcher.GetHTTPHandler(skyenv.SkycoinWebName); app != nil {
			switch {
			case rest == "coins":
				serveAt(app, w, r, "/api/v1/coins")
				return
			case strings.HasPrefix(rest, "coin/"):
				serveAt(app, w, r, "/"+rest)
				return
			}
		}
		switch {
		case rest == "" || rest == "index.html":
			toDashboardWallet(w)
		case rest == "config":
			// The ONE wallet config page (mode / coin nodes / BTC / skysocks exit),
			// framed by the dashboard's wallet tab. Opened on its own it is out of
			// context, so it goes to the wallet as well.
			if r.Header.Get("Sec-Fetch-Dest") == "document" {
				toDashboardWallet(w)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte(browseui.WalletConfigHTML)) //nolint:errcheck
		case rest == "coins":
			// Coin registry → the list skycoin-web's coin.service fetches as
			// /api/v1/coins. Each coin's nodeUrl is a /coin/<index> prefix proxied
			// below.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(coins.JSON()) //nolint:errcheck
		case strings.HasPrefix(rest, "coin/"):
			// Per-coin API → that coin's backend: a skycoin-style node over dmsg,
			// or the in-visor BTC electrum gateway.
			hv.walletCoinProxy(w, r, strings.TrimPrefix(rest, "coin/"))
		default:
			http.NotFound(w, r)
		}
	}
}

// serveAt hands r to h with its path replaced.
func serveAt(h http.Handler, w http.ResponseWriter, r *http.Request, path string) {
	r2 := r.Clone(r.Context())
	r2.URL.Path, r2.URL.RawPath, r2.RequestURI = path, "", ""
	h.ServeHTTP(w, r2)
}

// walletCoinProxy routes a per-coin API call (/wallet/coin/<index>/<path>) to
// the coin's backend. rest is "<index>/<path…>". Skycoin-style coins go to the
// dmsg node proxy; bitcoin coins go to the in-visor electrum gateway. This is
// the server-proxy half of skycoin-web's /coin/<index> multicoin model — see
// docs/design/skycoin-web-multicoin-wallets.md.
func (hv *Hypervisor) walletCoinProxy(w http.ResponseWriter, r *http.Request, rest string) {
	idxStr, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		idxStr, path = rest[:i], rest[i+1:]
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		http.Error(w, "invalid coin index", http.StatusBadRequest)
		return
	}
	coin, ok := coins.ByIndex(idx)
	if !ok {
		http.Error(w, "unknown coin", http.StatusNotFound)
		return
	}
	if coin.IsBitcoin() {
		// The browser derives + signs BTC itself; only chain queries come here,
		// to the in-process electrum gateway. Normalize the path to /v1/btc/… (the
		// wallet addresses it as coin/<index>/api/v1/btc/…). The backend electrum
		// server (X-Skywire-Btc-Backend) is reached on the clearnet by default, or
		// via a skysocks exit (X-Skywire-Btc-Proxy) for IP privacy — headers set
		// by the dashboard wallet's interceptor from localStorage.
		if i := strings.Index(path, "v1/btc/"); i >= 0 {
			r.URL.Path = "/" + path[i:]
		} else {
			r.URL.Path = "/" + path
		}
		hv.nativeBtcGateway(r.Header.Get("X-Skywire-Btc-Proxy")).ServeHTTP(w, r)
		return
	}
	// Skycoin-style node: proxy the remaining api/v1|v2 path to the coin's node
	// over dmsg (X-Skywire-Coin-Node selects it; empty = deployment default).
	hv.walletNodeProxy(w, r, path)
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
	// Server-side request log, the parity of the standalone `skywire skycoin web`
	// proxy logs. Lets a native-HV operator see what wallet traffic is crossing
	// the mesh (visible at -sl debug) even before a wallet is unlocked.
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
	// visor's dmsg client or its skynet transports (Visor.DmsgHTTP), which
	// keeps its connections to the node open between requests.
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
	resp, err := hv.visor.DmsgHTTP(visorapi.DmsgHTTPRequest{
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

// walletCipherHandler answers the cipher the dashboard's wallet route loads:
// assets/scripts/wasm_exec.js, which it adds to the page, and
// assets/scripts/skycoin-lite.wasm, which the wallet's CipherProvider fetches
// relative to the page. Both are skycoin's TinyGo build of skycoin-lite, from
// the same module version as the wallet source the dashboard compiles. The
// wasm is embedded gzipped and goes out that way.
func (hv *Hypervisor) walletCipherHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if strings.HasSuffix(r.URL.Path, "/wasm_exec.js") {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			_, _ = w.Write(wasmtinygo.WasmExecJS) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/wasm")
		w.Header().Add("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(wasmtinygo.WasmFileGz) //nolint:errcheck
			return
		}
		b, err := wasmtinygo.WasmFile()
		if err != nil {
			http.Error(w, "wallet cipher: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b) //nolint:errcheck
	}
}

// toDashboardWallet sends a browser to the dashboard's #/wallet page. The
// Location stays relative: http.Redirect would make it absolute from the path
// this handler sees, which in the desk lacks the page's /vnet/<port>/ prefix.
func toDashboardWallet(w http.ResponseWriter) {
	w.Header().Set("Location", "../#/wallet")
	w.WriteHeader(http.StatusSeeOther)
}
