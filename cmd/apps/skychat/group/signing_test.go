// Package group — cmd/apps/skychat/group/signing_test.go: pins
// the contract on leaf signing/verification that admin-aggregator
// safety relies on. Specifically:
//
//   - A signed Message verifies against the SenderPK that signed it.
//   - Tampering ANY signed field (TS, Text, Ciphertext, Nonce, SenderPK)
//     invalidates the signature.
//   - The Signature field itself is NOT covered by canonicalBytes
//     (a verifier with the same SenderPK can produce a fresh signature
//     over the same body — that's fine; the body is what matters).
//   - Unsigned legacy leaves surface as ErrLeafUnsignedLegacy distinctly
//     from ErrLeafBadSignature, so the inbox path can downgrade legacy
//     leaves to accept-with-warning without accepting forgeries.
package group

import (
	"errors"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	m := Message{
		SenderPK: pk,
		TS:       time.Date(2026, 5, 17, 20, 0, 0, 0, time.UTC),
		Text:     "hello",
	}
	if err := SignMessage(&m, sk); err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	if m.Signature == (cipher.Sig{}) {
		t.Fatal("SignMessage: signature still zero after sign")
	}
	if err := VerifyMessage(m); err != nil {
		t.Errorf("VerifyMessage on fresh signed message: %v", err)
	}
}

func TestVerifyRejectsTampered(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	base := Message{
		SenderPK: pk,
		TS:       time.Date(2026, 5, 17, 20, 0, 0, 0, time.UTC),
		Text:     "original",
	}
	if err := SignMessage(&base, sk); err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	// Every signed-field perturbation must fail Verify; pre-fix we
	// could let an admin republish-and-edit a leaf because none of
	// these were covered.
	cases := []struct {
		name string
		mut  func(*Message)
	}{
		{"text changed", func(m *Message) { m.Text = "tampered" }},
		{"ts changed", func(m *Message) { m.TS = m.TS.Add(time.Second) }},
		{"sender pk changed", func(m *Message) { other, _ := cipher.GenerateKeyPair(); m.SenderPK = other }},
		{"ciphertext changed", func(m *Message) { m.Ciphertext = []byte{0xff} }},
		{"nonce changed", func(m *Message) { m.Nonce = []byte{0xff} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tampered := base
			tc.mut(&tampered)
			err := VerifyMessage(tampered)
			if err == nil {
				t.Fatal("VerifyMessage accepted tampered message")
			}
			// sender-pk mutation rejects via ErrLeafBadSignature
			// (signature no longer binds to the new PK).
			if !errors.Is(err, ErrLeafBadSignature) {
				t.Errorf("expected ErrLeafBadSignature, got %v", err)
			}
		})
	}
}

func TestVerifyUnsignedLegacy(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	m := Message{SenderPK: pk, TS: time.Now().UTC(), Text: "legacy"}
	// Signature deliberately zero — simulates a pre-signing leaf on disk.
	err := VerifyMessage(m)
	if !errors.Is(err, ErrLeafUnsignedLegacy) {
		t.Fatalf("expected ErrLeafUnsignedLegacy on zero Signature, got %v", err)
	}
	// Must NOT also return ErrLeafBadSignature — the two sentinels
	// are distinct so the inbox path can branch on accept-with-
	// warning vs strict-reject.
	if errors.Is(err, ErrLeafBadSignature) {
		t.Error("ErrLeafUnsignedLegacy must not also satisfy errors.Is(ErrLeafBadSignature)")
	}
}

func TestVerifyMissingSenderPK(t *testing.T) {
	m := Message{TS: time.Now().UTC(), Text: "no sender"}
	if err := VerifyMessage(m); !errors.Is(err, ErrLeafMissingSenderPK) {
		t.Errorf("expected ErrLeafMissingSenderPK, got %v", err)
	}
}

func TestSignWrongSKBindsToDerivedPK(t *testing.T) {
	// publishAs is parametric in senderPK — the caller passes
	// whichever PK should appear as the author. SignMessage uses
	// the SK passed to it; if those don't match, verify fails. This
	// is the relay-path protection: a leaf signed by the owner's SK
	// but claiming a different SenderPK will reject. Documents the
	// invariant called out in publishAs's comment.
	authorPK, _ := cipher.GenerateKeyPair()
	_, relaySK := cipher.GenerateKeyPair()
	m := Message{
		SenderPK: authorPK, // claims author identity
		TS:       time.Now().UTC(),
		Text:     "relayed",
	}
	if err := SignMessage(&m, relaySK); err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	err := VerifyMessage(m)
	if err == nil {
		t.Fatal("VerifyMessage accepted leaf signed by non-matching SK")
	}
	if !errors.Is(err, ErrLeafBadSignature) {
		t.Errorf("expected ErrLeafBadSignature on SK/SenderPK mismatch, got %v", err)
	}
}
