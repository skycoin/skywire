// Package group — cmd/apps/skychat/group/gossip_test.go: unit
// coverage for the signed-mutation envelopes added by gossip.go.
//
// Scope is pure types + sign + verify + JSON round-trip; the
// reconciler that applies mutations to a local Record lives in a
// separate file (subsequent PR) and gets its own integration-shape
// tests against the Manager surface.
package group

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// gossipKeys produces a deterministic-feeling but fresh PK/SK pair
// for each test. Pulled out so individual tests don't repeat the
// boilerplate.
func gossipKeys(t *testing.T) (cipher.PubKey, cipher.SecKey) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	return pk, sk
}

// gossipBaseRoster builds a populated-but-unsigned RosterMutation
// for the test's own arrangement. PeerPK / IssuedAt / GroupID are
// fixed so test failures point at semantic mismatches, not
// nondeterminism.
func gossipBaseRoster(peer cipher.PubKey) RosterMutation {
	return RosterMutation{
		GroupID:   uuid.MustParse("72afa409-8b2e-4323-aa1e-8be57f30ebd5"),
		Op:        RosterOpAdd,
		PeerPK:    peer,
		ParentSeq: 42,
		IssuedAt:  time.Date(2026, 5, 16, 22, 50, 0, 0, time.UTC),
	}
}

func gossipBaseAdmin(peer cipher.PubKey) AdminMutation {
	return AdminMutation{
		GroupID:   uuid.MustParse("72afa409-8b2e-4323-aa1e-8be57f30ebd5"),
		Op:        AdminOpPromote,
		PeerPK:    peer,
		ParentSeq: 7,
		IssuedAt:  time.Date(2026, 5, 16, 22, 50, 0, 0, time.UTC),
	}
}

func TestSignVerifyRosterRoundTrip(t *testing.T) {
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)
	m := gossipBaseRoster(peer)
	if err := SignRoster(&m, sk); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}
	if m.IssuerPK == (cipher.PubKey{}) {
		t.Fatal("issuer pk should be populated after sign")
	}
	if m.Signature == (cipher.Sig{}) {
		t.Fatal("signature should be populated after sign")
	}
	if err := VerifyRoster(m); err != nil {
		t.Fatalf("VerifyRoster on fresh sign: %v", err)
	}
}

func TestSignVerifyAdminRoundTrip(t *testing.T) {
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)
	m := gossipBaseAdmin(peer)
	if err := SignAdmin(&m, sk); err != nil {
		t.Fatalf("SignAdmin: %v", err)
	}
	if err := VerifyAdmin(m); err != nil {
		t.Fatalf("VerifyAdmin on fresh sign: %v", err)
	}
}

func TestVerifyDetectsTamperedField(t *testing.T) {
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)
	m := gossipBaseRoster(peer)
	if err := SignRoster(&m, sk); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}
	// Flip Op — same canonical-bytes prefix until the Op byte, so
	// the signature should clearly not bind to the new payload.
	tampered := m
	tampered.Op = RosterOpRemove
	if err := VerifyRoster(tampered); !errors.Is(err, ErrGossipBadSignature) {
		t.Fatalf("expected ErrGossipBadSignature on tampered Op, got %v", err)
	}
	// Flip PeerPK.
	tampered = m
	tampered.PeerPK[0] ^= 0xFF
	if err := VerifyRoster(tampered); !errors.Is(err, ErrGossipBadSignature) {
		t.Fatalf("expected ErrGossipBadSignature on tampered PeerPK, got %v", err)
	}
	// Bump ParentSeq.
	tampered = m
	tampered.ParentSeq++
	if err := VerifyRoster(tampered); !errors.Is(err, ErrGossipBadSignature) {
		t.Fatalf("expected ErrGossipBadSignature on tampered ParentSeq, got %v", err)
	}
	// Bump IssuedAt by 1ns.
	tampered = m
	tampered.IssuedAt = tampered.IssuedAt.Add(time.Nanosecond)
	if err := VerifyRoster(tampered); !errors.Is(err, ErrGossipBadSignature) {
		t.Fatalf("expected ErrGossipBadSignature on tampered IssuedAt, got %v", err)
	}
}

func TestVerifyDetectsWrongIssuer(t *testing.T) {
	// Sign with sk1, swap IssuerPK to pk2 — the verifier should
	// reject because the signature still recovers to pk1 but the
	// envelope claims pk2.
	_, sk1 := gossipKeys(t)
	pk2, _ := gossipKeys(t)
	peer, _ := gossipKeys(t)
	m := gossipBaseRoster(peer)
	if err := SignRoster(&m, sk1); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}
	m.IssuerPK = pk2
	if err := VerifyRoster(m); !errors.Is(err, ErrGossipBadSignature) {
		t.Fatalf("expected ErrGossipBadSignature on swapped IssuerPK, got %v", err)
	}
}

