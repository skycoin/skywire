package btcgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skycoin/skycoin/src/btc"
)

// mockBackend is a btc.Backend that returns canned data — lets us test the
// gateway's routing + JSON shapes without a live Electrum server.
type mockBackend struct{}

func (mockBackend) GetBalance(addrs []string) (map[string]btc.AddressBalance, error) {
	out := make(map[string]btc.AddressBalance, len(addrs))
	for _, a := range addrs {
		out[a] = btc.AddressBalance{Confirmed: 1000, Unconfirmed: 0}
	}
	return out, nil
}
func (mockBackend) ListUnspent([]string) ([]btc.UTXO, error)       { return []btc.UTXO{}, nil }
func (mockBackend) GetHistory([]string) ([]btc.Transaction, error) { return []btc.Transaction{}, nil }
func (mockBackend) BroadcastTransaction(string) (string, error)    { return "deadbeef", nil }
func (mockBackend) EstimateFee(int) (int64, error)                 { return 12, nil }
func (mockBackend) Close() error                                   { return nil }

func newTestGateway() *Gateway {
	g := New(nil)
	g.backends["mock"] = mockBackend{} // pre-seed so no dial happens
	return g
}

func do(g *Gateway, method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("X-Skywire-Btc-Backend", "mock")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func TestBalanceShape(t *testing.T) {
	w := do(newTestGateway(), http.MethodGet, "/v1/btc/balance?addrs=addrA,addrB")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Confirmed struct {
			Coins int64 `json:"coins"`
		} `json:"confirmed"`
		Addresses map[string]map[string]map[string]int64 `json:"addresses"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v — %s", err, w.Body.String())
	}
	if resp.Confirmed.Coins != 2000 {
		t.Fatalf("total confirmed = %d, want 2000", resp.Confirmed.Coins)
	}
	if got := resp.Addresses["addrA"]["confirmed"]["coins"]; got != 1000 {
		t.Fatalf("addrA confirmed = %d, want 1000", got)
	}
}

func TestFeeShape(t *testing.T) {
	w := do(newTestGateway(), http.MethodGet, "/v1/btc/fee?blocks=3")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		SatPerByte int64 `json:"sat_per_byte"`
		Blocks     int   `json:"blocks"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp) //nolint
	if resp.SatPerByte != 12 || resp.Blocks != 3 {
		t.Fatalf("fee resp = %+v, want {12, 3}", resp)
	}
}

func TestMissingBackendHeader(t *testing.T) {
	g := newTestGateway()
	r := httptest.NewRequest(http.MethodGet, "/v1/btc/balance?addrs=a", nil) // no header
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (no backend configured)", w.Code)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	if w := do(newTestGateway(), http.MethodGet, "/v1/btc/nope"); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
