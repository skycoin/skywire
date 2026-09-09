//go:build !mobile

// Package visor pkg/visor/hypervisor_handlers_browse.go c3-vis-core
// browse.js virtual-browser engine in the NATIVE hypervisor UI (the same one the
// wasm-visor runs), backed by /api/browse/* instead of the wasm JS hooks.
package visor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"github.com/skycoin/skywire/pkg/wasmhv"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/wasmhv/browseui"
	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin"
)

// postBrowseFetch fetches a dmsg/skynet site for the in-UI browser (local visor).
func (hv *Hypervisor) postBrowseFetch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req BrowseFetchRequest
		if err := httputil.ReadJSON(r, &req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		resp, err := hv.visor.BrowseFetch(req)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, resp)
	}
}

// postBrowseClearnet fetches a clearnet URL through a skysocks exit over a route.
func (hv *Hypervisor) postBrowseClearnet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req BrowseClearnetRequest
		if err := httputil.ReadJSON(r, &req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		resp, err := hv.visor.BrowseClearnet(req)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, resp)
	}
}

// uiHandler wraps the embedded-UI file server to ALSO serve the browse engine +
// native launcher and to inject them (plus the local visor PK) into index.html,
// so the native hypervisor dashboard gets the same skynet/clearnet browser the
// wasm-visor has.
func (hv *Hypervisor) uiHandler() http.Handler {
	fileServer := uiCacheControl(http.FileServer(http.FS(hv.c.UIAssets)))
	// Fingerprint the desk-host blob once, so a rebuilt binary serves a fresh
	// module and an unchanged one 304s instead of re-sending ~59MB. Hash the
	// COMPRESSED bytes: they are already in memory, and they change exactly
	// when the module does.
	deskWasmETag := func() string {
		h := sha256.Sum256(wasmbin.GetVariantGz(wasmbin.Default()))
		return `"` + hex.EncodeToString(h[:])[:16] + `"`
	}()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/browse.js":
			serveJS(w, browseui.BrowseJS)
			return
		case "/vnet-sw.js":
			// bottle's vnet service worker: pages that run in-page servers
			// (a wasm visor in a tab) register it to give their nested
			// browser real /vnet/<port>/ URLs. Same asset on every desk-ish
			// origin, so a page served by the native HV behaves like one
			// served by `hv serve`.
			serveJS(w, browseui.VNetSWJS())
			return
		case "/winbox.wasm":
			// The window manager the browse bundle loads. instantiateStreaming
			// refuses a module that does not arrive as application/wasm.
			w.Header().Set("Content-Type", "application/wasm")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(browseui.WinBoxWasm()) //nolint:errcheck
			return
		case "/wasm-visor.wasm":
			// The desk-host blob. netscrape — the nested browser the desk
			// renders its windows as TABS in — is Go/wasm and lives in this
			// module (cmd/wasm-visor/browser_js.go installBrowser). Without it
			// this origin has a window manager and no browser, so the
			// hypervisor UI can only open as a bare iframe in a WinBox, which
			// is not what the wasm desk at `hv serve` looks like.
			//
			// It boots in the "shell" ROLE: surfaces only (shell + browser +
			// desk panel), boot() is never called, no visor runs in this tab.
			// The native hypervisor already IS the visor.
			w.Header().Set("Content-Type", "application/wasm")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("ETag", deskWasmETag)
			if r.Header.Get("If-None-Match") == deskWasmETag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			b, err := wasmbin.Get()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(b) //nolint:errcheck
			return
		case "/wasm_exec.js":
			// Go's loader, and it must be the one that PAIRS with the blob
			// above — a std-Go wasm_exec.js cannot run a TinyGo module or vice
			// versa (see wasmbin/embed.go).
			serveJS(w, wasmbin.WasmExecJSVariant(wasmbin.Default()))
			return
		case "/skywire.wasm":
			// The FULL skywire command module — the visor the served desk runs
			// in its terminal — from the same file the wasm-serve port serves,
			// when the operator has one (hypervisor.wasm_serve.exec_wasm).
			// Absent that, the desk boots with no visor of its own, as before.
			if p := hv.execWasmPath(); p != "" {
				w.Header().Set("Content-Type", "application/wasm")
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, p)
				return
			}
			http.NotFound(w, r)
			return
		case "/skywire-worker.js":
			// The worker every skywire command runs on when the page hosts one.
			serveJS(w, wasmhv.ExecWorkerJS())
			return
		case "/desk-boot.js":
			// The shared desk boot (skywireDeskBoot) — same asset `hv serve`
			// exposes, so /desk below boots through the one entry point both
			// serving contexts share.
			serveJS(w, browseui.DeskBootJS())
			return
		case "/", "/index.html":
			// EMBEDDED: serve the dashboard itself, not the desk. A desk inside
			// a desk window is never what the embedder wanted, and the root is
			// the only path that survives being framed — a page served under a
			// /vnet/<port>/ prefix gets its <base href> rewritten to that
			// prefix, so any deeper path (".../dashboard/") is normalised away
			// before the document even finishes loading, landing back here.
			//
			// Sec-Fetch-Dest is the reliable signal and survives a frame
			// reload; ?embed=1 is the explicit override for callers that set
			// it (and for engines that omit the header).
			if r.Header.Get("Sec-Fetch-Dest") == "iframe" || r.URL.Query().Get("embed") == "1" {
				hv.serveInjectedIndex(w, r, fileServer)
				return
			}
			// The DESK is the hypervisor UI (operator decision 2026-09-04): the
			// shell greets at the root with the Angular dashboard as a tab
			// inside it, matching the wasm visor's desk surface.
			hv.serveNativeDesk(w)
			return
		case "/desk":
			// The old separate desk path — gone; the desk IS the root now.
			//
			// The Location is written by hand, and is RELATIVE. Reached
			// through the vnet service worker this page lives under a
			// /vnet/<port>/ prefix that the server never sees, so an absolute
			// "/" escapes the prefix and lands on the OUTER server's root — a
			// different visor entirely. "./" is resolved by the BROWSER
			// against the URL it actually asked for, which keeps the prefix.
			// http.Redirect cannot be used here: it resolves a relative
			// target against the request path server-side, turning it back
			// into the absolute "/" this is avoiding.
			w.Header().Set("Location", "./")
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveJS(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b) //nolint:errcheck
}

