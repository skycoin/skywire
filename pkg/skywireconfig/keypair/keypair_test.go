package keypair

import (
	"regexp"
	"testing"
)

// PK is a 33-byte compressed secp256k1 point → 66 hex chars.
// SK is a 32-byte scalar → 64 hex chars.
var (
	pkHexRE = regexp.MustCompile(`^[0-9a-f]{66}$`)
	skHexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func TestGenerate_HexFormat(t *testing.T) {
	pk, sk := Generate()
	if !pkHexRE.MatchString(pk) {
		t.Errorf("pk = %q; want 66 lowercase hex chars", pk)
	}
	if !skHexRE.MatchString(sk) {
		t.Errorf("sk = %q; want 64 lowercase hex chars", sk)
	}
}

func TestGenerate_UniquePerCall(t *testing.T) {
	pk1, sk1 := Generate()
	pk2, sk2 := Generate()
	if pk1 == pk2 {
		t.Error("Generate returned same PK twice — entropy source broken")
	}
	if sk1 == sk2 {
		t.Error("Generate returned same SK twice — entropy source broken")
	}
}

func TestFromSecretKey_RoundTrip(t *testing.T) {
	_, sk := Generate()
	pkRecovered, err := FromSecretKey(sk)
	if err != nil {
		t.Fatalf("FromSecretKey: %v", err)
	}
	if !pkHexRE.MatchString(pkRecovered) {
		t.Errorf("recovered pk = %q; want 66 lowercase hex chars", pkRecovered)
	}
}

func TestFromSecretKey_Malformed(t *testing.T) {
	cases := []string{
		"",
		"deadbeef",
		"not-hex-at-all",
		"00000000000000000000000000000000000000000000000000000000000000000000", // 68 chars
	}
	for _, c := range cases {
		if _, err := FromSecretKey(c); err == nil {
			t.Errorf("FromSecretKey(%q) succeeded; want error", c)
		}
	}
}
