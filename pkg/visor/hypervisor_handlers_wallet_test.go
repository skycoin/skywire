//go:build !mobile

package visor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	wasmtinygo "github.com/skycoin/skycoin/src/skycoin-lite/wasm-tinygo"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/wallet/coins"
)

// walletReq builds a request whose {rest} path value is rest, as the
// /wallet/{rest...} route delivers it to walletHandler.
func walletReq(rest string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/wallet/"+rest, nil)
	req.SetPathValue("rest", rest)
	return req
}

// TestWalletHandlerRouting covers the multicoin routing decisions added to the
// native wallet handler (the /coin/<index> server-proxy model). The paths that
// need a live visor/gateway (coin 0 node proxy, coin 1 electrum) aren't
// exercised here — only the classification is.
func TestWalletHandlerRouting(t *testing.T) {
	hv := &Hypervisor{}
	h := hv.walletHandler()

	t.Run("coins returns the registry", func(t *testing.T) {
		w := httptest.NewRecorder()
		h(w, walletReq("coins"))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
			t.Errorf("Content-Type=%q, want json", ct)
		}
		var got []coins.Coin
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("body not the coin registry JSON: %v", err)
		}
		if len(got) != len(coins.Registry) || got[1].CoinSymbol != "BTC" {
			t.Fatalf("unexpected registry: %s", w.Body.String())
		}
	})

	t.Run("unknown coin index → 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		h(w, walletReq("coin/9/api/v1/health"))
		if w.Code != http.StatusNotFound {
			t.Errorf("status=%d, want 404 for unknown coin", w.Code)
		}
	})

	t.Run("non-numeric coin index → 400", func(t *testing.T) {
		w := httptest.NewRecorder()
		h(w, walletReq("coin/x/api/v1/health"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("status=%d, want 400 for invalid index", w.Code)
		}
	})

	t.Run("config page served", func(t *testing.T) {
		w := httptest.NewRecorder()
		h(w, walletReq("config"))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<") {
			t.Errorf("config page not served: status=%d", w.Code)
		}
	})

	t.Run("the old wallet page sends a browser to the dashboard's", func(t *testing.T) {
		for _, rest := range []string{"", "index.html"} {
			w := httptest.NewRecorder()
			h(w, walletReq(rest))
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "../#/wallet" {
				t.Errorf("%q: status=%d location=%q, want 303 to ../#/wallet", rest, w.Code, w.Header().Get("Location"))
			}
		}
	})

	t.Run("bundle assets are gone", func(t *testing.T) {
		w := httptest.NewRecorder()
		h(w, walletReq("assets/scripts/skycoin-lite.wasm"))
		if w.Code != http.StatusNotFound {
			t.Errorf("status=%d, want 404", w.Code)
		}
	})
}

// TestDashboardWalletCipher checks the cipher the dashboard's wallet route
// loads: Go's wasm loader, and the TinyGo skycoin-lite sent as embedded
// (gzipped) to a browser that accepts that, inflated otherwise.
func TestDashboardWalletCipher(t *testing.T) {
	h := (&Hypervisor{}).walletCipherHandler()

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/assets/scripts/wasm_exec.js", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "javascript") || w.Body.Len() == 0 {
		t.Fatalf("wasm_exec.js: status=%d ct=%q len=%d", w.Code, w.Header().Get("Content-Type"), w.Body.Len())
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/scripts/skycoin-lite.wasm", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	w = httptest.NewRecorder()
	h(w, req)
	if w.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(w.Body.Bytes(), wasmtinygo.WasmFileGz) {
		t.Fatalf("gzip request: encoding=%q, body is not the embedded gzip", w.Header().Get("Content-Encoding"))
	}

	w = httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/assets/scripts/skycoin-lite.wasm", nil))
	if w.Header().Get("Content-Encoding") != "" || !bytes.HasPrefix(w.Body.Bytes(), []byte("\x00asm")) {
		t.Fatalf("plain request: encoding=%q, body starts %q, want a raw wasm module", w.Header().Get("Content-Encoding"), w.Body.Bytes()[:min(4, w.Body.Len())])
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/wasm" {
		t.Errorf("content type %q, want application/wasm (instantiateStreaming requires it)", ct)
	}
}

// TestWalletRequiresSession checks that the wallet proxy needs a login when
// auth is on, while the static cipher stays public.
func TestWalletRequiresSession(t *testing.T) {
	config := visorconfig.MakeConfig(false)
	config.EnableAuth = true
	config.FillDefaults(false)
	config.DBPath = filepath.Join(t.TempDir(), "users_wallet.db")

	addr, client, closeFn := makeStartNode(t, config)
	defer closeFn()

	for path, want := range map[string]int{
		"/wallet/coins":                     http.StatusUnauthorized,
		"/wallet/coin/0/api/v1/health":      http.StatusUnauthorized,
		"/assets/scripts/wasm_exec.js":      http.StatusOK,
		"/assets/scripts/skycoin-lite.wasm": http.StatusOK,
	} {
		resp, err := client.Get("https://" + addr + path)
		require.NoError(t, err)
		_ = resp.Body.Close() //nolint:errcheck
		require.Equal(t, want, resp.StatusCode, path)
	}
}
