// Package group cmd/apps/skychat/group/replay_guard_test.go c4-app-chat
// tests for gossip freshness gating — the guard that stops a superseded
// mutation being replayed to resurrect state an admin already undid.
package group

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func signedRoster(t *testing.T, gid string, op RosterOp, peer cipher.PubKey, sk cipher.SecKey, at time.Time) []byte {
	t.Helper()
	m := RosterMutation{GroupID: uuid.MustParse(gid), Op: op, PeerPK: peer, IssuedAt: at}
	if err := SignRoster(&m, sk); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}
	body, err := MarshalRoster(m)
	if err != nil {
		t.Fatalf("MarshalRoster: %v", err)
	}
	return body
}

func signedAdmin(t *testing.T, gid string, op AdminOp, peer cipher.PubKey, sk cipher.SecKey, at time.Time) []byte {
	t.Helper()
	m := AdminMutation{GroupID: uuid.MustParse(gid), Op: op, PeerPK: peer, IssuedAt: at}
	if err := SignAdmin(&m, sk); err != nil {
		t.Fatalf("SignAdmin: %v", err)
	}
	body, err := MarshalAdmin(m)
	if err != nil {
		t.Fatalf("MarshalAdmin: %v", err)
	}
	return body
}

// A watermark that reaches the store must survive every other write to
// the record.
//
// Manager ops are all read-modify-Put, and the reconciler advances
// watermarks in the background between the read and the Put. A Put that
// replaced the map wholesale would silently drop whatever the reconciler
// pinned in that window — and a dropped watermark never comes back, so
// the next replayed RosterOpAdd for that target sails through the
// freshness gate. That is the original bug, resurfacing through the
// persistence layer instead of the wire.
func TestStorePutDoesNotWalkWatermarksBackwards(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close() //nolint:errcheck

	id := uuid.NewString()
	evicted, other := pkN(1), pkN(2)
	base := Record{
		ID: id, OwnerPK: pkN(3), Port: 40011,
		Mode: ModePublic, Kind: KindPublic, Role: RoleOwner, Status: StatusActive,
	}
	if err := st.Put(base); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// A stale read, taken before the reconciler acts.
	stale, _, err := st.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The reconciler applies an eviction and persists the watermark.
	pinned := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	evictedKey := watermarkKey(familyRoster, evicted)
	if err := st.SetMutationSeen(id, map[string]time.Time{evictedKey: pinned}); err != nil {
		t.Fatalf("SetMutationSeen: %v", err)
	}

	// Now an admin op lands, built on the stale read: it adds a member and
	// writes the whole record back, watermarks and all.
	stale.Members = append(stale.Members, other)
	if err := st.Put(stale); err != nil {
		t.Fatalf("Put (stale): %v", err)
	}

	got, ok, err := st.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: err=%v ok=%v", err, ok)
	}
	if !containsPK(got.Members, other) {
		t.Error("the admin's roster change was lost")
	}
	at, held := got.MutationSeen[evictedKey]
	if !held {
		t.Fatal("the eviction watermark was dropped — a replayed RosterOpAdd would now be accepted")
	}
	if !at.Equal(pinned) {
		t.Errorf("watermark = %v, want %v", at, pinned)
	}

	// A genuinely newer watermark carried by a Put still advances.
	later := pinned.Add(time.Minute)
	got.MutationSeen[evictedKey] = later
	if err := st.Put(got); err != nil {
		t.Fatalf("Put (advance): %v", err)
	}
	again, _, err := st.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if at := again.MutationSeen[evictedKey]; !at.Equal(later) {
		t.Errorf("watermark = %v, want the newer %v", at, later)
	}
}

// guardSession builds a member-role session whose admin is a separate
// PK, with no publisher (these are pure state transitions).
func guardSession(t *testing.T) (*Session, cipher.SecKey, cipher.PubKey) {
	t.Helper()
	selfPK, _ := cipher.GenerateKeyPair()
	adminPK, adminSK := cipher.GenerateKeyPair()
	s := &Session{
		cfg: Config{
			MyPK: selfPK,
			Record: Record{
				ID:      uuid.NewString(),
				OwnerPK: adminPK,
				Admins:  []cipher.PubKey{adminPK},
				Members: []cipher.PubKey{adminPK, selfPK},
				Kind:    KindPublic,
				Mode:    ModePublic,
			},
		},
		members: []cipher.PubKey{adminPK, selfPK},
		log:     logging.MustGetLogger("group.replay-guard-test"),
	}
	return s, adminSK, adminPK
}