func TestSignRejectsZeroFields(t *testing.T) {
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)

	// Zero GroupID.
	m := gossipBaseRoster(peer)
	m.GroupID = uuid.UUID{}
	if err := SignRoster(&m, sk); !errors.Is(err, ErrGossipMissingFields) {
		t.Errorf("expected ErrGossipMissingFields on zero GroupID, got %v", err)
	}

	// Zero PeerPK.
	m = gossipBaseRoster(peer)
	m.PeerPK = cipher.PubKey{}
	if err := SignRoster(&m, sk); !errors.Is(err, ErrGossipMissingFields) {
		t.Errorf("expected ErrGossipMissingFields on zero PeerPK, got %v", err)
	}

	// Zero IssuedAt.
	m = gossipBaseRoster(peer)
	m.IssuedAt = time.Time{}
	if err := SignRoster(&m, sk); !errors.Is(err, ErrGossipMissingFields) {
		t.Errorf("expected ErrGossipMissingFields on zero IssuedAt, got %v", err)
	}

	// Nil receiver.
	if err := SignRoster(nil, sk); !errors.Is(err, ErrGossipMissingFields) {
		t.Errorf("expected ErrGossipMissingFields on nil receiver, got %v", err)
	}
}

func TestSignRejectsUnknownOp(t *testing.T) {
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)
	m := gossipBaseRoster(peer)
	m.Op = 99
	if err := SignRoster(&m, sk); !errors.Is(err, ErrGossipUnknownOp) {
		t.Fatalf("expected ErrGossipUnknownOp on Op=99, got %v", err)
	}

	a := gossipBaseAdmin(peer)
	a.Op = 99
	if err := SignAdmin(&a, sk); !errors.Is(err, ErrGossipUnknownOp) {
		t.Fatalf("expected ErrGossipUnknownOp on AdminOp=99, got %v", err)
	}
}

func TestVerifyRejectsUnknownOp(t *testing.T) {
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)
	m := gossipBaseRoster(peer)
	// Sign at a valid Op, then mutate to an invalid Op AFTER
	// signing — verifier should reject on the op-range check
	// before reaching the signature path.
	if err := SignRoster(&m, sk); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}
	m.Op = 99
	if err := VerifyRoster(m); !errors.Is(err, ErrGossipUnknownOp) {
		t.Fatalf("expected ErrGossipUnknownOp on Op=99, got %v", err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)
	m := gossipBaseRoster(peer)
	if err := SignRoster(&m, sk); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}

	wire, err := MarshalRoster(m)
	if err != nil {
		t.Fatalf("MarshalRoster: %v", err)
	}
	got, err := UnmarshalRoster(wire)
	if err != nil {
		t.Fatalf("UnmarshalRoster: %v", err)
	}
	if got != m {
		t.Fatalf("round-trip mismatch:\n  want %+v\n  got  %+v", m, got)
	}

	a := gossipBaseAdmin(peer)
	if err := SignAdmin(&a, sk); err != nil {
		t.Fatalf("SignAdmin: %v", err)
	}
	wire, err = MarshalAdmin(a)
	if err != nil {
		t.Fatalf("MarshalAdmin: %v", err)
	}
	gota, err := UnmarshalAdmin(wire)
	if err != nil {
		t.Fatalf("UnmarshalAdmin: %v", err)
	}
	if gota != a {
		t.Fatalf("admin round-trip mismatch:\n  want %+v\n  got  %+v", a, gota)
	}
}

func TestUnmarshalRejectsTamperedWire(t *testing.T) {
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)
	m := gossipBaseRoster(peer)
	if err := SignRoster(&m, sk); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}

	wire, err := MarshalRoster(m)
	if err != nil {
		t.Fatalf("MarshalRoster: %v", err)
	}

	// Decode → mutate → re-encode → confirm rejection.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(wire, &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	raw["op"] = json.RawMessage([]byte("2")) // RosterOpRemove instead of Add
	tampered, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if _, err := UnmarshalRoster(tampered); !errors.Is(err, ErrGossipBadSignature) {
		t.Fatalf("expected ErrGossipBadSignature on tampered wire, got %v", err)
	}
}

func TestCanonicalBytesIsStableAcrossRuns(t *testing.T) {
	// Two identical envelopes (same fields) must hash to the same
	// canonical bytes so a re-signed mutation produces a verifying
	// signature for any party reconstructing the canonical form.
	_, sk := gossipKeys(t)
	peer, _ := gossipKeys(t)
	a := gossipBaseRoster(peer)
	b := gossipBaseRoster(peer)
	if err := SignRoster(&a, sk); err != nil {
		t.Fatalf("SignRoster a: %v", err)
	}
	if err := SignRoster(&b, sk); err != nil {
		t.Fatalf("SignRoster b: %v", err)
	}
	// Secp256k1 signatures are randomized via the nonce k, so the
	// signatures themselves differ — but both must verify against
	// the same envelope.
	if err := VerifyRoster(a); err != nil {
		t.Fatalf("VerifyRoster a: %v", err)
	}
	bWithASig := b
	bWithASig.Signature = a.Signature
	if err := VerifyRoster(bWithASig); err != nil {
		t.Fatalf("cross-sig verify (same canonical bytes): %v", err)
	}
}
