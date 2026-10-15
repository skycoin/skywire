package cipher

import (
	"strings"
	"testing"
)

func TestPubKeyTakesEitherForm(t *testing.T) {
	want, _ := GenerateKeyPair()

	var fromHex, fromLabel, fromText PubKey
	if err := fromHex.Set(want.Hex()); err != nil {
		t.Fatal(err)
	}
	if err := fromLabel.Set(want.DNSLabel()); err != nil {
		t.Fatal(err)
	}
	if err := fromText.UnmarshalText([]byte(strings.ToUpper(want.DNSLabel()))); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]PubKey{"hex": fromHex, "label": fromLabel, "text": fromText} {
		if got != want {
			t.Fatalf("%s: got %s, want %s", name, got.Hex(), want.Hex())
		}
	}
	// Output stays hex, so files and APIs do not change.
	if b, _ := fromLabel.MarshalText(); string(b) != want.Hex() { //nolint:errcheck
		t.Fatalf("MarshalText = %s, want hex", b)
	}
}

func TestPubKeyRejectsABadLabel(t *testing.T) {
	var pk PubKey
	// 53 characters that decode to 33 bytes but not to a curve point.
	if err := pk.Set(strings.Repeat("a", PubKeyDNSLabelLen)); err == nil {
		t.Fatal("Set accepted a label that is not a key")
	}
	if err := pk.Set(strings.Repeat("1", PubKeyDNSLabelLen)); err == nil {
		t.Fatal("Set accepted characters outside base32")
	}
}
