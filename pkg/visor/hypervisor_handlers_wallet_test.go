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
