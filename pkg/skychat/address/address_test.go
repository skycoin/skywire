// Package address pkg/skychat/address/address_test.go c4-app-chat
// round-trip and rejection tests for the skychat:// grammar.
//
// The rejection half matters more than the happy path: this parser is
// the first thing an arbitrary pasted or QR-decoded string reaches, so
// every malformed shape has to fail with a message rather than produce a
// plausible-looking Address that later costs a dial to disprove.
package address

import (
	"errors"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// testPK is a valid compressed secp256k1 key in the shape every skychat
// surface uses (02-prefixed, 66 hex chars).
const testPK = "0248c948affc71f4dd6b0b6b47e5d5f1e0e13bc39d3f5d5f4e1f3d3a6e6c0b2b7f"

const testGID = "9c1e4b7a-2d3f-4c5e-8a9b-0d1e2f3a4b5c"

func mustPK(t *testing.T, hex string) cipher.PubKey {
	t.Helper()
	var pk cipher.PubKey
	if err := pk.Set(hex); err != nil {
		t.Fatalf("test key %q is not a valid public key: %v", hex, err)
	}
	return pk
}

func TestParseDM(t *testing.T) {
	pk := mustPK(t, testPK)
	// Every spelling a user can plausibly arrive with must land on the
	// same address: the scheme is optional because the field this feeds
	// used to accept a bare key, and trailing slashes / whitespace are
	// what a copy-paste or a QR decode leaves behind.
	for _, in := range []string{
		Scheme + testPK,
		testPK,
		"  " + Scheme + testPK + "  ",
		Scheme + testPK + "/",
		"SKYCHAT://" + testPK,
		Scheme + strings.ToUpper(testPK),
	} {
		got, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): unexpected error: %v", in, err)
		}
		if got.PK != pk {
			t.Errorf("Parse(%q): pk = %s, want %s", in, got.PK.Hex(), pk.Hex())
		}
		if got.GroupID != "" {
			t.Errorf("Parse(%q): group id = %q, want empty", in, got.GroupID)
		}
		if got.Kind() != KindDM {
			t.Errorf("Parse(%q): kind = %q, want %q", in, got.Kind(), KindDM)
		}
		if got.IsGroup() {
			t.Errorf("Parse(%q): IsGroup() = true, want false", in)
		}
	}
}

func TestParseGroup(t *testing.T) {
	pk := mustPK(t, testPK)
	for _, in := range []string{
		Scheme + testPK + "/" + testGID,
		testPK + "/" + testGID,
		Scheme + testPK + "/" + testGID + "/",
	} {
		got, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): unexpected error: %v", in, err)
		}
		if got.PK != pk {
			t.Errorf("Parse(%q): pk = %s, want %s", in, got.PK.Hex(), pk.Hex())
		}
		if got.GroupID != testGID {
			t.Errorf("Parse(%q): group id = %q, want %q", in, got.GroupID, testGID)
		}
		if got.Kind() != KindGroup {
			t.Errorf("Parse(%q): kind = %q, want %q", in, got.Kind(), KindGroup)
		}
		if !got.IsGroup() {
			t.Errorf("Parse(%q): IsGroup() = false, want true", in)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	pk := mustPK(t, testPK)
	for _, a := range []Address{DM(pk), Group(pk, testGID)} {
		back, err := Parse(a.String())
		if err != nil {
			t.Fatalf("Parse(%q): %v", a.String(), err)
		}
		if back != a {
			t.Errorf("round trip of %q: got %+v, want %+v", a.String(), back, a)
		}
	}
}

// A canonical address is lowercase, because the UI keys its contact list
// on the string: two spellings of one identity must not read as two.
func TestStringIsLowercase(t *testing.T) {
	pk := mustPK(t, strings.ToUpper(testPK))
	got := DM(pk).String()
	if got != Scheme+strings.ToLower(testPK) {
		t.Errorf("String() = %q, want lowercase %q", got, Scheme+strings.ToLower(testPK))
	}
}

// An invite link has to be distinguishable rather than merely invalid:
// one paste box accepts both, and a user who pasted a working invite
// must not be told their address is malformed.
func TestParseRejectsInviteWithSentinel(t *testing.T) {
	for _, in := range []string{
		"skychat:invite:eyJpZCI6IngifQ",
		"  SKYCHAT:INVITE:eyJpZCI6IngifQ  ",
	} {
		_, err := Parse(in)
		if !errors.Is(err, ErrIsInvite) {
			t.Errorf("Parse(%q): err = %v, want ErrIsInvite", in, err)
		}
		if !IsInvite(in) {
			t.Errorf("IsInvite(%q) = false, want true", in)
		}
	}
	if IsInvite(Scheme + testPK) {
		t.Error("IsInvite reported true for a plain address")
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"scheme only", Scheme},
		{"short key", Scheme + testPK[:64]},
		{"long key", Scheme + testPK + "ab"},
		{"non-hex key", Scheme + strings.Repeat("z", 66)},
		{"group id not a uuid", Scheme + testPK + "/not-a-uuid"},
		{"extra path segment", Scheme + testPK + "/" + testGID + "/extra"},
		{"empty key with group", Scheme + "/" + testGID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Parse(tt.in); err == nil {
				t.Errorf("Parse(%q) = %+v, want error", tt.in, got)
			}
		})
	}
}

// ErrEmpty is separate from a generic parse failure so a caller can keep
// quiet while the user is still typing rather than flashing an error on
// an empty field.
func TestParseEmptySentinel(t *testing.T) {
	for _, in := range []string{"", "   ", Scheme, Scheme + "/"} {
		if _, err := Parse(in); !errors.Is(err, ErrEmpty) {
			t.Errorf("Parse(%q): err = %v, want ErrEmpty", in, err)
		}
	}
}
