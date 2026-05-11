package cipher

import (
	"strings"
	"testing"
)

func TestDNSLabelRoundtrip(t *testing.T) {
	pk, _ := GenerateKeyPair()
	label := pk.DNSLabel()

	if got := len(label); got != PubKeyDNSLabelLen {
		t.Fatalf("DNSLabel length = %d, want %d", got, PubKeyDNSLabelLen)
	}
	if label != strings.ToLower(label) {
		t.Errorf("DNSLabel = %q, expected lowercase", label)
	}
	if strings.ContainsAny(label, "=+/") {
		t.Errorf("DNSLabel = %q contains padding/non-base32 chars", label)
	}

	got, err := ParseDNSLabel(label)
	if err != nil {
		t.Fatalf("ParseDNSLabel: %v", err)
	}
	if got != pk {
		t.Errorf("roundtrip mismatch: got %x want %x", got[:], pk[:])
	}
}

func TestDNSLabelCaseInsensitive(t *testing.T) {
	pk, _ := GenerateKeyPair()
	label := pk.DNSLabel()
	upper, err := ParseDNSLabel(strings.ToUpper(label))
	if err != nil {
		t.Fatalf("ParseDNSLabel(uppercase): %v", err)
	}
	if upper != pk {
		t.Errorf("uppercase decode mismatch")
	}
}

func TestDNSLabelInvalid(t *testing.T) {
	cases := []string{
		"",            // empty
		"not-base32!", // bad chars
		"aa",          // too short (decodes to <33 bytes)
		strings.Repeat("a", 200), // way too long
	}
	for _, c := range cases {
		if _, err := ParseDNSLabel(c); err == nil {
			t.Errorf("ParseDNSLabel(%q) = nil err, want error", c)
		}
	}
}

func TestDNSLabelFitsRFC1035(t *testing.T) {
	// Sanity: 53 chars must be ≤ 63 (RFC 1035 label limit) and ≤ 64
	// (X.520 ub-common-name).
	if PubKeyDNSLabelLen > 63 {
		t.Fatalf("DNSLabel length %d exceeds RFC 1035 label limit (63)", PubKeyDNSLabelLen)
	}
	if PubKeyDNSLabelLen > 64 {
		t.Fatalf("DNSLabel length %d exceeds X.520 ub-common-name (64)", PubKeyDNSLabelLen)
	}
}
