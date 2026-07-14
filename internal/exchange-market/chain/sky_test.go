package chain

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDepositConfirmed verifies the SKY deposit check matches an output of the
// expected amount with enough confirmations, and rejects otherwise.
func TestDepositConfirmed(t *testing.T) {
	const wallet = "2sky...market"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/transactions" {
			http.NotFound(w, r)
			return
		}
		// One confirmed tx paying 10.001234 SKY to the market wallet, 3 confs.
		resp := []map[string]any{
			{
				"status": map[string]any{"confirmed": true, "height": 3, "block_seq": 100},
				"txn": map[string]any{
					"txid":    "tx-good",
					"outputs": []map[string]any{{"dst": wallet, "coins": "10.001234"}},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	node := NewSkyNode(srv.URL, "", "", 2, srv.Client())

	// Exact match with sufficient confirmations.
	ok, txid, err := node.DepositConfirmed(wallet, 10.001234)
	if err != nil {
		t.Fatalf("DepositConfirmed: %v", err)
	}
	if !ok || txid != "tx-good" {
		t.Fatalf("expected confirmed deposit tx-good, got ok=%v txid=%q", ok, txid)
	}

	// Different amount → not matched.
	ok, _, err = node.DepositConfirmed(wallet, 10.0)
	if err != nil {
		t.Fatalf("DepositConfirmed(mismatch): %v", err)
	}
	if ok {
		t.Fatal("expected no match for a different amount")
	}
}

// TestDepositBelowConfirmations rejects a deposit that has too few confirmations.
func TestDepositBelowConfirmations(t *testing.T) {
	const wallet = "2sky...market"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := []map[string]any{{
			"status": map[string]any{"confirmed": true, "height": 1, "block_seq": 100},
			"txn":    map[string]any{"txid": "tx-shallow", "outputs": []map[string]any{{"dst": wallet, "coins": "5.000000"}}},
		}}
		_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	node := NewSkyNode(srv.URL, "", "", 2, srv.Client()) // needs 2, tx has 1
	ok, _, err := node.DepositConfirmed(wallet, 5.0)
	if err != nil {
		t.Fatalf("DepositConfirmed: %v", err)
	}
	if ok {
		t.Fatal("expected rejection: only 1 confirmation, need 2")
	}
}

// Note: the SKY spend path (local build + sign + inject) is covered end-to-end in
// sky_signer_test.go. TestSendSKYNoWallet here only asserts the no-seed guard.

// TestSendSKYNoWallet errors clearly when no escrow seed is configured.
func TestSendSKYNoWallet(t *testing.T) {
	node := NewSkyNode("http://127.0.0.1:1", "", "", 2, nil)
	if _, err := node.SendSKY("addr", 1); err == nil {
		t.Fatal("expected an error when no escrow seed is configured")
	}
}

// TestNoExplorer confirms the default explorer never confirms a payment.
func TestNoExplorer(t *testing.T) {
	c := New(Config{}, nil)
	confs, _, err := c.PaymentConfirmations("BTC", "addr", 1.0)
	if err != nil || confs != 0 {
		t.Fatalf("noExplorer should report 0 confirmations, got confs=%d err=%v", confs, err)
	}
}
