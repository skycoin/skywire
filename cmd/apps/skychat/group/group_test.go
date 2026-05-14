// Package group — cmd/apps/skychat/group/group_test.go: round-trip
// coverage for the invite + crypto + store code paths. The fan-out
// CXO wiring lives in the manager and is exercised separately by
// the manual E2E test plan (project memo).
package group

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pkN returns a fresh real PK. Tests use real keys (not raw byte
// synthesis) because cipher.PubKey.UnmarshalText validates the
// curve-point via cipher.PubKeyFromHex; a synthetic non-point would
// silently round-trip to the zero PK and mask the actual SetMembers /
// allowlist behavior being tested. The `n` parameter is just for
// readability when an assertion fails — it doesn't influence the key.
func pkN(_ byte) cipher.PubKey {
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

func TestInviteRoundTripPublic(t *testing.T) {
	in := Invite{
		ID:      "11111111-2222-3333-4444-555555555555",
		Name:    "operator support",
		OwnerPK: pkN(0x01),
		Port:    20001,
		Mode:    ModePublic,
	}
	link, err := EncodeInvite(in)
	if err != nil {
		t.Fatalf("EncodeInvite: %v", err)
	}
	if got := link; len(got) == 0 || got[:len(inviteURIScheme)] != inviteURIScheme {
		t.Errorf("link missing scheme prefix: %q", got)
	}
	out, err := DecodeInvite(link)
	if err != nil {
		t.Fatalf("DecodeInvite: %v", err)
	}
	if out.ID != in.ID || out.Name != in.Name || out.Port != in.Port || out.Mode != in.Mode || out.OwnerPK != in.OwnerPK {
		t.Errorf("round trip mismatch: %+v vs %+v", in, out)
	}
	if len(out.AESKey) != 0 {
		t.Errorf("public invite should not carry AES key, got %d bytes", len(out.AESKey))
	}
}

func TestInviteRoundTripPrivate(t *testing.T) {
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatalf("GenerateAESKey: %v", err)
	}
	in := Invite{
		ID:      "abcdef01-2345-6789-abcd-ef0123456789",
		Name:    "private ops",
		OwnerPK: pkN(0x02),
		Port:    20002,
		Mode:    ModePrivate,
		AESKey:  key,
	}
	link, err := EncodeInvite(in)
	if err != nil {
		t.Fatalf("EncodeInvite: %v", err)
	}
	out, err := DecodeInvite(link)
	if err != nil {
		t.Fatalf("DecodeInvite: %v", err)
	}
	if !bytes.Equal(out.AESKey, in.AESKey) {
		t.Errorf("AES key did not survive round trip")
	}
}

func TestInviteRejectsModeKeyMismatch(t *testing.T) {
	// Public must NOT have a key.
	if _, err := EncodeInvite(Invite{
		ID: "x", Name: "x", OwnerPK: pkN(1), Port: 1, Mode: ModePublic,
		AESKey: make([]byte, 32),
	}); err == nil {
		t.Error("encode: public with AES key should error")
	}
	// Private must have a 32-byte key.
	if _, err := EncodeInvite(Invite{
		ID: "x", Name: "x", OwnerPK: pkN(1), Port: 1, Mode: ModePrivate,
		AESKey: make([]byte, 16), // wrong length
	}); err == nil {
		t.Error("encode: private with short key should error")
	}
	// Decoder also rejects malformed.
	if _, err := DecodeInvite("skychat:invite:!!!!"); err == nil {
		t.Error("decode: garbage payload should error")
	}
	if _, err := DecodeInvite("not-an-invite"); err == nil {
		t.Error("decode: missing scheme should error")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatalf("GenerateAESKey: %v", err)
	}
	plain := []byte("hello group, this is a chat message with utf-8: 你好")
	ct, nonce, err := Encrypt(key, plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(nonce) != 12 {
		t.Errorf("nonce should be 12 bytes (GCM standard), got %d", len(nonce))
	}
	if bytes.Equal(ct, plain) {
		t.Error("ciphertext should not equal plaintext")
	}
	pt, err := Decrypt(key, ct, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(pt, plain) {
		t.Errorf("decrypt mismatch: %q vs %q", pt, plain)
	}
}

func TestDecryptRejectsTampered(t *testing.T) {
	key, _ := GenerateAESKey()                             //nolint:errcheck
	ct, nonce, _ := Encrypt(key, []byte("secret message")) //nolint:errcheck
	// Flip one byte in the ciphertext. GCM's MAC should detect it.
	ct[0] ^= 0xff
	if _, err := Decrypt(key, ct, nonce); err == nil {
		t.Error("Decrypt should reject tampered ciphertext")
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	k1, _ := GenerateAESKey()                          //nolint:errcheck
	k2, _ := GenerateAESKey()                          //nolint:errcheck
	ct, nonce, _ := Encrypt(k1, []byte("for k1 only")) //nolint:errcheck
	if _, err := Decrypt(k2, ct, nonce); err == nil {
		t.Error("Decrypt should reject wrong key")
	}
}

func TestDeriveMirrorPath(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantOK  bool
		wantOut string
	}{
		{
			name:    "msgs leaf rewrites to mirror",
			in:      "msgs/02abcdef00112233445566778899aabbccddeeff00112233445566778899aabbcc/1700000000000000000/1",
			wantOK:  true,
			wantOut: "mirror/02abcdef00112233445566778899aabbccddeeff00112233445566778899aabbcc/1700000000000000000/1",
		},
		{
			name:    "already-mirrored leaf is rejected (loop guard)",
			in:      "mirror/02abcdef00112233445566778899aabbccddeeff00112233445566778899aabbcc/1700000000000000000/1",
			wantOK:  false,
			wantOut: "",
		},
		{
			name:    "non-message subtree is rejected",
			in:      "config/heartbeat/whatever",
			wantOK:  false,
			wantOut: "",
		},
		{
			name:    "bare msgs without slash is rejected (would otherwise produce mirror/ root)",
			in:      "msgs",
			wantOK:  false,
			wantOut: "",
		},
		{
			name:    "empty input is rejected",
			in:      "",
			wantOK:  false,
			wantOut: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := deriveMirrorPath(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("deriveMirrorPath(%q): ok=%v, want %v", tc.in, ok, tc.wantOK)
			}
			if got != tc.wantOut {
				t.Errorf("deriveMirrorPath(%q): got %q, want %q", tc.in, got, tc.wantOut)
			}
		})
	}
}

// Two random nonces from independent calls must not collide in a
// reasonable number of iterations. A regression that fixes the
// nonce (e.g. someone hard-codes it for "convenience") would fail
// this immediately.
func TestEncryptNonceUnique(t *testing.T) {
	key, _ := GenerateAESKey() //nolint:errcheck
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		_, n, err := Encrypt(key, []byte("x"))
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		s := string(n)
		if _, dup := seen[s]; dup {
			t.Fatalf("nonce collision at iteration %d", i)
		}
		seen[s] = struct{}{}
	}
}

func TestStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "groups.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close() //nolint:errcheck

	id := "abcdef01-0000-0000-0000-000000000000"
	r := Record{
		ID:        id,
		Name:      "test",
		OwnerPK:   pkN(1),
		Port:      30001,
		Mode:      ModePublic,
		Members:   []cipher.PubKey{pkN(1), pkN(2), pkN(3)},
		Role:      RoleOwner,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Put(r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := s.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: err=%v ok=%v", err, ok)
	}
	if got.Name != r.Name || got.Port != r.Port || got.Mode != r.Mode {
		t.Errorf("Get mismatch: %+v", got)
	}
	// SetStatus + List.
	if err := s.SetStatus(id, StatusActive); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Status != StatusActive {
		t.Errorf("List/SetStatus: %+v", all)
	}
	// SetMembers replaces.
	m1, m2 := pkN(10), pkN(20)
	newMembers := []cipher.PubKey{m1, m2}
	if err := s.SetMembers(id, newMembers); err != nil {
		t.Fatalf("SetMembers: %v", err)
	}
	got, _, _ = s.Get(id) //nolint:errcheck
	if len(got.Members) != 2 || got.Members[0] != m1 || got.Members[1] != m2 {
		t.Errorf("SetMembers: %+v", got.Members)
	}
	// Delete.
	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := s.Get(id); ok { //nolint:errcheck
		t.Error("Delete: record still present")
	}
}
