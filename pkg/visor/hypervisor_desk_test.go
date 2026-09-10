//go:build !mobile

// Package visor pkg/visor/hypervisor_desk_test.go c3-vis-core
package visor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
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
	if execwasm.Present() {
		t.Skip("a command module is embedded in this build (two-stage build); this test pins the no-module behaviour")
	}
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
		// ONE desk: the page boots the same desk module the wasm page does, as a
		// shell over the host visor — no in-tab visor, no help terminal and no
		// docs server (those need the skywire command module this port does
		// not serve), the dashboard tab on this origin's own UI.
		for _, want := range []string{"deskWasmURL: '/wasm-visor.wasm'", "autostartVisor: false", "helpTerminal: false", "docsPort: 0", "hvWindow: true"} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %s", want)
			}
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
		for _, want := range []string{`src="/wasm_exec.js"`, `src="/browse.js"`, `src="/desk-boot.js"`, "skywireDeskBoot("} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %s", want)
			}
		}
		if !strings.Contains(body, pk.Hex()) {
			t.Error("page lacks the injected local PK")
		}
		// The terminal window: the host visor's pty page, relative like the
		// dashboard URL, keyed by the local PK. Parity with the wasm desk's
		// console window.
		if !strings.Contains(body, "terminalURL: './pty/"+pk.Hex()+"'") {
			t.Error("page lacks the relative pty terminal window URL")
		}
		// With no command module configured (hypervisor.wasm_serve.exec_wasm
		// unset) the page is a shell over the host visor only: no autostart and
		// no reference to the skywire command module. TestNativeDeskAttachedVisor
		// covers the configured case.
		for _, banned := range []string{"autostartVisor: true", "/skywire.wasm", "skywire.wasm.gz", "skywire-browse-launcher"} {
			if strings.Contains(body, banned) {
				t.Errorf("desk page references %s without a command module to serve", banned)
			}
		}
	})

	t.Run("the JS-panel launcher is gone; desk-boot carries the browse-API transport", func(t *testing.T) {
		if w := get("/skywire-browse-launcher.js"); w.Code != http.StatusNotFound {
			t.Errorf("launcher status=%d, want 404 — the engine-free panel was retired", w.Code)
		}
		w := get("/desk-boot.js")
		if w.Code != http.StatusOK {
			t.Fatalf("desk-boot status=%d, want 200", w.Code)
		}
		body := w.Body.String()
		// Over the host bridge netscrape's transport is the hypervisor's browse
		// API; without a hook it falls back to a same-origin /fetch proxy this
		// server does not have, and every foreign URL renders a 404 body.
		for _, want := range []string{"__netscrapeFetch", "/api/browse/fetch", "/api/browse/clearnet"} {
			if !strings.Contains(body, want) {
				t.Errorf("desk-boot lacks %s", want)
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

	t.Run("Angular serves at the FRAMED root, with the page injection intact", func(t *testing.T) {
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
				t.Error("index injection (browse.js + local PK) missing on the framed root")
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

	// The rule is that native mode must not START a visor in the page — not
	// that the module may never be served. Those were the same thing while the
	// only reason to ship the module was to boot a visor out of it; they came
	// apart when the desk began rendering the hypervisor UI as a netscrape tab,
	// because netscrape is Go/wasm and lives in that same module. So the module
	// is served, in a role that installs the browser and nothing else, while
	// everything that could actually start a visor stays absent.
	t.Run("the visor BOOT path is not exposed on the hypervisor port", func(t *testing.T) {
		// hv-boot.js and worker.js ARE the boot path (worker.js hosts a visor
		// off-thread; hv-boot.js spawns it) and are never served here.
		// skywire.wasm is the in-tab CLI the desk starts a visor with; it is
		// served only when the operator configured a command module (see
		// TestNativeDeskAttachedVisor) — with none, 404 like the rest.
		for _, p := range []string{"/skywire.wasm", "/hv-boot.js", "/worker.js"} {
			if w := get(p); w.Code != http.StatusNotFound {
				t.Errorf("GET %s → %d, want 404 (no command module configured)", p, w.Code)
			}
		}
	})

	t.Run("the desk-host module is served for the browser, not for a visor", func(t *testing.T) {
		for _, p := range []string{"/wasm-visor.wasm", "/wasm_exec.js"} {
			// (wasm-visor.wasm only until every desk has a command module: with
			// none, it is still the desk host.)
			if w := get(p); w.Code != http.StatusOK {
				t.Errorf("GET %s → %d, want 200 (netscrape lives in this module)", p, w.Code)
			}
		}
	})

	t.Run("the native desk page does not autostart a visor without a command module", func(t *testing.T) {
		w := get("/")
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "autostartVisor: false") {
			t.Error("native desk page lost its explicit autostartVisor: false")
		}
		// These are what the WASM desk passes to run a visor in the tab. Their
		// absence here is the guarantee, and it is what makes serving the desk
		// module safe: nothing tells the desk to boot one, and the command
		// module it would need is not even named.
		for _, never := range []string{"autostartVisor: true", "wasmURL: '/skywire.wasm'"} {
			if strings.Contains(body, never) {
				t.Errorf("native desk page carries %q — it must not start a visor", never)
			}
		}
	})
}

// TestDeskShellHTMLWasmMode guards the refactor of the wasm /desk page onto
// the shared skeleton: `hv serve`'s desk must still wire the wasm assets and
// the harness injection anchor exactly as before.
func TestDeskShellHTMLWasmMode(t *testing.T) {
	page := string(deskShellHTML(wasmDeskScripts(), deskWasmBootOpts(false, 0)))
	for _, want := range []string{
		"deskWasmURL: '/skywire.wasm'",
		"wasmURL: '/skywire.wasm'",
		"autostartVisor: true",
		"skywireDeskBoot(",
		// The exact tag ServeWasm's --harness injection keys on.
		`<script src="/desk-boot.js"></script>`,
		// The served-build stamp the poller compares against, and the poller
		// itself — without both, a desk tab never learns that the skywire.wasm
		// its visor runs has been rebuilt (one sat 22 h on a stale blob).
		`window.__SKYWIRE_WASM_VERSION__="` + servedVersionToken + `"`,
		`<script src="/autoupdate.js"></script>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("wasm desk page lacks %s", want)
		}
	}
	if strings.Contains(page, "__DESK_SCRIPTS__") || strings.Contains(page, "__DESK_OPTS__") {
		t.Error("template placeholders leaked into the rendered page")
	}
	// The poller must run before desk-boot, so it is armed even if boot fails.
	if strings.Index(page, "/autoupdate.js") > strings.Index(page, "/desk-boot.js") {
		t.Error("autoupdate.js must be included before desk-boot.js")
	}
	rendered := string(renderServedVersion([]byte(page), "abc-def"))
	if strings.Contains(rendered, servedVersionToken) || !strings.Contains(rendered, `window.__SKYWIRE_WASM_VERSION__="abc-def"`) {
		t.Error("served-version token not filled in the rendered page")
	}
}

// TestServedUIVersionTracksExecWasm pins that the native port's /api/ui-version
// — what the desk's poller compares against — changes when the skywire command
// module it serves changes (appears, or is rebuilt in place), and that the
// served desk page boots with exactly the value the endpoint answers, so a
// poll compares like with like.
func TestServedUIVersionTracksExecWasm(t *testing.T) {
	hv, _ := deskTestHypervisor(t)
	h := hv.uiHandler()
	get := func(path string) string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d", path, w.Code)
		}
		return w.Body.String()
	}
	// /api/ui-version is mounted on the API router, not uiHandler; ask its
	// handler directly.
	version := func() string {
		w := httptest.NewRecorder()
		hv.getUIVersion()(w, httptest.NewRequest(http.MethodGet, "/api/ui-version", nil))
		return w.Body.String()
	}
	v0 := version()
	if v0 == "" {
		t.Fatal("/api/ui-version is empty with UI assets present")
	}
	if !strings.Contains(get("/"), `window.__SKYWIRE_UI_VERSION__="`+v0+`"`) {
		t.Errorf("desk page does not boot with the polled version %q", v0)
	}

	// A command module appears (hypervisor.wasm_serve.exec_wasm).
	p := filepath.Join(t.TempDir(), "skywire.wasm")
	if err := os.WriteFile(p, []byte("build one"), 0o600); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 9, 8, 9, 38, 0, 0, time.UTC)
	if err := os.Chtimes(p, t0, t0); err != nil {
		t.Fatal(err)
	}
	hv.c.WasmServe = &visorconfig.WasmServeConf{ExecWasm: p}
	v1 := version()
	if v1 == v0 {
		t.Fatalf("version %q did not change when a command module appeared", v1)
	}
	if !strings.Contains(get("/"), `window.__SKYWIRE_UI_VERSION__="`+v1+`"`) {
		t.Errorf("desk page does not boot with the polled version %q", v1)
	}

	// The module is rebuilt in place while the server runs.
	t1 := t0.Add(5 * time.Hour)
	if err := os.Chtimes(p, t1, t1); err != nil {
		t.Fatal(err)
	}
	if v2 := version(); v2 == v1 {
		t.Fatalf("version %q did not change when the command module was rebuilt", v2)
	}
}

// TestNativeDeskAttachedVisor pins the ATTACHED desk: when the operator has a
// skywire command module for js/wasm (hypervisor.wasm_serve.exec_wasm), the
// hypervisor UI port serves it and its worker, and the page boots a visor of
// the tab's own whose one transport is a WebSocket back to this origin — the
// same-origin transport at /tp/ws, addressed by the host's public key, no
// address-resolver lookup — with public autoconnect off.
func TestNativeDeskAttachedVisor(t *testing.T) {
	hv, pk := deskTestHypervisor(t)
	exec := filepath.Join(t.TempDir(), "skywire.wasm")
	if err := os.WriteFile(exec, []byte("\x00asm-test"), 0o600); err != nil {
		t.Fatal(err)
	}
	hv.c.WasmServe = &visorconfig.WasmServeConf{ExecWasm: exec}
	h := hv.uiHandler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	t.Run("the command module and its worker are served", func(t *testing.T) {
		w := get("/skywire.wasm")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /skywire.wasm → %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/wasm" {
			t.Errorf("Content-Type=%q, want application/wasm", ct)
		}
		if w.Body.String() != "\x00asm-test" {
			t.Errorf("served body is not the configured module")
		}
		if w := get("/skywire-worker.js"); w.Code != http.StatusOK {
			t.Errorf("GET /skywire-worker.js → %d, want 200", w.Code)
		}
		// The off-thread boot path stays absent: the attached visor runs in
		// the desk's own terminal, not in a worker the page spawns.
		for _, p := range []string{"/hv-boot.js", "/worker.js"} {
			if w := get(p); w.Code != http.StatusNotFound {
				t.Errorf("GET %s → %d, want 404", p, w.Code)
			}
		}
	})

	t.Run("the page boots a visor attached to this origin", func(t *testing.T) {
		w := get("/")
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200", w.Code)
		}
		body := w.Body.String()
		for _, want := range []string{
			"autostartVisor: true",
			// ONE module: the desk host is `skywire desk-host` out of the command
			// module, not a wasm-visor.wasm of its own.
			"deskWasmURL: '/skywire.wasm'",
			"wasmURL: '/skywire.wasm'",
			"execWorkerURL: '/skywire-worker.js'",
			"attach: { pk: '" + pk.Hex() + "', path: '/tp/ws' }",
			// Still the ONE desk: dashboard and pty are the host's.
			"dashboardURL: './?embed=1#/?embed=1'",
			"terminalURL: './pty/" + pk.Hex() + "'",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("attached desk page lacks %s", want)
			}
		}
		if strings.Contains(body, "autostartVisor: false") {
			t.Error("attached desk page still carries autostartVisor: false")
		}
	})
}

// TestTransportWSEndpoint pins /tp/ws on the hypervisor port: it is the WS
// transport client's own acceptor, so with no visor (or no transport manager)
// behind the hypervisor it refuses rather than pretending.
func TestTransportWSEndpoint(t *testing.T) {
	hv, _ := deskTestHypervisor(t)
	w := httptest.NewRecorder()
	hv.getTransportWS()(w, httptest.NewRequest(http.MethodGet, "/tp/ws", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("no transport manager: status=%d, want 503", w.Code)
	}
	hv.visor = nil
	w = httptest.NewRecorder()
	hv.getTransportWS()(w, httptest.NewRequest(http.MethodGet, "/tp/ws", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("no visor: status=%d, want 503", w.Code)
	}
}
