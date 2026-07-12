package chain

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// fakeStore is an ExplorerConfigStore returning a fixed config for one currency.
type fakeStore struct {
	currency, provider, url, key string
}

func (f fakeStore) ExplorerConfig(currency string) (string, string, string, error) {
	if currency == f.currency {
		return f.provider, f.url, f.key, nil
	}
	return "", "", "", nil
}

func TestProvidersFor(t *testing.T) {
	if got := ProvidersFor("BTC"); !slices.Contains(got, "esplora") {
		t.Fatalf("ProvidersFor(BTC) = %v, want esplora present", got)
	}
	if got := ProvidersFor("DASH"); len(got) != 0 {
		t.Fatalf("ProvidersFor(DASH) = %v, want none (no adapter yet)", got)
	}
	if !SupportsProvider("LTC", "esplora") || SupportsProvider("DASH", "esplora") {
		t.Fatal("SupportsProvider coverage is wrong")
	}
}

// TestEsploraPaymentConfirmations serves an Esplora-shaped address txs response
// and a tip height, then checks the router matches the exact amount + confs.
func TestEsploraPaymentConfirmations(t *testing.T) {
	const addr = "bc1qbuyerpays"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/blocks/tip/height":
			_, _ = w.Write([]byte("800010")) //nolint:errcheck
		case "/api/address/" + addr + "/txs":
			txs := []map[string]any{
				{ // pays exactly 0.05000000 to addr, confirmed at height 800000 => 11 confs
					"txid":   "tx-pay",
					"status": map[string]any{"confirmed": true, "block_height": 800000},
					"vout": []map[string]any{
						{"scriptpubkey_address": addr, "value": 5000000},
						{"scriptpubkey_address": "other", "value": 999},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(txs) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Route BTC to an esplora adapter pointed at the mock server.
	c := New(Config{}, fakeStore{currency: "BTC", provider: "esplora", url: srv.URL})
	c.exp.(*router).hc = srv.Client()

	confs, txid, err := c.PaymentConfirmations("BTC", addr, 0.05)
	if err != nil {
		t.Fatalf("PaymentConfirmations: %v", err)
	}
	if confs != 11 || txid != "tx-pay" {
		t.Fatalf("got confs=%d txid=%q, want 11 / tx-pay", confs, txid)
	}

	// A different amount is not matched.
	confs, _, err = c.PaymentConfirmations("BTC", addr, 0.04)
	if err != nil {
		t.Fatalf("PaymentConfirmations(mismatch): %v", err)
	}
	if confs != 0 {
		t.Fatalf("expected no match for a different amount, got %d confs", confs)
	}
}

// TestRouterUnconfiguredCurrency returns 0 confirmations (not an error) when the
// currency has no explorer configured.
func TestRouterUnconfiguredCurrency(t *testing.T) {
	c := New(Config{}, fakeStore{}) // store returns empty provider for everything
	confs, _, err := c.PaymentConfirmations("BTC", "addr", 1.0)
	if err != nil || confs != 0 {
		t.Fatalf("unconfigured currency should be 0/nil, got confs=%d err=%v", confs, err)
	}
}
