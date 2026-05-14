package skymailbridge

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

const testPKHex = "027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed"

func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	var pk cipher.PubKey
	if err := pk.Set(testPKHex); err != nil {
		t.Fatalf("Set: %v", err)
	}
	return pk
}

func TestParseRecipient_ModeB_StripsHostPart(t *testing.T) {
	pk := mustPK(t)
	addr := "user@magnetosphere.net." + pk.DNSLabel() + ".skynet"

	gotPK, fwd, isSkynet, err := ParseRecipient(addr, ".skynet", "b")
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

func TestParseRecipient_ModeA_Verbatim(t *testing.T) {
	pk := mustPK(t)
	addr := "user@" + pk.DNSLabel() + ".skynet"

	gotPK, fwd, isSkynet, err := ParseRecipient(addr, ".skynet", "a")
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

func TestParseRecipient_ModeB_RequiresHostPart(t *testing.T) {
	pk := mustPK(t)
	addr := "user@" + pk.DNSLabel() + ".skynet"

	if _, _, _, err := ParseRecipient(addr, ".skynet", "b"); err == nil {
		t.Errorf("mode b accepted bare <pk>.skynet, expected error")
	}
}

func TestParseRecipient_NonSkynet_PassThrough(t *testing.T) {
	gotPK, fwd, isSkynet, err := ParseRecipient("user@example.com", ".skynet", "b")
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

func TestParseRecipient_PKLabelWrongLength(t *testing.T) {
	addr := "user@aaaa.skynet"
	if _, _, _, err := ParseRecipient(addr, ".skynet", "a"); err == nil {
		t.Errorf("4-char pk label accepted, expected error")
	}
}

func TestParseRecipient_MissingAt(t *testing.T) {
	if _, _, _, err := ParseRecipient("no-at-sign.skynet", ".skynet", "a"); err == nil {
		t.Errorf("address without '@' accepted, expected error")
	}
}

func TestParseRecipient_CaseInsensitive(t *testing.T) {
	pk := mustPK(t)
	addr := "user@MAGNETOSPHERE.NET." + strings.ToUpper(pk.DNSLabel()) + ".SKYNET"

	gotPK, fwd, isSkynet, err := ParseRecipient(addr, ".skynet", "b")
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

func TestParseAngleAddr(t *testing.T) {
	if a, err := parseAngleAddr("FROM:<user@example.com>", "FROM"); err != nil || a != "user@example.com" {
		t.Errorf("FROM parse: got (%q, %v)", a, err)
	}
	if a, err := parseAngleAddr("TO:<rcpt@example.com>", "TO"); err != nil || a != "rcpt@example.com" {
		t.Errorf("TO parse: got (%q, %v)", a, err)
	}
	if _, err := parseAngleAddr("MISSING_KEYWORD", "FROM"); err == nil {
		t.Errorf("expected error on missing FROM:")
	}
	if _, err := parseAngleAddr("TO:no-angle", "TO"); err == nil {
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

func TestConfig_Validate(t *testing.T) {
	if err := (Config{Mode: "a"}).Validate(); err != nil {
		t.Errorf("a: %v", err)
	}
	if err := (Config{Mode: "b"}).Validate(); err != nil {
		t.Errorf("b: %v", err)
	}
	if err := (Config{Mode: "x"}).Validate(); err == nil {
		t.Errorf("expected error on bad mode")
	}
	if err := (Config{}).Validate(); err != nil {
		t.Errorf("zero-value Mode must validate (defaults to b)")
	}
}

func TestConfig_Defaults(t *testing.T) {
	d := (Config{}).withDefaults()
	if d.Suffix != DefaultSuffix {
		t.Errorf("Suffix default = %q, want %q", d.Suffix, DefaultSuffix)
	}
	if d.Mode != "b" {
		t.Errorf("Mode default = %q, want b", d.Mode)
	}
	if d.HeloName != DefaultHeloName {
		t.Errorf("HeloName default = %q, want %q", d.HeloName, DefaultHeloName)
	}
	if d.RemotePort != 25 {
		t.Errorf("RemotePort default = %d, want 25", d.RemotePort)
	}

	d2 := (Config{Suffix: "skynet"}).withDefaults() // no leading dot
	if d2.Suffix != ".skynet" {
		t.Errorf("leading-dot fix: got %q", d2.Suffix)
	}
}
