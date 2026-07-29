// Package group cmd/apps/skychat/group/moderation_test.go c4-app-chat
// tests for the moderation gossip envelope and — the reason this file
// leads with them — the domain separation that keeps the three signed
// envelope families from being interchangeable.
package group

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestCrossTypeSignatureReplay is a regression test for a privilege
// escalation that existed before domain separation.
//
// canonicalBytesRoster and canonicalBytesAdmin emitted identical
// layouts, and their op spaces collide numerically (RosterOpAdd == 1 ==
// AdminOpPromote). Every member joins via an admin-signed roster/<seq>
// leaf with Op=1, readable by every other member. Copying that leaf's
// issuer + signature + timestamp into an admin/<seq> envelope with
// Op=AdminOpPromote produced a mutation that VerifyAdmin accepted —
// so any member could promote themselves to admin and take roster
// authority, invite issuance, and group deletion with it.
//
// Each direction is checked independently: it is not enough that the
// pair differ, each family must reject bytes signed for another.
func TestCrossTypeSignatureReplay(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	victim, _ := cipher.GenerateKeyPair()
	gid := uuid.New()
	now := time.Now().UTC()

	rm := RosterMutation{GroupID: gid, Op: RosterOpAdd, PeerPK: victim, IssuedAt: now}
	if err := SignRoster(&rm, sk); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}
	if err := VerifyRoster(rm); err != nil {
		t.Fatalf("roster mutation should verify against its own family: %v", err)
	}

	// roster-add → admin-promote
	am := AdminMutation{
		GroupID: rm.GroupID, Op: AdminOpPromote, PeerPK: rm.PeerPK,
		ParentSeq: rm.ParentSeq, IssuedAt: rm.IssuedAt,
		IssuerPK: rm.IssuerPK, Signature: rm.Signature,
	}
	if err := VerifyAdmin(am); err == nil {
		t.Fatal("PRIVILEGE ESCALATION: a roster-add signature verified as an admin-promote")
	}

	// roster-add → moderation-ban
	mm := ModerationMutation{
		GroupID: rm.GroupID, Op: ModOpBan, PeerPK: rm.PeerPK,
		ParentSeq: rm.ParentSeq, IssuedAt: rm.IssuedAt,
		IssuerPK: rm.IssuerPK, Signature: rm.Signature,
	}
	if err := VerifyMod(mm); err == nil {
		t.Fatal("a roster-add signature verified as a moderation ban")
	}

	// moderation-ban → roster-add, the reverse direction
	signedBan := ModerationMutation{GroupID: gid, Op: ModOpBan, PeerPK: victim, IssuedAt: now}
	if err := SignMod(&signedBan, sk); err != nil {
		t.Fatalf("SignMod: %v", err)
	}
	replayed := RosterMutation{
		GroupID: signedBan.GroupID, Op: RosterOpAdd, PeerPK: signedBan.PeerPK,
		ParentSeq: signedBan.ParentSeq, IssuedAt: signedBan.IssuedAt,
		IssuerPK: signedBan.IssuerPK, Signature: signedBan.Signature,
	}
	if err := VerifyRoster(replayed); err == nil {
		t.Fatal("a moderation-ban signature verified as a roster add")
	}
}

func TestSignVerifyModRoundTrip(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	peer, _ := cipher.GenerateKeyPair()
	gid := uuid.New()

	for _, op := range []ModOp{ModOpBan, ModOpUnban, ModOpMute, ModOpUnmute} {
		m := ModerationMutation{GroupID: gid, Op: op, PeerPK: peer, IssuedAt: time.Now().UTC()}
		if err := SignMod(&m, sk); err != nil {
			t.Fatalf("op %d: SignMod: %v", op, err)
		}
		if m.IssuerPK != pk {
			t.Errorf("op %d: IssuerPK = %s, want %s", op, m.IssuerPK, pk)
		}
		if err := VerifyMod(m); err != nil {
			t.Errorf("op %d: VerifyMod: %v", op, err)
		}
		// Wire round trip must preserve verifiability.
		body, err := MarshalMod(m)
		if err != nil {
			t.Fatalf("op %d: MarshalMod: %v", op, err)
		}
		got, err := UnmarshalMod(body)
		if err != nil {
			t.Fatalf("op %d: UnmarshalMod: %v", op, err)
		}
		if got.PeerPK != peer || got.Op != op {
			t.Errorf("op %d: round trip lost fields: %+v", op, got)
		}
	}
}

