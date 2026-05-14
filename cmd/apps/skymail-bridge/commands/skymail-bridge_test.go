package commands

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// testPK is a stable, well-known PubKey we can encode/decode without
// hitting the variable output of GenerateKeyPair.
const testPKHex = "027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed"

func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	var pk cipher.PubKey
	if err := pk.Set(testPKHex); err != nil {
		t.Fatalf("Set: %v", err)
	}
	return pk
}

func TestParseSkynetRecipient_ModeB_StripsHostPart(t *testing.T) {
	pk := mustPK(t)
	addr := "user@magnetosphere.net." + pk.DNSLabel() + ".skynet"

	gotPK, fwd, isSkynet, err := parseSkynetRecipient(addr, ".skynet", "b")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !isSkynet {
		t.Fatalf("isSkynet=false on .skynet address")
	}
	if gotPK != pk {
		t.Errorf("PK mismatch: got %x want %x", gotPK, pk)
	}
	if fwd != "user@magnetosphere.net" {
		t.Errorf("forward = %q, want user@magnetosphere.net", fwd)
	}
}

func TestParseSkynetRecipient_ModeA_Verbatim(t *testing.T) {
	pk := mustPK(t)
	addr := "user@" + pk.DNSLabel() + ".skynet"

	gotPK, fwd, isSkynet, err := parseSkynetRecipient(addr, ".skynet", "a")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !isSkynet || gotPK != pk {
		t.Fatalf("isSkynet=%v PK=%x want true, %x", isSkynet, gotPK, pk)
	}
	if fwd != addr {
		t.Errorf("forward = %q, want %q (verbatim in mode A)", fwd, addr)
	}
}

func TestParseSkynetRecipient_ModeB_RequiresHostPart(t *testing.T) {
	pk := mustPK(t)
	addr := "user@" + pk.DNSLabel() + ".skynet" // no host before pk label

	if _, _, _, err := parseSkynetRecipient(addr, ".skynet", "b"); err == nil {
		t.Errorf("mode b accepted bare <pk>.skynet, expected error")
	}
}

func TestParseSkynetRecipient_NonSkynet_PassThrough(t *testing.T) {
	gotPK, fwd, isSkynet, err := parseSkynetRecipient("user@example.com", ".skynet", "b")
	if err != nil {
		t.Errorf("err = %v on non-skynet address", err)
	}
	if isSkynet {
		t.Errorf("isSkynet=true on plain user@example.com")
	}
	if !gotPK.Null() || fwd != "" {
		t.Errorf("non-skynet result not zero-valued: pk=%x fwd=%q", gotPK, fwd)
	}
}

func TestParseSkynetRecipient_PKLabelWrongLength(t *testing.T) {
	addr := "user@aaaa.skynet"
	if _, _, _, err := parseSkynetRecipient(addr, ".skynet", "a"); err == nil {
		t.Errorf("4-char pk label accepted, expected error")
	}
}

func TestParseSkynetRecipient_MissingAt(t *testing.T) {
	if _, _, _, err := parseSkynetRecipient("no-at-sign.skynet", ".skynet", "a"); err == nil {
		t.Errorf("address without '@' accepted, expected error")
	}
}

func TestParseSkynetRecipient_CaseInsensitive(t *testing.T) {
	pk := mustPK(t)
	addr := "user@MAGNETOSPHERE.NET." + strings.ToUpper(pk.DNSLabel()) + ".SKYNET"

	gotPK, fwd, isSkynet, err := parseSkynetRecipient(addr, ".skynet", "b")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !isSkynet || gotPK != pk {
		t.Errorf("upper-case address not recognized")
	}
	if fwd != "user@magnetosphere.net" {
		t.Errorf("forward = %q, want lowercased host", fwd)
	}
}

func TestParseFromTo(t *testing.T) {
	if a, err := parseFromArg("FROM:<user@example.com>"); err != nil || a != "user@example.com" {
		t.Errorf("FROM parse: got (%q, %v)", a, err)
	}
	if a, err := parseToArg("TO:<rcpt@example.com>"); err != nil || a != "rcpt@example.com" {
		t.Errorf("TO parse: got (%q, %v)", a, err)
	}
	if _, err := parseFromArg("MISSING_KEYWORD"); err == nil {
		t.Errorf("expected error on missing FROM:")
	}
	if _, err := parseToArg("TO:no-angle"); err == nil {
		t.Errorf("expected error on missing '<'")
	}
}

func TestSplitCommand(t *testing.T) {
	for _, tc := range []struct {
		in, verb, rest string
	}{
		{"HELO host", "HELO", "host"},
		{"MAIL  FROM:<a@b>", "MAIL", "FROM:<a@b>"},
		{"QUIT", "QUIT", ""},
		{"", "", ""},
	} {
		v, r := splitCommand(tc.in)
		if v != tc.verb || r != tc.rest {
			t.Errorf("splitCommand(%q) = (%q,%q), want (%q,%q)", tc.in, v, r, tc.verb, tc.rest)
		}
	}
}