// The headline case: a member copies an admin's old "add X" leaf onto
// their own feed after X was evicted. Signature is genuine and the
// issuer really is an admin, so only freshness can stop it.
func TestReplayedRosterAddCannotResurrectEvictedMember(t *testing.T) {
	s, adminSK, _ := guardSession(t)
	victim, _ := cipher.GenerateKeyPair()
	gid := s.cfg.Record.ID

	addAt := time.Now().UTC().Add(-time.Hour)
	removeAt := addAt.Add(30 * time.Minute)

	addLeaf := signedRoster(t, gid, RosterOpAdd, victim, adminSK, addAt)
	s.applyRosterLeaf(addLeaf)
	if !containsPK(s.cfg.Record.Members, victim) {
		t.Fatal("setup: the add did not apply")
	}

	s.applyRosterLeaf(signedRoster(t, gid, RosterOpRemove, victim, adminSK, removeAt))
	if containsPK(s.cfg.Record.Members, victim) {
		t.Fatal("setup: the remove did not apply")
	}

	// Replay the original, still-valid add.
	s.applyRosterLeaf(addLeaf)
	if containsPK(s.cfg.Record.Members, victim) {
		t.Fatal("REPLAY: an evicted member was resurrected by replaying the original add leaf")
	}
}

// Same shape for admin authority: a demoted admin must not be restored
// by replaying the promotion that originally granted it.
func TestReplayedPromoteCannotRestoreDemotedAdmin(t *testing.T) {
	s, adminSK, _ := guardSession(t)
	peer, _ := cipher.GenerateKeyPair()
	gid := s.cfg.Record.ID
	s.cfg.Record.Members = append(s.cfg.Record.Members, peer)

	promoteAt := time.Now().UTC().Add(-time.Hour)
	promoteLeaf := signedAdmin(t, gid, AdminOpPromote, peer, adminSK, promoteAt)

	s.applyAdminLeaf(promoteLeaf)
	if !s.cfg.Record.IsAdmin(peer) {
		t.Fatal("setup: the promote did not apply")
	}
	s.applyAdminLeaf(signedAdmin(t, gid, AdminOpDemote, peer, adminSK, promoteAt.Add(time.Minute)))
	if s.cfg.Record.IsAdmin(peer) {
		t.Fatal("setup: the demote did not apply")
	}

	s.applyAdminLeaf(promoteLeaf)
	if s.cfg.Record.IsAdmin(peer) {
		t.Fatal("REPLAY: a demoted admin regained authority by replaying the original promote")
	}
}

// And for moderation: replaying an old unban must not lift a standing
// ban.
func TestReplayedUnbanCannotLiftStandingBan(t *testing.T) {
	s, adminSK, _ := guardSession(t)
	victim, _ := cipher.GenerateKeyPair()
	gid := s.cfg.Record.ID
	s.cfg.Record.Members = append(s.cfg.Record.Members, victim)

	base := time.Now().UTC().Add(-time.Hour)
	s.applyModLeaf(signedMod(t, gid, ModOpBan, victim, adminSK, base))
	unbanLeaf := signedMod(t, gid, ModOpUnban, victim, adminSK, base.Add(time.Minute))
	s.applyModLeaf(unbanLeaf)
	if s.cfg.Record.IsBanned(victim) {
		t.Fatal("setup: the unban did not apply")
	}
	// Banned again, later.
	s.applyModLeaf(signedMod(t, gid, ModOpBan, victim, adminSK, base.Add(2*time.Minute)))
	if !s.cfg.Record.IsBanned(victim) {
		t.Fatal("setup: the re-ban did not apply")
	}

	s.applyModLeaf(unbanLeaf)
	if !s.cfg.Record.IsBanned(victim) {
		t.Fatal("REPLAY: a standing ban was lifted by replaying an older unban")
	}
}