// Group-scoped ops carry no peer. The PeerPK field is inside the signed
// bytes, so tolerating a populated value would make two different
// envelopes — one naming a victim, one not — both valid read-only
// toggles. Reject rather than ignore.
func TestModGroupScopedOpsRejectPeerPK(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	peer, _ := cipher.GenerateKeyPair()
	gid := uuid.New()

	for _, op := range []ModOp{ModOpReadOnly, ModOpReadWrite} {
		ok := ModerationMutation{GroupID: gid, Op: op, IssuedAt: time.Now().UTC()}
		if err := SignMod(&ok, sk); err != nil {
			t.Errorf("op %d: group-scoped op with zero PeerPK should sign: %v", op, err)
		}
		if err := VerifyMod(ok); err != nil {
			t.Errorf("op %d: group-scoped op should verify: %v", op, err)
		}

		bad := ModerationMutation{GroupID: gid, Op: op, PeerPK: peer, IssuedAt: time.Now().UTC()}
		if err := SignMod(&bad, sk); !errors.Is(err, ErrGossipMissingFields) {
			t.Errorf("op %d: group-scoped op with a PeerPK should be refused, got %v", op, err)
		}
	}
}

// Peer-scoped ops require a peer, symmetrically.
func TestModPeerScopedOpsRequirePeerPK(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	m := ModerationMutation{GroupID: uuid.New(), Op: ModOpBan, IssuedAt: time.Now().UTC()}
	if err := SignMod(&m, sk); !errors.Is(err, ErrGossipMissingFields) {
		t.Errorf("ban with no PeerPK should be refused, got %v", err)
	}
}

func TestModUnknownOpRejected(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	peer, _ := cipher.GenerateKeyPair()
	m := ModerationMutation{GroupID: uuid.New(), Op: ModOp(99), PeerPK: peer, IssuedAt: time.Now().UTC()}
	if err := SignMod(&m, sk); !errors.Is(err, ErrGossipUnknownOp) {
		t.Errorf("SignMod with unknown op = %v, want ErrGossipUnknownOp", err)
	}
	if err := VerifyMod(ModerationMutation{
		GroupID: uuid.New(), Op: ModOp(99), PeerPK: peer,
		IssuedAt: time.Now().UTC(), IssuerPK: peer, Signature: cipher.Sig{1},
	}); !errors.Is(err, ErrGossipUnknownOp) {
		t.Errorf("VerifyMod with unknown op = %v, want ErrGossipUnknownOp", err)
	}
}

// Tampering with any signed field must invalidate the signature.
func TestModTamperDetected(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	peer, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	base := ModerationMutation{GroupID: uuid.New(), Op: ModOpMute, PeerPK: peer, IssuedAt: time.Now().UTC()}
	if err := SignMod(&base, sk); err != nil {
		t.Fatalf("SignMod: %v", err)
	}

	tests := map[string]func(m *ModerationMutation){
		"peer swapped":    func(m *ModerationMutation) { m.PeerPK = other },
		"op flipped":      func(m *ModerationMutation) { m.Op = ModOpBan },
		"group swapped":   func(m *ModerationMutation) { m.GroupID = uuid.New() },
		"time moved":      func(m *ModerationMutation) { m.IssuedAt = m.IssuedAt.Add(time.Second) },
		"parent seq bump": func(m *ModerationMutation) { m.ParentSeq++ },
		"issuer swapped":  func(m *ModerationMutation) { m.IssuerPK = other },
	}
	for name, mutate := range tests {
		m := base
		mutate(&m)
		if err := VerifyMod(m); err == nil {
			t.Errorf("%s: tampered mutation verified", name)
		}
	}
}
