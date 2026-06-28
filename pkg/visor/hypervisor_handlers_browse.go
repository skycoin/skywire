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
    var p = self.SkywireBrowse.mountPanel(document, { fetchDmsg: fetchDmsg, fetchClearnet: fetchClearnet, selfPK: function () { return localPK; } });
    var btn = document.createElement("button");
    btn.textContent = "skynet"; btn.title = "browse skynet/dmsg sites + clearnet (via proxy)";
    btn.style.cssText = "position:fixed;left:12px;top:12px;z-index:2147483647;cursor:pointer;background:#9d7cff;color:#0e0c14;border:0;border-radius:6px;padding:.5em .8em;font:bold 12px monospace;box-shadow:0 4px 14px rgba(0,0,0,.4)";
    btn.onclick = function () { p.toggle(); };
    document.body.appendChild(btn);
  }
  ready();
})();`
