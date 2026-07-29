// Package group cmd/apps/skychat/group/mod_enforcement_test.go c4-app-chat
// tests for reader-side moderation enforcement and the mod/ reconciler's
// authority gate.
package group

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// modSessionFixture builds a Session with no publisher (these tests
// exercise pure state transitions, not CXO writes) whose founder/admin
// is a distinct PK from the local one, so the authority gate is
// meaningful.
func modSessionFixture(t *testing.T) (*Session, cipher.SecKey, cipher.PubKey) {
	t.Helper()
	selfPK, _ := cipher.GenerateKeyPair()
	adminPK, adminSK := cipher.GenerateKeyPair()
	gid := uuid.NewString()
	s := &Session{
		cfg: Config{
			MyPK: selfPK,
			Record: Record{
				ID:      gid,
				OwnerPK: adminPK,
				Admins:  []cipher.PubKey{adminPK},
				Members: []cipher.PubKey{adminPK, selfPK},
				Kind:    KindPublic,
				Mode:    ModePublic,
			},
		},
		members: []cipher.PubKey{adminPK, selfPK},
		log:     logging.MustGetLogger("group.mod-enforcement-test"),
	}
	return s, adminSK, adminPK
}

func signedMod(t *testing.T, gid string, op ModOp, peer cipher.PubKey, sk cipher.SecKey, at time.Time) []byte {
	t.Helper()
	m := ModerationMutation{
		GroupID:  uuid.MustParse(gid),
		Op:       op,
		PeerPK:   peer,
		IssuedAt: at,
	}
	if err := SignMod(&m, sk); err != nil {
		t.Fatalf("SignMod: %v", err)
	}
	body, err := MarshalMod(m)
	if err != nil {
		t.Fatalf("MarshalMod: %v", err)
	}
	return body
}

// A mute must not rewrite history. Flipping this to retroactive would
// make a moderation action silently erase everything the member had
// already said — see Record.MutedSince.
func TestMuteIsForwardOnly(t *testing.T) {
	s, adminSK, _ := modSessionFixture(t)
	victim, _ := cipher.GenerateKeyPair()
	s.cfg.Record.Members = append(s.cfg.Record.Members, victim)
	s.members = append(s.members, victim)

	muteAt := time.Now().UTC()
	s.applyModLeaf(signedMod(t, s.cfg.Record.ID, ModOpMute, victim, adminSK, muteAt))

	if !s.cfg.Record.IsMuted(victim) {
		t.Fatal("mute was not applied")
	}
	before := muteAt.Add(-time.Hour)
	if ok, _ := s.senderAllowedToPost(victim, before); !ok {
		t.Error("a message sent BEFORE the mute was dropped; mutes must not rewrite history")
	}
	after := muteAt.Add(time.Second)
	if ok, _ := s.senderAllowedToPost(victim, after); ok {
		t.Error("a message sent AFTER the mute was rendered")
	}
}

// Read-only is the one most likely to be noticed if it were retroactive:
// flipping a busy group quiet would blank every message in it.
func TestReadOnlyIsForwardOnly(t *testing.T) {
	s, adminSK, _ := modSessionFixture(t)
	member, _ := cipher.GenerateKeyPair()
	s.cfg.Record.Members = append(s.cfg.Record.Members, member)

	roAt := time.Now().UTC()
	s.applyModLeaf(signedMod(t, s.cfg.Record.ID, ModOpReadOnly, cipher.PubKey{}, adminSK, roAt))
	if !s.cfg.Record.ReadOnly {
		t.Fatal("read-only was not applied")
	}

	if ok, _ := s.senderAllowedToPost(member, roAt.Add(-time.Hour)); !ok {
		t.Error("history vanished when the group went read-only")
	}
	if ok, _ := s.senderAllowedToPost(member, roAt.Add(time.Second)); ok {
		t.Error("a non-admin message after read-only was rendered")
	}
	// Admins are exempt — they are the ones who set it.
	if ok, why := s.senderAllowedToPost(s.cfg.Record.OwnerPK, roAt.Add(time.Second)); !ok {
		t.Errorf("admin blocked by read-only: %s", why)
	}

	// Lifting it restores posting for everyone.
	s.applyModLeaf(signedMod(t, s.cfg.Record.ID, ModOpReadWrite, cipher.PubKey{}, adminSK, roAt.Add(time.Minute)))
	if s.cfg.Record.ReadOnly {
		t.Fatal("read-write did not lift read-only")
	}
	if ok, _ := s.senderAllowedToPost(member, time.Now().UTC()); !ok {
		t.Error("member still blocked after read-only was lifted")
	}
}

// A ban is the deliberate exception to forward-only: banning means
// gone, and their leaves drop regardless of age.
func TestBanDropsHistoryAndRoster(t *testing.T) {
	s, adminSK, _ := modSessionFixture(t)
	victim, victimSK := cipher.GenerateKeyPair()
	_ = victimSK
	s.cfg.Record.Members = append(s.cfg.Record.Members, victim)
	s.cfg.Record.Admins = append(s.cfg.Record.Admins, victim) // also an admin
	s.members = append(s.members, victim)

	s.applyModLeaf(signedMod(t, s.cfg.Record.ID, ModOpBan, victim, adminSK, time.Now().UTC()))

	if !s.cfg.Record.IsBanned(victim) {
		t.Fatal("ban was not applied")
	}
	if containsPK(s.cfg.Record.Members, victim) {
		t.Error("banned PK kept its roster seat; the ban would not revoke read access")
	}
	// A banned PK retaining admin authority would let its own forged
	// mutations keep passing the authority gate.
	if containsPK(s.cfg.Record.Admins, victim) {
		t.Error("banned PK kept admin authority")
	}
	if ok, _ := s.senderAllowedToPost(victim, time.Now().Add(-24*time.Hour)); ok {
		t.Error("banned PK's older messages still render")
	}
}

