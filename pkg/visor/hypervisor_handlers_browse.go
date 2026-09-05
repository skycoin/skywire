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
		case "/skywire-browse-launcher.js":
			serveJS(w, []byte(nativeBrowseLauncherJS))
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
	// Stamp the served bundle's fingerprint + a poller that reloads the tab when
	// a newer bundle is served (after the visor binary updates its embedded UI),
	// so an open dashboard never sits on a stale build. Mirrors the wasm
	// visor's autoupdate.js. The fingerprint is a hash of index.html, which
	// references the content-hashed chunk filenames — it changes iff the UI does.
	ver := uiVersionHash(b)
	inject := []byte(`<script>window.__SKYWIRE_LOCAL_PK__=` + strconv.Quote(hv.visor.conf.PK.Hex()) +
		`;window.__SKYWIRE_UI_VERSION__=` + strconv.Quote(ver) + `;` + browseOriginInjectJS(hv.visor) + `</script>` +
		`<script src="browse.js"></script><script src="skywire-browse-launcher.js"></script>` +
		`<script>` + uiAutoReloadJS + `</script>`)
	out := bytes.Replace(b, []byte("</body>"), append(inject, []byte("</body>")...), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(out) //nolint:errcheck
}

// serveNativeDesk serves the converged desk shell at /desk on the NATIVE
// hypervisor UI port — the same page skeleton `skywire cli hv serve` renders
// (deskShellTemplate in wasmserve.go), in native mode: the browse launcher
// (the exact script the dashboard injection loads) mounts the desk panel over
// its /api-backed providers, and desk-boot's native branch opens the Angular
// dashboard as a same-origin window inside it. NO wasm-visor module is
// referenced or served on this port — the native visor IS the visor; the desk
// here is a shell over it, so the in-page-visor machinery stays dormant.
func (hv *Hypervisor) serveNativeDesk(w http.ResponseWriter) {
	localPK, browseJS := "", ""
	if hv.visor != nil {
		localPK = hv.visor.conf.PK.Hex()
		browseJS = browseOriginInjectJS(hv.visor)
	}
	// Mirror serveInjectedIndex's page environment (local PK, browse-origin
	// mode, served-bundle fingerprint + auto-reloader) so the launcher and the
	// update behavior are identical on /desk and on the dashboard beneath it.
	var ver string
	if hv.c.UIAssets != nil {
		if f, err := hv.c.UIAssets.Open("index.html"); err == nil {
			if b, rerr := io.ReadAll(f); rerr == nil {
				ver = uiVersionHash(b)
			}
			_ = f.Close() //nolint:errcheck
		}
	}
	scripts := `<script>window.__SKYWIRE_LOCAL_PK__=` + strconv.Quote(localPK) +
		`;window.__SKYWIRE_UI_VERSION__=` + strconv.Quote(ver) + `;` + browseJS + `</script>` + "\n" +
		`<script src="/browse.js"></script>` + "\n" +
		`<script src="/skywire-browse-launcher.js"></script>` + "\n" +
		`<script>` + uiAutoReloadJS + `</script>` + "\n" +
		`<script src="/desk-boot.js"></script>`
	page := deskShellHTML(scripts, nativeDeskBootOpts)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page) //nolint:errcheck
}

// nativeDeskBootOpts is the skywireDeskBoot options object for the
// native-served /desk: native mode (no wasm anything), dashboard window on
// the same-origin Angular UI. embed=1 rides in the HASH (the Angular UI is
// hash-routed) so the iframe's own injected launcher hides its taskbar — the
// same chrome-less guard the ☰ chat/log windows rely on.
const nativeDeskBootOpts = `{
  native: true,
  dashboardURL: './?embed=1#/?embed=1',
}`

// uiVersionHash fingerprints the served UI bundle (short sha256 of index.html,
// which embeds the content-hashed chunk names).
func uiVersionHash(indexHTML []byte) string {
	sum := sha256.Sum256(indexHTML)
	return hex.EncodeToString(sum[:])[:16]
}

// getUIVersion → GET /api/ui-version : the current served-bundle fingerprint
// (no-store), polled by the injected auto-reloader.
func (hv *Hypervisor) getUIVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		var ver string
		if hv.c.UIAssets != nil {
			if f, err := hv.c.UIAssets.Open("index.html"); err == nil {
				if b, rerr := io.ReadAll(f); rerr == nil {
					ver = uiVersionHash(b)
				}
				_ = f.Close() //nolint:errcheck
			}
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(ver)) //nolint:errcheck
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

// nativeBrowseLauncherJS is the native HV-UI launcher: it mounts the
// engine-free desk panel (browseui/desk-panel.js, part of /browse.js) and
// publishes it as __skywireDesk — the handle desk-boot's native branch waits
// on before opening the dashboard window. It refuses to mount inside an
// embedded frame (embed=1 in the hash/query): the dashboard window is an
// iframe of the framed root and must not grow its own taskbar. The retired
// browse.js engine's providers are gone with it; the native desk's terminal
// remains the dashboard's Terminal tab (dmsgpty) until the shared shell
// integration lands.
const nativeBrowseLauncherJS = `(function () {
  if (/[#?&]embed=1/.test(location.hash + location.search)) { return; }
  function ready() {
    if (!self.skywireDeskPanel || !document.body || typeof self.WinBox !== "function") { return setTimeout(ready, 200); }
    // RELATIVE: reached through the vnet service worker this page lives under
    // a "/vnet/<port>/" prefix the server never sees, so an absolute URL
    // escapes it onto the outer server's root — a different visor entirely
    // (the same trap #4499 fixed for /desk). "./" is resolved by the browser
    // against the URL it actually asked for, which keeps the prefix.
    //
    // The root, not a /dashboard path: the hypervisor serves the dashboard at
    // the root whenever the request is framed, and the SW normalises deeper
    // paths away anyway. The launcher already returns early on an embed=1
    // page, so this only ever runs on the desk root.
    var dashURL = "./?embed=1#/?embed=1";
    var p = self.skywireDeskPanel.mount(document, { dashboardURL: dashURL });
    // Stream the NATIVE visor's server-side log (/api/log SSE) into the log
    // window's buffer, when a log consumer exists on the page.
    try {
      var es = new EventSource("/api/log");
      es.onmessage = function (ev) {
        try { var j = JSON.parse(ev.data); if (self.skywireLog) { self.skywireLog.emit(j.level || "log", [j.msg]); } } catch (e) {}
      };
    } catch (e) {}
    self.__skywireDesk = p;
  }
  ready();
})();`