// Out-of-order arrival must converge to the newest mutation regardless
// of which order the leaves turn up in. This is the same mechanism as
// the replay defence, and it fixes a genuine convergence bug: a joiner
// replaying feed history has no ordering guarantee across peers.
func TestOutOfOrderRosterLeavesConverge(t *testing.T) {
	victim, _ := cipher.GenerateKeyPair()
	base := time.Now().UTC().Add(-time.Hour)

	// remove-then-add (reverse of causal order) must still end removed.
	s, adminSK, _ := guardSession(t)
	gid := s.cfg.Record.ID
	addLeaf := signedRoster(t, gid, RosterOpAdd, victim, adminSK, base)
	removeLeaf := signedRoster(t, gid, RosterOpRemove, victim, adminSK, base.Add(time.Minute))

	s.applyRosterLeaf(removeLeaf)
	s.applyRosterLeaf(addLeaf)
	if containsPK(s.cfg.Record.Members, victim) {
		t.Error("reverse-order delivery converged to the OLDER state")
	}

	// And the forward order agrees.
	s2, adminSK2, _ := guardSession(t)
	gid2 := s2.cfg.Record.ID
	s2.applyRosterLeaf(signedRoster(t, gid2, RosterOpAdd, victim, adminSK2, base))
	s2.applyRosterLeaf(signedRoster(t, gid2, RosterOpRemove, victim, adminSK2, base.Add(time.Minute)))
	if containsPK(s2.cfg.Record.Members, victim) {
		t.Error("forward-order delivery did not converge to removed")
	}
}

// A banned PK must never be re-added, independent of the watermark —
// ban is the strongest revocation the group has and shouldn't depend on
// watermark bookkeeping surviving.
func TestRosterAddRefusedForBannedPeer(t *testing.T) {
	s, adminSK, _ := guardSession(t)
	victim, _ := cipher.GenerateKeyPair()
	gid := s.cfg.Record.ID

	s.applyModLeaf(signedMod(t, gid, ModOpBan, victim, adminSK, time.Now().UTC().Add(-time.Hour)))
	if !s.cfg.Record.IsBanned(victim) {
		t.Fatal("setup: ban did not apply")
	}

	// A brand-new, perfectly fresh add for the banned PK.
	s.applyRosterLeaf(signedRoster(t, gid, RosterOpAdd, victim, adminSK, time.Now().UTC()))
	if containsPK(s.cfg.Record.Members, victim) {
		t.Fatal("a banned peer was added back to the roster")
	}
}

// A future-dated mutation would pin the watermark and lock the target
// out of every later update — a roster denial-of-service costing one
// leaf. maxMutationSkew bounds it.
func TestFutureDatedMutationRefused(t *testing.T) {
	s, adminSK, _ := guardSession(t)
	victim, _ := cipher.GenerateKeyPair()
	gid := s.cfg.Record.ID

	far := time.Now().UTC().Add(365 * 24 * time.Hour)
	s.applyRosterLeaf(signedRoster(t, gid, RosterOpAdd, victim, adminSK, far))
	if containsPK(s.cfg.Record.Members, victim) {
		t.Fatal("a mutation dated a year ahead was applied")
	}

	// The watermark must be untouched, so a legitimate add still works.
	s.applyRosterLeaf(signedRoster(t, gid, RosterOpAdd, victim, adminSK, time.Now().UTC()))
	if !containsPK(s.cfg.Record.Members, victim) {
		t.Fatal("a future-dated leaf poisoned the watermark and locked out legitimate updates")
	}
}

// Modest skew is tolerated — visors don't share a clock.
func TestSmallClockSkewTolerated(t *testing.T) {
	s, adminSK, _ := guardSession(t)
	victim, _ := cipher.GenerateKeyPair()
	near := time.Now().UTC().Add(maxMutationSkew / 2)
	s.applyRosterLeaf(signedRoster(t, s.cfg.Record.ID, RosterOpAdd, victim, adminSK, near))
	if !containsPK(s.cfg.Record.Members, victim) {
		t.Error("a mutation within the allowed skew was refused")
	}
}

// A restarting admin re-asserts its roster on every session open. Those
// echoes must be dated by when the state was learned, not by "now" —
// otherwise the restart out-ranks another admin's genuine newer
// decisions, silently re-adding evicted members and (worse) pinning the
// watermark so the real eviction is rejected when it arrives.
func TestReassertionUsesHistoricalTimestamps(t *testing.T) {
	s, adminSK, _ := guardSession(t)
	victim, _ := cipher.GenerateKeyPair()
	gid := s.cfg.Record.ID

	learnedAt := time.Now().UTC().Add(-2 * time.Hour)
	s.applyRosterLeaf(signedRoster(t, gid, RosterOpAdd, victim, adminSK, learnedAt))
	if !containsPK(s.cfg.Record.Members, victim) {
		t.Fatal("setup: add did not apply")
	}

	// What a re-assertion of this member would be stamped with.
	s.membersMu.RLock()
	at := s.assertionTimeLocked(familyRoster, victim)
	s.membersMu.RUnlock()
	if !at.Equal(learnedAt) {
		t.Fatalf("re-assertion time = %v, want the time we learned it (%v)", at, learnedAt)
	}

	// A removal issued AFTER we learned the add, but BEFORE our restart,
	// must still win.
	removeAt := learnedAt.Add(time.Minute)
	s.applyRosterLeaf(signedRoster(t, gid, RosterOpRemove, victim, adminSK, removeAt))
	if containsPK(s.cfg.Record.Members, victim) {
		t.Fatal("a genuine removal was rejected because a re-assertion had pinned the watermark")
	}
}