// browseOriginInjectJS returns a JS snippet that sets window.__SKYWIRE_BROWSE_ORIGIN__
// so browse.js loads mesh sites from the native real-origin reverse-proxy
// (meshproxy) instead of the sandboxed-srcdoc transcoder — but ONLY when
// BrowseOrigin (the mesh_proxy module) is enabled. The origin is
// <pk>[.<net>]<suffix>:<port> on the meshproxy's loopback listener; the native
// visor reverse-proxies it over dmsg/skynet server-side (no SW/bridge needed).
// Empty string when disabled → the transcoder fallback stays in effect.
func browseOriginInjectJS(v *Visor) string {
	bo := v.conf.BrowseOrigin
	if bo == nil || !bo.Enable {
		return ""
	}
	addr := bo.Addr
	if addr == "" {
		addr = defaultMeshProxyAddr
	}
	port := ""
	if _, p, err := net.SplitHostPort(addr); err == nil {
		port = p
	}
	scheme := "http"
	if bo.TLSCert != "" && bo.TLSKey != "" {
		scheme = "https"
	}
	suffix := normalizeMeshSuffix(bo.Suffix) // guaranteed leading dot
	return `window.__SKYWIRE_BROWSE_ORIGIN__={suffix:` + strconv.Quote(suffix) +
		`,scheme:` + strconv.Quote(scheme) + `,port:` + strconv.Quote(port) + `};`
}

