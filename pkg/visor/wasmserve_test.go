// Package visor pkg/visor/wasmserve_test.go c3-vis-core
package visor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin"
)

func TestIsMeshBrowseHost(t *testing.T) {
	const suffix = ".mesh.localhost"

	tests := []struct {
		name, host string
		want       bool
	}{
		{"matching host", "site.mesh.localhost", true},
		{"matching host with port", "site.mesh.localhost:8461", true},
		{"the undotted suffix does not match", "mesh.localhost", false},
		{"dotted bare suffix matches", ".mesh.localhost", true},
		{"different domain", "site.example.net", false},
		{"suffix appearing mid-host does not match", "mesh.localhost.evil.net", false},
		{"empty host", "", false},
		{"port only", ":8461", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMeshBrowseHost(tt.host, suffix); got != tt.want {
				t.Errorf("isMeshBrowseHost(%q, %q) = %v, want %v", tt.host, suffix, got, tt.want)
			}
		})
	}
}

func TestRandomHex(t *testing.T) {
	a, err := randomHex(32)
	if err != nil {
		t.Fatalf("randomHex: %v", err)
	}
	if len(a) != 64 {
		t.Errorf("randomHex(32) produced %d hex chars, want 64", len(a))
	}
	b, err := randomHex(32)
	if err != nil {
		t.Fatalf("randomHex: %v", err)
	}
	if a == b {
		t.Error("two randomHex calls returned the same value")
	}
}

// wasmPasswordGate is the only thing standing between a non-loopback listener
// and an unauthenticated hypervisor, so its states are worth pinning down.
func TestWasmPasswordGate(t *testing.T) {
	const password = "hunter2"
	protected := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("PROTECTED")); err != nil {
			t.Errorf("write protected body: %v", err)
		}
	})

	t.Run("unauthenticated request gets the login page, not the content", func(t *testing.T) {
		gate := wasmPasswordGate(protected, password, false)
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if strings.Contains(rec.Body.String(), "PROTECTED") {
			t.Error("protected content leaked to an unauthenticated request")
		}
	})

	t.Run("wrong password is rejected and sets no cookie", func(t *testing.T) {
		gate := wasmPasswordGate(protected, password, false)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/__login", strings.NewReader("p=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		gate.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if len(rec.Result().Cookies()) != 0 { //nolint:bodyclose
			t.Error("a session cookie was issued for a wrong password")
		}
	})

	t.Run("correct password issues a session cookie that then grants access", func(t *testing.T) {
		gate := wasmPasswordGate(protected, password, false)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/__login", strings.NewReader("p="+password))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		gate.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
		}
		cookies := rec.Result().Cookies() //nolint:bodyclose
		if len(cookies) != 1 {
			t.Fatalf("got %d cookies, want 1", len(cookies))
		}
		c := cookies[0]
		if !c.HttpOnly {
			t.Error("session cookie is not HttpOnly")
		}
		if c.SameSite != http.SameSiteStrictMode {
			t.Error("session cookie is not SameSite=Strict")
		}

		// The issued cookie must actually open the gate.
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.AddCookie(c)
		gate.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusOK || rec2.Body.String() != "PROTECTED" {
			t.Errorf("authenticated request got %d %q, want 200 PROTECTED", rec2.Code, rec2.Body.String())
		}
	})

	t.Run("a cookie from one gate does not open another", func(t *testing.T) {
		first := wasmPasswordGate(protected, password, false)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/__login", strings.NewReader("p="+password))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		first.ServeHTTP(rec, req)
		cookies := rec.Result().Cookies() //nolint:bodyclose
		if len(cookies) != 1 {
			t.Fatalf("got %d cookies, want 1", len(cookies))
		}

		// Each gate mints its own session token at construction, so a token
		// from a previous process/listener must not be replayable.
		second := wasmPasswordGate(protected, password, false)
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.AddCookie(cookies[0])
		second.ServeHTTP(rec2, req2)

		if rec2.Code == http.StatusOK {
			t.Error("a session cookie minted by a different gate was accepted")
		}
	})

	t.Run("secure flag propagates to the cookie", func(t *testing.T) {
		gate := wasmPasswordGate(protected, password, true)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/__login", strings.NewReader("p="+password))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		gate.ServeHTTP(rec, req)

		cookies := rec.Result().Cookies() //nolint:bodyclose
		if len(cookies) != 1 {
			t.Fatalf("got %d cookies, want 1", len(cookies))
		}
		if !cookies[0].Secure {
			t.Error("secure=true did not set the Secure cookie attribute")
		}
	})
}

// TestVariantPathsServeEachToolchainPair pins the per-variant PATH surface.
// A query string cannot select bytes on a static host, so publishing this
// surface as plain files needs the variant in the URL path. The wasm blob and
// its wasm_exec.js loader are toolchain-specific and must never be mixed (see
// wasmbin/embed.go), so both live under the same /wasm/<variant>/ prefix.
func TestVariantPathsServeEachToolchainPair(t *testing.T) {
	avail := wasmbin.Available()
	if len(avail) == 0 {
		t.Skip("no wasm variants embedded in this build")
	}
	for _, v := range avail {
		blob, err := wasmbin.GetVariant(v)
		require.NoError(t, err, "variant %s", v)
		require.NotEmpty(t, blob, "variant %s blob", v)
		require.NotEmpty(t, wasmbin.WasmExecJSVariant(v), "variant %s loader", v)
	}

	// Distinct variants must not share bytes — that is the whole point of
	// addressing them separately.
	if len(avail) > 1 {
		a, err := wasmbin.GetVariant(avail[0])
		require.NoError(t, err)
		b, err := wasmbin.GetVariant(avail[1])
		require.NoError(t, err)
		require.NotEqual(t, len(a), len(b), "variants %s and %s should differ", avail[0], avail[1])
		require.NotEqual(t,
			string(wasmbin.WasmExecJSVariant(avail[0])),
			string(wasmbin.WasmExecJSVariant(avail[1])),
			"each toolchain ships its own wasm_exec.js loader")
	}
}