// A member with no recorded mutation (added at create time, so no
// roster leaf ever existed) re-asserts at the group's creation time
// rather than now.
func TestReassertionFallsBackToCreatedAt(t *testing.T) {
	s, _, _ := guardSession(t)
	created := time.Now().UTC().Add(-24 * time.Hour)
	s.cfg.Record.CreatedAt = created
	peer, _ := cipher.GenerateKeyPair()

	s.membersMu.RLock()
	at := s.assertionTimeLocked(familyRoster, peer)
	s.membersMu.RUnlock()
	if !at.Equal(created) {
		t.Errorf("fallback re-assertion time = %v, want CreatedAt %v", at, created)
	}
}

// recordMutation must be monotonic, or the re-assertion path (which
// deliberately writes historical timestamps) could walk a watermark
// backwards and re-open the replay window.
func TestRecordMutationIsMonotonic(t *testing.T) {
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	seen := recordMutation(nil, "r:a", base)
	seen = recordMutation(seen, "r:a", base.Add(-time.Hour))
	if !seen["r:a"].Equal(base) {
		t.Errorf("watermark moved backwards to %v, want %v", seen["r:a"], base)
	}
	seen = recordMutation(seen, "r:a", base.Add(time.Hour))
	if !seen["r:a"].Equal(base.Add(time.Hour)) {
		t.Errorf("watermark did not advance: %v", seen["r:a"])
	}
}

func TestMutationFreshRules(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	key := "r:abc"

	if ok, _ := mutationFresh(nil, key, now, now); !ok {
		t.Error("first mutation for a target should be fresh")
	}
	if ok, why := mutationFresh(nil, key, time.Time{}, now); ok {
		t.Errorf("a zero IssuedAt should be refused (got ok, %q)", why)
	}
	seen := map[string]time.Time{key: now}
	if ok, _ := mutationFresh(seen, key, now.Add(time.Second), now); !ok {
		t.Error("a newer mutation should be fresh")
	}
	if ok, _ := mutationFresh(seen, key, now.Add(-time.Second), now); ok {
		t.Error("an older mutation should be refused")
	}
	// Exactly equal means the same leaf arriving again.
	if ok, _ := mutationFresh(seen, key, now, now); ok {
		t.Error("an equal-timestamp mutation should be refused as a re-delivery")
	}
	if ok, _ := mutationFresh(seen, key, now.Add(2*maxMutationSkew), now); ok {
		t.Error("a far-future mutation should be refused")
	}
	// A different target is unaffected by this one's watermark.
	if ok, _ := mutationFresh(seen, "r:other", now.Add(-time.Hour), now); !ok {
		t.Error("a watermark leaked across targets")
	}
}

// Watermarks are keyed per family, so a roster action on a PK must not
// suppress an admin or moderation action on the same PK.
func TestWatermarkKeysAreFamilyScoped(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	r := watermarkKey(familyRoster, pk)
	a := watermarkKey(familyAdmin, pk)
	m := watermarkKey(familyMod, pk)
	if r == a || a == m || r == m {
		t.Fatalf("family keys collide: %q %q %q", r, a, m)
	}
	// Group-scoped ops share one key that cannot collide with a PK.
	g := watermarkKey(familyMod, cipher.PubKey{})
	if g == m {
		t.Error("group scope collides with a peer key")
	}
}

// The store must never move a watermark backwards, because reconciler
// paths persist interleaved snapshots.
func TestMergeWatermarksKeepsLatest(t *testing.T) {
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	older := map[string]time.Time{"r:a": base}
	newer := map[string]time.Time{"r:a": base.Add(time.Hour), "r:b": base}

	got := mergeWatermarks(newer, older)
	if !got["r:a"].Equal(base.Add(time.Hour)) {
		t.Errorf("merge moved a watermark backwards: %v", got["r:a"])
	}
	if !got["r:b"].Equal(base) {
		t.Errorf("merge dropped a key: %v", got)
	}
	if got := mergeWatermarks(nil, nil); got != nil {
		t.Errorf("empty merge should stay nil, got %v", got)
	}
}
