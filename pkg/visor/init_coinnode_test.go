package visor

import (
	"testing"
)

// TestParseCoinHealth verifies the /api/v1/health -> CoinInfo mapping, using a
// representative skycoin mainnet health response (trimmed to the fields we read
// plus a few extras that must be ignored).
func TestParseCoinHealth(t *testing.T) {
	body := []byte(`{
		"blockchain": {
			"head": {"seq": 123456, "block_hash": "abc", "previous_block_hash": "def"},
			"unspents": 100,
			"unconfirmed": 0
		},
		"version": {"version": "0.28.0", "commit": "1e07e977", "branch": "develop"},
		"coin": "skycoin",
		"user_agent": "skycoin:0.28.0",
		"open_connections": 8,
		"csrf_enabled": true,
		"block_publisher": false,
		"blockchain_pubkey": "0328c576d3f420e7682058a981173a4b374c7cc5ff55bf394d3cf57059bbe6456a",
		"fiber": {"name": "skycoin", "display_name": "Skycoin", "ticker": "SKY", "bip44_coin": 8000}
	}`)

	info, err := parseCoinHealth(body)
	if err != nil {
		t.Fatalf("parseCoinHealth: %v", err)
	}
	if info.BlockchainPubKey != "0328c576d3f420e7682058a981173a4b374c7cc5ff55bf394d3cf57059bbe6456a" {
		t.Errorf("BlockchainPubKey = %q", info.BlockchainPubKey)
	}
	if info.Fiber != "skycoin" {
		t.Errorf("Fiber = %q, want skycoin", info.Fiber)
	}
	if info.Version != "0.28.0" {
		t.Errorf("Version = %q, want 0.28.0", info.Version)
	}
	if info.Commit != "1e07e977" {
		t.Errorf("Commit = %q, want 1e07e977", info.Commit)
	}
	if info.HeadSeq != 123456 {
		t.Errorf("HeadSeq = %d, want 123456", info.HeadSeq)
	}
}

// TestParseCoinHealthFiberFallback verifies the coin name falls back to the
// top-level "coin" field when fiber.name is absent.
func TestParseCoinHealthFiberFallback(t *testing.T) {
	info, err := parseCoinHealth([]byte(`{"coin": "mdl", "blockchain_pubkey": "02deadbeef"}`))
	if err != nil {
		t.Fatalf("parseCoinHealth: %v", err)
	}
	if info.Fiber != "mdl" {
		t.Errorf("Fiber = %q, want mdl (fallback to coin)", info.Fiber)
	}
}

// TestParseCoinHealthMalformed ensures a bad body errors rather than
// registering a bogus entry.
func TestParseCoinHealthMalformed(t *testing.T) {
	if _, err := parseCoinHealth([]byte(`not json`)); err == nil {
		t.Fatal("expected error for malformed health body")
	}
}
