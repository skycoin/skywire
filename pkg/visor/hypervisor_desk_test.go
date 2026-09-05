//go:build !mobile

// Package visor pkg/visor/hypervisor_desk_test.go c3-vis-core
package visor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// deskTestHypervisor builds the minimal Hypervisor uiHandler needs: embedded
// UI assets plus a visor identity (serveNativeDesk injects the local PK the
// way serveInjectedIndex does).
func deskTestHypervisor(t *testing.T) (*Hypervisor, cipher.PubKey) {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	hv := &Hypervisor{
		c: visorconfig.HypervisorConfig{
			UIAssets: fstest.MapFS{
				"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body>ANGULAR</body></html>")},
			},
		},
		visor: &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}}},
	}
	return hv, pk
}

// TestNativeDeskServing pins the /desk serving contract on the NATIVE
// hypervisor UI port: the converged desk shell is served in native mode, the
// Angular dashboard stays untouched at its existing routes, and nothing
// wasm-visor-shaped is exposed (the native visor IS the visor — the desk is a
// shell over it, so the in-page-visor machinery must have nothing to fetch).
func TestNativeDeskServing(t *testing.T) {
	hv, pk := deskTestHypervisor(t)
	h := hv.uiHandler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	t.Run("GET / serves the desk shell in native mode (the desk IS the UI)", func(t *testing.T) {
		w := get("/")
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("Content-Type=%q, want text/html", ct)
		}
		body := w.Body.String()
		// The native-mode marker: desk-boot must take its native branch, which
		// boots no in-page visor and opens the same-origin dashboard window.
		if !strings.Contains(body, "native: true") {
			t.Error("page lacks the native-mode marker (native: true)")
		}
		// RELATIVE and rooted: an absolute URL escapes the /vnet/<port>/ prefix
		// onto the outer server's root (the trap #4499 fixed for /desk), and the
		// dashboard lives at the framed root now, not a /dashboard path.
		if !strings.Contains(body, "dashboardURL: './?embed=1#/?embed=1'") {
			t.Error("page lacks the relative framed-root dashboard window URL")
		}
		if strings.Contains(body, "dashboardURL: '/") {
			t.Error("dashboardURL is absolute — it would escape the vnet prefix")
		}
		// The shell is assembled from the SAME assets the dashboard injection
		// uses plus the shared desk boot.
		for _, want := range []string{`src="/browse.js"`, `src="/skywire-browse-launcher.js"`, `src="/desk-boot.js"`, "skywireDeskBoot("} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %s", want)
			}
		}
		if !strings.Contains(body, pk.Hex()) {
			t.Error("page lacks the injected local PK")
		}
		// The ONE-VISOR rule, as served bytes: the native desk page must not
		// even reference the wasm-visor module or its loader.
		for _, banned := range []string{"wasm-visor.wasm", "wasm_exec", "skywire.wasm"} {
			if strings.Contains(body, banned) {
				t.Errorf("native desk page references %s — the in-page-visor machinery must stay dormant", banned)
			}
		}
	})

	t.Run("the old /desk path redirects to the root", func(t *testing.T) {
		w := get("/desk")
		if w.Code != http.StatusMovedPermanently {
			t.Fatalf("status=%d, want 301", w.Code)
		}
		// RELATIVE on purpose: served under a /vnet/<port>/ prefix an absolute
		// "/" escapes to the outer server's root, a different visor. See the
		// handler.
		if loc := w.Header().Get("Location"); loc != "./" {
			t.Errorf("Location=%q, want ./ (relative, so the vnet prefix survives)", loc)
		}
	})

	t.Run("Angular serves at the FRAMED root, with the launcher injection intact", func(t *testing.T) {
		// The dashboard has no path of its own. Under the vnet service worker a
		// framed page gets its <base href> rewritten to the /vnet/<port>/ prefix,
		// so any deeper path is normalised back to the root before the document
		// finishes loading — which is why desk-boot.js stopped pointing at
		// /dashboard. The root serves the dashboard whenever the request is framed.
		for _, framed := range []func() *httptest.ResponseRecorder{
			func() *httptest.ResponseRecorder { return get("/?embed=1") },
			func() *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("Sec-Fetch-Dest", "iframe")
				h.ServeHTTP(w, r)
				return w
			},
		} {
			w := framed()
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d, want 200", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, "ANGULAR") {
				t.Error("Angular index not served at the framed root")
			}
			if !strings.Contains(body, `src="browse.js"`) || !strings.Contains(body, "__SKYWIRE_LOCAL_PK__") {
				t.Error("index injection (desk launcher) missing on the framed root")
			}
		}
	})

	t.Run("the retired /dashboard path is gone", func(t *testing.T) {
		for _, p := range []string{"/dashboard", "/dashboard/"} {
			if w := get(p); w.Code != http.StatusNotFound {
				t.Errorf("GET %s → %d, want 404 (the dashboard is the framed root now)", p, w.Code)
			}
		}
	})

	t.Run("desk-boot.js is served beside the page", func(t *testing.T) {
		w := get("/desk-boot.js")
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "skywireDeskBoot") {
			t.Error("/desk-boot.js does not carry the shared desk boot")
		}
	})

	t.Run("the wasm-visor module is not exposed on the hypervisor port", func(t *testing.T) {
		for _, p := range []string{"/wasm-visor.wasm", "/skywire.wasm", "/wasm_exec.js", "/hv-boot.js", "/worker.js"} {
			if w := get(p); w.Code != http.StatusNotFound {
				t.Errorf("GET %s → %d, want 404 (native mode must not serve the in-page visor)", p, w.Code)
			}
		}
	})
}

// TestDeskShellHTMLWasmMode guards the refactor of the wasm /desk page onto
// the shared skeleton: `hv serve`'s desk must still wire the wasm assets and
// the harness injection anchor exactly as before.
func TestDeskShellHTMLWasmMode(t *testing.T) {
	page := string(deskShellHTML(
		`<script src="/wasm_exec.js?variant=go"></script>`+"\n"+
			`<script src="/browse.js"></script>`+"\n"+
			`<script src="/desk-boot.js"></script>`,
		deskWasmBootOpts))
	for _, want := range []string{
		"deskWasmURL: '/wasm-visor.wasm'",
		"wasmURL: '/skywire.wasm'",
		"autostartVisor: true",
		"skywireDeskBoot(",
		// The exact tag ServeWasm's --harness injection keys on.
		`<script src="/desk-boot.js"></script>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("wasm desk page lacks %s", want)
		}
	}
	if strings.Contains(page, "__DESK_SCRIPTS__") || strings.Contains(page, "__DESK_OPTS__") {
		t.Error("template placeholders leaked into the rendered page")
	}
}
