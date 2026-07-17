package coins

import (
	"encoding/json"
	"testing"
)

func TestRegistryShape(t *testing.T) {
	if len(Registry) != 2 {
		t.Fatalf("expected 2 coins, got %d", len(Registry))
	}
	// index == ID == /coin/<index> prefix
	for i, c := range Registry {
		if c.ID != i {
			t.Errorf("coin %d: ID=%d, want %d", i, c.ID, i)
		}
		if want := "/coin/" + string(rune('0'+i)); c.NodeURL != want {
			t.Errorf("coin %d: NodeURL=%q, want %q", i, c.NodeURL, want)
		}
		if c.ServerWallets {
			t.Errorf("coin %d: ServerWallets must be false (browser-held keys)", i)
		}
	}
	if Registry[0].CoinType != "skycoin" || Registry[0].IsBitcoin() {
		t.Errorf("coin 0 should be skycoin")
	}
	if !Registry[1].IsBitcoin() {
		t.Errorf("coin 1 should be bitcoin")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	var got []Coin
	if err := json.Unmarshal(JSON(), &got); err != nil {
		t.Fatalf("JSON() not valid: %v", err)
	}
	if len(got) != 2 || got[1].CoinSymbol != "BTC" || got[0].CoinSymbol != "SKY" {
		t.Fatalf("unexpected marshaled registry: %s", JSON())
	}
}

func TestByIndex(t *testing.T) {
	if c, ok := ByIndex(1); !ok || !c.IsBitcoin() {
		t.Errorf("ByIndex(1) should be bitcoin, ok=%v", ok)
	}
	if _, ok := ByIndex(5); ok {
		t.Errorf("ByIndex(5) should be not-ok")
	}
	if _, ok := ByIndex(-1); ok {
		t.Errorf("ByIndex(-1) should be not-ok")
	}
}
