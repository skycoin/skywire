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

// TestNativeDeskServing pins the root serving contract on the NATIVE
// hypervisor UI port in BOTH build states. With a skywire command module
// (the two-stage build embeds one; a source build has none unless one is on
// disk) the root is the converged desk, hosted out of that ONE module. Without
// one there is no desk host, so the root is the dashboard exactly as the
// legacy_ui option serves it. Either way the Angular dashboard stays at its
// framed root, the retired paths stay gone, and the legacy wasm-visor blob
// is not served: netscrape, the desk host and the tab's visor all live in
// the command module now.
func TestNativeDeskServing(t *testing.T) {
	_, haveModule := execModuleSource("")
	t.Logf("command module available in this build: %v", haveModule)
	hv, pk := deskTestHypervisor(t)
	h := hv.uiHandler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	t.Run("GET / serves the desk with a command module, the dashboard without", func(t *testing.T) {
		w := get("/")
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("Content-Type=%q, want text/html", ct)
		}
		body := w.Body.String()
		if !haveModule {
			// No module, no desk host: the root is the injected dashboard,
			// the same page legacy_ui serves (#4753). Nothing desk-shaped.
			if !strings.Contains(body, "ANGULAR") || !strings.Contains(body, "__SKYWIRE_LOCAL_PK__") {
				t.Error("without a command module the root must serve the injected dashboard")
			}
			if strings.Contains(body, "skywireDeskBoot(") || strings.Contains(body, "/skywire.wasm") {
				t.Error("a desk was served with no command module to host it")
			}
			return
		}
		// ONE desk: the page boots the desk host out of the one command
		// module, the dashboard tab on this origin's own UI, no help terminal
		// and no docs server. With a local PK the tab's visor attaches to this
		// hypervisor (TestNativeDeskAttachedVisor pins that wiring).
		for _, want := range []string{"wasmURL: '/skywire.wasm'", "autostartVisor: true", "helpTerminal: false", "docsPort: 0", "hvWindow: true"} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %s", want)
			}
		}
		if strings.Contains(body, "wasm-visor.wasm") {
			t.Error("page still names the retired wasm-visor.wasm blob")
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

	t.Run("the retired boot path is never exposed on the hypervisor port", func(t *testing.T) {
		// hv-boot.js and worker.js were the retired SharedWorker boot path;
		// nothing serves them any more, and nothing may bring them back.
		// skywire.wasm is served exactly when there is a command module.
		for _, p := range []string{"/hv-boot.js", "/worker.js"} {
			if w := get(p); w.Code != http.StatusNotFound {
				t.Errorf("GET %s → %d, want 404", p, w.Code)
			}
		}
		want := http.StatusNotFound
		if haveModule {
			want = http.StatusOK
		}
		if w := get("/skywire.wasm"); w.Code != want {
			t.Errorf("GET /skywire.wasm → %d, want %d (module available: %v)", w.Code, want, haveModule)
		}
	})

	t.Run("the legacy wasm-visor blob is gone; Go's loader stays", func(t *testing.T) {
		// Everything that blob carried — netscrape, the desk host, the tab's
		// visor — is a role of the one command module now.
		if w := get("/wasm-visor.wasm"); w.Code != http.StatusNotFound {
			t.Errorf("GET /wasm-visor.wasm → %d, want 404 (the blob was removed)", w.Code)
		}
		w := get("/wasm_exec.js")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /wasm_exec.js → %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "globalThis.Go") {
			t.Error("/wasm_exec.js is not Go's loader")
		}
	})
}

// TestDeskShellHTMLWasmMode guards the refactor of the wasm /desk page onto
// the shared skeleton: `hv serve`'s desk must still wire the wasm assets and
// the harness injection anchor exactly as before.
func TestDeskShellHTMLWasmMode(t *testing.T) {
	page := string(deskShellHTML(wasmDeskScripts(), deskWasmBootOpts(false, 0)))
	for _, want := range []string{
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
		// The retired SharedWorker boot path stays absent: the attached visor
		// runs in the desk's own terminal, not in a worker the page spawns.
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
			// module, not a blob of its own.
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