// serveInjectedIndex serves index.html with the browse engine + launcher scripts
// (and window.__SKYWIRE_LOCAL_PK__) injected before </body>. Falls back to the
// plain file server if index.html can't be read.
func (hv *Hypervisor) serveInjectedIndex(w http.ResponseWriter, r *http.Request, fallback http.Handler) {
	if hv.c.UIAssets == nil || hv.visor == nil {
		fallback.ServeHTTP(w, r)
		return
	}
	f, err := hv.c.UIAssets.Open("index.html")
	if err != nil {
		fallback.ServeHTTP(w, r)
		return
	}
	defer f.Close() //nolint:errcheck
	b, err := io.ReadAll(f)
	if err != nil || !bytes.Contains(b, []byte("</body>")) {
		fallback.ServeHTTP(w, r)
		return
	}
	// Stamp the served build's fingerprint + a poller that reloads the tab when
	// a newer build is served (a visor binary with a new embedded UI or desk
	// client, or a rebuilt skywire.wasm), so an open dashboard never sits on a
	// stale build. Mirrors the wasm visor's autoupdate.js.
	ver := hv.servedUIVersion()
	inject := []byte(`<script>window.__SKYWIRE_LOCAL_PK__=` + strconv.Quote(hv.visor.conf.PK.Hex()) +
		`;window.__SKYWIRE_UI_VERSION__=` + strconv.Quote(ver) + `;` + browseOriginInjectJS(hv.visor) + `</script>` +
		`<script src="browse.js"></script>` +
		`<script>` + uiAutoReloadJS + `</script>`)
	out := bytes.Replace(b, []byte("</body>"), append(inject, []byte("</body>")...), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(out) //nolint:errcheck
}

// serveNativeDesk serves THE desk at the root of the native hypervisor UI
// port — the same page skeleton and the same desk module `skywire cli hv
// serve` renders (deskShellTemplate in wasmserve.go). There is one desk; what
// differs here is what it is a shell over. desk-boot probes this origin's /ws,
// finds the hypervisor, and bridges the desk's virtual-loopback ports to the
// host visor, so the panels, the nested browser and the terminal all reach the
// native visor and never a visor of the tab's own: autostartVisor is off, the
// help terminal and the docs server (both need the skywire command module,
// which this port does not serve) are off, the dashboard tab is this origin's
// own Angular UI, and the terminal is the host's pty page. The native visor IS
// the visor; the desk is a shell over it.
func (hv *Hypervisor) serveNativeDesk(w http.ResponseWriter) {
	localPK, browseJS := "", ""
	if hv.visor != nil {
		localPK = hv.visor.conf.PK.Hex()
		browseJS = browseOriginInjectJS(hv.visor)
	}
	// Mirror serveInjectedIndex's page environment (local PK, browse-origin
	// mode, served-bundle fingerprint + auto-reloader) so the desk and the
	// dashboard beneath it see the same page and update together. The local
	// PK doubles as the desk module's "this page is served by a visor" signal:
	// its DirectLoader renders same-origin pages natively only when it is set.
	ver := hv.servedUIVersion()
	scripts := `<script>window.__SKYWIRE_LOCAL_PK__=` + strconv.Quote(localPK) +
		`;window.__SKYWIRE_UI_VERSION__=` + strconv.Quote(ver) + `;` + browseJS + `</script>` + "\n" +
		`<script src="/wasm_exec.js"></script>` + "\n" +
		`<script src="/browse.js"></script>` + "\n" +
		`<script>` + uiAutoReloadJS + `</script>` + "\n" +
		`<script src="/desk-boot.js"></script>`
	page := deskShellHTML(scripts, nativeDeskBootOpts(localPK, hv.execWasmPath() != ""))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page) //nolint:errcheck
}

