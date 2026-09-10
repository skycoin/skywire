//go:build !mobile

package visor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/skycoin/skywire/pkg/wallet/coins"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// walletReq builds a request whose chi "*" URL param is set to rest, as the
// /wallet/* route delivers it to walletHandler.
func walletReq(rest string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/wallet/"+rest, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("*", rest)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
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
}

// TestWalletCipherRoutes checks the native hypervisor's /wallet/* serves the
// cipher assets the vendored bundle instantiates — the dist carries neither, so
// before this the native (and the desk's tab) wallet got "Go is not defined".
func TestWalletCipherRoutes(t *testing.T) {
	hv := &Hypervisor{}
	h := hv.walletHandler()

	w := httptest.NewRecorder()
	h(w, walletReq("assets/scripts/wasm_exec.js"))
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("wasm_exec.js: status=%d ct=%q", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "'--role','cipher'") {
		t.Error("wasm_exec.js is not pinned to the cipher role")
	}

	w = httptest.NewRecorder()
	h(w, walletReq("assets/scripts/skycoin-lite.wasm"))
	switch {
	case execwasm.Present():
		if w.Code != http.StatusOK || w.Header().Get("ETag") != `"`+execwasm.Stamp()+`"` {
			t.Fatalf("skycoin-lite.wasm: status=%d etag=%q, want 200 under the execwasm stamp", w.Code, w.Header().Get("ETag"))
		}
	default:
		if w.Code != http.StatusFound || w.Header().Get("Location") != execwasm.OriginPath {
			t.Fatalf("skycoin-lite.wasm: status=%d location=%q, want 302 → %s", w.Code, w.Header().Get("Location"), execwasm.OriginPath)
		}
		t.Log("no module embedded in this build: the wasm route redirects to /skywire.wasm")
	}
}
