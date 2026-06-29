// Package visor pkg/visor/hypervisor_handlers_browse.go — serve the shared
// browse.js virtual-browser engine in the NATIVE hypervisor UI (the same one the
// wasm-visor runs), backed by /api/browse/* instead of the wasm JS hooks.
package visor

import (
	"bytes"
	"io"
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
		case "/skywire-browse-launcher.js":
			serveJS(w, []byte(nativeBrowseLauncherJS))
			return
		case "/", "/index.html":
			hv.serveInjectedIndex(w, r, fileServer)
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
	inject := []byte(`<script>window.__SKYWIRE_LOCAL_PK__=` + strconv.Quote(hv.visor.conf.PK.Hex()) +
		`</script><script src="browse.js"></script><script src="skywire-browse-launcher.js"></script>`)
	out := bytes.Replace(b, []byte("</body>"), append(inject, []byte("</body>")...), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(out) //nolint:errcheck
}

// nativeBrowseLauncherJS is the native HV-UI launcher: it gives browse.js the
// fetchDmsg / fetchClearnet / selfPK providers backed by /api/browse/* (the wasm
// page uses skywireVisor.* instead), mounts the panel, and adds the floating
// "skynet" button. Authenticated via the dashboard session cookie.
const nativeBrowseLauncherJS = `(function () {
  function ready() {
    if (!self.SkywireBrowse || !self.SkywireBrowse.mountPanel || !document.body) { return setTimeout(ready, 200); }
    var localPK = window.__SKYWIRE_LOCAL_PK__ || "";
    function b64e(s) { try { return btoa(unescape(encodeURIComponent(s))); } catch (e) { return btoa(s); } }
    function adapt(j) {
      var bytes = j && j.body ? Uint8Array.from(atob(j.body), function (c) { return c.charCodeAt(0); }) : new Uint8Array(0);
      return { status: (j && j.status_code) || 0, body: bytes, headers: (j && j.header) || {} };
    }
    function apiPost(path, obj) {
      return fetch(path, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(obj) })
        .then(function (r) { return r.json().catch(function () { return {}; }).then(function (j) { if (!r.ok) { throw new Error((j && j.error) || ("HTTP " + r.status)); } return j; }); });
    }
    function fetchDmsg(host, method, path, body) {
      // host is the resolving-proxy form (pk, pk:port, pk.dmsg, home.dmsg, an
      // alias.dmsg, …); the visor resolves it the same way its SOCKS5 proxy does.
      return apiPost("/api/browse/fetch", { host: String(host || ""), method: method || "GET", path: path || "/", body: body != null ? b64e(String(body)) : null, scheme: "auto" }).then(adapt);
    }
    function fetchClearnet(exitPK, method, url, body) {
      return apiPost("/api/browse/clearnet", { exit_pk: exitPK, method: method || "GET", url: url, body: body != null ? b64e(String(body)) : null }).then(adapt);
    }
    // directViaBackend: when the browse upstream-proxy is set to this visor's own
    // PK, the native visor egresses clearnet directly (http.Get) and we inline the
    // result — so it isn't blocked by the target site's X-Frame-Options the way a
    // browser-direct iframe load is. (A pure wasm tab leaves this unset and uses a
    // direct iframe load, since it can't read cross-origin server-side.)
    // api drives the visor cli REPL window: arbitrary /api calls (cookie-authed)
    // so the operator can interrogate the running visor from the UI without a shell.
    function api(method, path, body) {
      var p2 = path.charAt(0) === "/" ? path : "/" + path;
      return fetch(p2, { method: method, credentials: "same-origin", headers: body != null ? { "Content-Type": "application/json" } : {}, body: body != null ? body : undefined })
        .then(function (r) { return r.text().then(function (t) { return { status: r.status, body: t }; }); });
    }
    // ptyURL points the terminal window at this visor's dmsgpty endpoint
    // (/pty/<pk>, the same one the dashboard's Terminal tab iframes). The wasm
    // visor leaves this unset — it has no host shell — so the term button is
    // native-only.
    var ptyURL = localPK ? ("/pty/" + localPK) : "";
    var p = self.SkywireBrowse.mountPanel(document, { fetchDmsg: fetchDmsg, fetchClearnet: fetchClearnet, selfPK: function () { return localPK; }, directViaBackend: true, api: api, ptyURL: ptyURL });
    // Stream the NATIVE visor's server-side log (/api/log SSE) into the log
    // window's buffer. The wasm visor logs to the browser console (captured
    // directly); the native visor's log lives server-side, so without this the
    // native log window would show only browser-side entries.
    try {
      var es = new EventSource("/api/log");
      es.onmessage = function (ev) {
        try { var j = JSON.parse(ev.data); if (self.skywireLog) { self.skywireLog.emit(j.level || "log", [j.msg]); } } catch (e) {}
      };
    } catch (e) {}
    var btn = document.createElement("button");
    btn.textContent = "panel"; btn.title = "open the skywire panel — browser, console, terminal, logs (☰ menu)";
    btn.style.cssText = "position:fixed;left:12px;bottom:12px;z-index:2147483647;cursor:pointer;background:#9d7cff;color:#0e0c14;border:0;border-radius:6px;padding:.5em .8em;font:bold 12px monospace;box-shadow:0 4px 14px rgba(0,0,0,.4)";
    btn.onclick = function () { p.toggle(); };
    document.body.appendChild(btn);
  }
  ready();
})();`
