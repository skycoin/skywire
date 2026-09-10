//go:build !mobile

// Package visor pkg/visor/hypervisor_handlers_browse.go c3-vis-core
// browse.js virtual-browser engine in the NATIVE hypervisor UI (the same one the
// wasm-visor runs), backed by /api/browse/* instead of the wasm JS hooks.
package visor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/browseui"
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		// PWA: the desk installs as an app. The manifest and icons are the ones
		// `hv serve` uses; the service worker precaches THIS context's shell and
		// is named by the served-build stamp, so a new build re-installs it.
		case "/manifest.webmanifest":
			w.Header().Set("Content-Type", "application/manifest+json")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(wasmhv.PWAManifest) //nolint:errcheck
			return
		case "/icon-192.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(wasmhv.PWAIcon192) //nolint:errcheck
			return
		case "/icon-512.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(wasmhv.PWAIcon512) //nolint:errcheck
			return
		case "/sw.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(wasmhv.ServiceWorkerFor(hv.servedUIVersion(), []string{"/", "/manifest.webmanifest", "/icon-192.png", "/icon-512.png", "/browse.js", "/desk-boot.js", "/wasm_exec.js", "/skywire-worker.js", "/autoupdate.js"})) //nolint:errcheck
			return
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
		case "/wasm_exec.js":
			// Go's loader for the one command module below (a std-Go build,
			// so std-Go's wasm_exec.js; the two are a pair).
			serveJS(w, wasmhv.WasmExecJS)
			return
		case "/skywire.wasm":
			// The ONE skywire command module — the desk host, the visor the
			// served desk runs in its terminal, every command — embedded by
			// the two-stage build, from the package location on disk, or the
			// operator's hypervisor.wasm_serve.exec_wasm. Absent all three
			// there is no desk: the root serves the dashboard (see "/").
			if p, ok := hv.execModule(); ok {
				serveExecWasm(w, r, p)
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
			//
			// Two things put the dashboard at the root instead: the opt-in
			// legacy mode (hypervisor.legacy_ui / LEGACYHVUI), and a build
			// with no skywire command module to host the desk out of — a
			// plain source build without `make build-embedded` and nothing
			// on disk. The desk is `skywire desk-host` out of that module;
			// there is no other host for it, and the dashboard is what the
			// operator can still use. Logged once at startup (logUIRoot).
			if _, haveExec := hv.execModule(); hv.LegacyUI() || !haveExec {
				hv.serveInjectedIndex(w, r, fileServer)
				return
			}
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
	page := deskShellHTML(scripts, nativeDeskBootOpts(localPK))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page) //nolint:errcheck
}

// nativeDeskBootOpts is the skywireDeskBoot options object for the desk the
// native hypervisor serves. The desk host is `skywire desk-host` out of the
// ONE command module (embedded by the two-stage build, on disk, or
// hypervisor.wasm_serve.exec_wasm); serveNativeDesk is only reached when
// there is one. No help terminal and no docs server — each is a whole extra
// Go/wasm runtime the tab never gets back. The dashboard tab is
// this origin's Angular UI — RELATIVE, since through a vnet service worker the
// page can sit under a /vnet/<port>/ prefix the server never sees, and an
// absolute URL would escape onto the outer server (the trap #4499 fixed).
// embed=1 rides in the HASH (the Angular UI is hash-routed) so the framed
// dashboard hides its own taskbar. terminalURL is the host's pty page,
// /pty/<pk>, xterm over a websocket; absent when no visor is attached.
func nativeDeskBootOpts(localPK string) string {
	opts := "{\n" +
		"  persistDB: 'skywire-desk',\n" +
		"  wasmExecURL: '/wasm_exec.js',\n" +
		"  wasmURL: '/skywire.wasm',\n"
	if localPK != "" {
		// The command module is served here, so the desk runs a visor of the
		// tab's own — ATTACHED to this hypervisor: its one transport is a
		// WebSocket back to this origin (see /tp/ws), it does not go looking
		// for public peers, and the host reaches it over that socket.
		opts += "  autostartVisor: true,\n" +
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

// logUIRoot says, once at mount, why the web UI root is the dashboard rather
// than the desk when that is not the operator's own choice: the build has no
// skywire command module to host the desk out of. Nothing is logged for the
// desk (the default) or for legacy_ui (the operator asked for it).
func (hv *Hypervisor) logUIRoot() {
	if hv.logger == nil || hv.LegacyUI() {
		return
	}
	if _, ok := hv.execModule(); ok {
		return
	}
	hv.logger.Info("no skywire command module in this build (make build-embedded; or " +
		"hypervisor.wasm_serve.exec_wasm; or " + skyenv.ExecWasmFile + " under the package bin dir): " +
		"the web UI root serves the dashboard, not the desk")
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
	execPath, _ := hv.execModule()
	return servedVersion(ui+"."+deskAssetsStamp(), execPath)
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
func (hv *Hypervisor) execModule() (path string, ok bool) {
	explicit := ""
	if hv.c.WasmServe != nil {
		explicit = hv.c.WasmServe.ExecWasm
	}
	return execModuleSource(explicit)
}