// nativeDeskBootOpts is the skywireDeskBoot options object for the desk the
// native hypervisor serves. The desk module and winbox come off this port; the
// skywire command module does not, so nothing that would exec it is enabled:
// no visor autostart, no help terminal, no docs server. The dashboard tab is
// this origin's Angular UI — RELATIVE, since through a vnet service worker the
// page can sit under a /vnet/<port>/ prefix the server never sees, and an
// absolute URL would escape onto the outer server (the trap #4499 fixed).
// embed=1 rides in the HASH (the Angular UI is hash-routed) so the framed
// dashboard hides its own taskbar. terminalURL is the host's pty page,
// /pty/<pk>, xterm over a websocket; absent when no visor is attached.
func nativeDeskBootOpts(localPK string, execWasm bool) string {
	opts := "{\n" +
		"  persistDB: 'skywire-desk',\n" +
		"  deskWasmURL: '/wasm-visor.wasm',\n" +
		"  wasmExecURL: '/wasm_exec.js',\n" +
		"  winboxURL: '/winbox.wasm',\n"
	if execWasm && localPK != "" {
		// The command module is served here, so the desk runs a visor of the
		// tab's own — ATTACHED to this hypervisor: its one transport is a
		// WebSocket back to this origin (see /tp/ws), it does not go looking
		// for public peers, and the host reaches it over that socket.
		opts += "  autostartVisor: true,\n" +
			"  wasmURL: '/skywire.wasm',\n" +
			"  execWorkerURL: '/skywire-worker.js',\n" +
			"  attach: { pk: '" + localPK + "', path: '/tp/ws' },\n"
	} else {
		opts += "  autostartVisor: false,\n"
	}
	opts +=
		"  helpTerminal: false,\n" +
			"  docsPort: 0,\n" +
			"  hvWindow: true,\n" +
			"  dashboardURL: './?embed=1#/?embed=1',\n"
	if localPK != "" {
		opts += "  terminalURL: './pty/" + localPK + "',\n"
	}
	return opts + "}"
}

// uiVersionHash fingerprints the served UI bundle (short sha256 of index.html,
// which embeds the content-hashed chunk names).
func uiVersionHash(indexHTML []byte) string {
	sum := sha256.Sum256(indexHTML)
	return hex.EncodeToString(sum[:])[:16]
}

// servedUIVersion is the fingerprint the pages on this port boot with and poll
// at /api/ui-version: the Angular bundle (index.html names its content-hashed
// chunks), the desk client this binary embeds, and — when one is served — the
// skywire command module on disk, stat'd per call. That last part is what a
// rebuilt skywire.wasm changes: the desk's own visor is that module, and it
// only ever runs a new one after a reload. Before it was folded in, a desk sat
// on a blob that had been rebuilt several times.
func (hv *Hypervisor) servedUIVersion() string {
	var ui string
	if hv.c.UIAssets != nil {
		if f, err := hv.c.UIAssets.Open("index.html"); err == nil {
			if b, rerr := io.ReadAll(f); rerr == nil {
				ui = uiVersionHash(b)
			}
			_ = f.Close() //nolint:errcheck
		}
	}
	return servedVersion(ui+"."+deskAssetsStamp(), hv.execWasmPath())
}

// getUIVersion → GET /api/ui-version : the current served-build fingerprint
// (no-store), polled by the injected auto-reloader.
func (hv *Hypervisor) getUIVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(hv.servedUIVersion())) //nolint:errcheck
	}
}

// uiAutoReloadJS polls the served-bundle fingerprint and reloads once it changes
// (a new visor binary shipped a new embedded UI). Silent, with a short grace so
// an in-flight click isn't cut off.
const uiAutoReloadJS = `(function(){
  var booted = window.__SKYWIRE_UI_VERSION__;
  if (!booted) { return; }
  setInterval(function(){
    fetch('/api/ui-version', {cache:'no-store'}).then(function(r){ return r.text(); }).then(function(v){
      if (v && v !== booted) {
        try { console.log('skywire: new hypervisor UI available — reloading'); } catch(e){}
        setTimeout(function(){ location.reload(); }, 1500);
      }
    }).catch(function(){});
  }, 30000);
})();`

// execWasmPath is the path of the full skywire command module for js/wasm, when
// the operator configured one for the wasm-serve port; the desk on this port
// shares it. Empty when there is none.
func (hv *Hypervisor) execWasmPath() string {
	if hv.c.WasmServe == nil {
		return ""
	}
	return hv.c.WasmServe.ExecWasm
}