// Unban must not re-admit. "You may ask again" is not "you're back in" —
// otherwise a ban/unban cycle would bypass the group's admission policy.
func TestUnbanDoesNotReadmit(t *testing.T) {
	s, adminSK, _ := modSessionFixture(t)
	victim, _ := cipher.GenerateKeyPair()
	s.cfg.Record.Members = append(s.cfg.Record.Members, victim)

	gid := s.cfg.Record.ID
	s.applyModLeaf(signedMod(t, gid, ModOpBan, victim, adminSK, time.Now().UTC()))
	s.applyModLeaf(signedMod(t, gid, ModOpUnban, victim, adminSK, time.Now().UTC()))

	if s.cfg.Record.IsBanned(victim) {
		t.Fatal("unban did not lift the ban")
	}
	if containsPK(s.cfg.Record.Members, victim) {
		t.Error("unban silently re-admitted the peer, bypassing the join gate")
	}
}

// The authority gate is what stops any member forging moderation.
func TestModLeafFromNonAdminRejected(t *testing.T) {
	s, _, _ := modSessionFixture(t)
	victim, _ := cipher.GenerateKeyPair()
	_, outsiderSK := cipher.GenerateKeyPair()

	s.applyModLeaf(signedMod(t, s.cfg.Record.ID, ModOpBan, victim, outsiderSK, time.Now().UTC()))

	if s.cfg.Record.IsBanned(victim) {
		t.Fatal("a non-admin's ban was applied — any member could evict anyone")
	}
}

// A leaf for another group must never apply here.
func TestModLeafWrongGroupIgnored(t *testing.T) {
	s, adminSK, _ := modSessionFixture(t)
	victim, _ := cipher.GenerateKeyPair()

	s.applyModLeaf(signedMod(t, uuid.NewString(), ModOpBan, victim, adminSK, time.Now().UTC()))

	if s.cfg.Record.IsBanned(victim) {
		t.Fatal("a mutation scoped to a different group was applied")
	}
}

// Founder immunity: a promoted admin banning or muting the founder
// would be a group-takeover primitive (promote self, silence founder,
// own the group). The founder is the record's recovery anchor.
func TestFounderCannotBeBannedOrMuted(t *testing.T) {
	s, _, founderPK := modSessionFixture(t)
	// A second admin, legitimately promoted, tries to take over.
	traitorPK, traitorSK := cipher.GenerateKeyPair()
	s.cfg.Record.Admins = append(s.cfg.Record.Admins, traitorPK)
	s.cfg.Record.Members = append(s.cfg.Record.Members, traitorPK)

	gid := s.cfg.Record.ID
	s.applyModLeaf(signedMod(t, gid, ModOpBan, founderPK, traitorSK, time.Now().UTC()))
	if s.cfg.Record.IsBanned(founderPK) {
		t.Fatal("founder was banned by another admin")
	}
	if !containsPK(s.cfg.Record.Members, founderPK) {
		t.Fatal("founder lost their roster seat")
	}

	s.applyModLeaf(signedMod(t, gid, ModOpMute, founderPK, traitorSK, time.Now().UTC()))
	if s.cfg.Record.IsMuted(founderPK) {
		t.Fatal("founder was muted by another admin")
	}
}

// Applying the same mutation twice must not double-append.
func TestModLeafIdempotent(t *testing.T) {
	s, adminSK, _ := modSessionFixture(t)
	victim, _ := cipher.GenerateKeyPair()
	s.cfg.Record.Members = append(s.cfg.Record.Members, victim)

	leaf := signedMod(t, s.cfg.Record.ID, ModOpMute, victim, adminSK, time.Now().UTC())
	s.applyModLeaf(leaf)
	s.applyModLeaf(leaf)

	if n := len(s.cfg.Record.Muted); n != 1 {
		t.Errorf("Muted has %d entries after a repeated mutation, want 1", n)
	}
}

// CanPostLocally is what the composer and Send consult; it must track
// the same state the reader gate does, or a member would be told they
// can speak into a room that drops everything they say.
func TestCanPostLocallyTracksMute(t *testing.T) {
	s, adminSK, _ := modSessionFixture(t)
	self := s.cfg.MyPK

	if ok, why := s.CanPostLocally(); !ok {
		t.Fatalf("unrestricted session cannot post: %s", why)
	}
	s.applyModLeaf(signedMod(t, s.cfg.Record.ID, ModOpMute, self, adminSK, time.Now().UTC()))
	ok, why := s.CanPostLocally()
	if ok {
		t.Fatal("muted session still reports it can post")
	}
	if why == "" {
		t.Error("no reason given for the block; the UI shows this verbatim")
	}
}

// Send must fail loudly rather than publishing into a feed where every
// reader drops it.
func TestSendRefusedWhenMuted(t *testing.T) {
	s, adminSK, _ := modSessionFixture(t)
	s.pub = newInProcessPublisher(t, mustSecKey(t))
	s.applyModLeaf(signedMod(t, s.cfg.Record.ID, ModOpMute, s.cfg.MyPK, adminSK, time.Now().UTC()))

	err := s.Send("hello")
	if err == nil {
		t.Fatal("Send succeeded while muted")
	}
	if !isPostNotPermitted(err) {
		t.Errorf("Send error = %v, want ErrPostNotPermitted", err)
	}
}

func mustSecKey(t *testing.T) cipher.SecKey {
	t.Helper()
	_, sk := cipher.GenerateKeyPair()
	return sk
}

func isPostNotPermitted(err error) bool {
	for e := err; e != nil; {
		if e == ErrPostNotPermitted {
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
