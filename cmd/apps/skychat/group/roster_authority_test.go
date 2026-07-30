// Package group cmd/apps/skychat/group/roster_authority_test.go
//
// Unit coverage for the two Session methods that reshape who this visor
// subscribes to: SetAllowlist (owner-role membership changes) and
// SetAdminRoster (admin promotions/demotions under the admin-aggregator
// topology, #2685). Both were at ~5% — only their guard clauses were reached.
//
// These are authority gates, so the assertions are about what happens to the
// per-peer CXO subscriptions, not just the return value:
//
//   - a removed member's subscriber is CLOSED and dropped, and reported in the
//     returned evicted slice (the Manager keys per-peer reconnect backoff on
//     that, so a missed eviction leaves stale backoff suppressing a later
//     re-add);
//   - a DEMOTED admin's subscription is torn down — a non-admin must not keep
//     reading a feed it is no longer entitled to follow;
//   - the two bookkeeping maps (peerLastInboundNs, peerUpdateCount) are
//     cleaned alongside peerSubs, since a leak there feeds the staleness
//     detector bad data.
//
// Fixture: an in-memory CXO publisher (newInProcessPublisher, no network) with
// DmsgC nil and PeerAddrs empty, so connectSub takes the native-TCP branch and
// fails instantly on "no TCP address configured" rather than burning the 15s
// reconnectAttemptTimeout per peer. The add path still installs the
// subscriber, which is what these tests check.
package group

import (
	"sort"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
)

// newRosterSession builds a Session on an in-memory publisher with the
// per-peer maps initialized, ready for SetAllowlist / SetAdminRoster.
func newRosterSession(t *testing.T, myPK cipher.PubKey, mySK cipher.SecKey, rec Record) *Session {
	t.Helper()
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	return &Session{
		// PeerFanout -1 opts this fixture out of the peer-backfill fanout,
		// so these tests keep asserting the ADMIN-ROSTER rule in isolation:
		// "who do I follow because of who is an admin", with no ring
		// neighbors mixed in. The fanout itself is covered in
		// nonadmin_sub_topology_test.go.
		cfg: Config{MyPK: myPK, MySK: mySK, Record: rec, PeerFanout: -1},
		pub: newInProcessPublisher(t, mySK),
		log: logging.MustGetLogger("group.roster-authority-test"),

		peerSubs:          map[cipher.PubKey]*treestore.Subscriber{},
		peerLastInboundNs: map[cipher.PubKey]*atomic.Int64{},
		peerUpdateCount:   map[cipher.PubKey]*atomic.Uint64{},
	}
}

// peerSubPKs returns the session's current per-peer subscription keys, sorted
// for comparison.
func peerSubPKs(s *Session) []cipher.PubKey {
	s.peerSubsMu.RLock()
	defer s.peerSubsMu.RUnlock()
	out := make([]cipher.PubKey, 0, len(s.peerSubs))
	for pk := range s.peerSubs {
		out = append(out, pk)
	}
	sortPKs(out)
	return out
}

func sortPKs(pks []cipher.PubKey) {
	sort.Slice(pks, func(i, j int) bool { return pks[i].Hex() < pks[j].Hex() })
}

// assertPKSet compares two PK sets order-insensitively.
func assertPKSet(t *testing.T, what string, got, want []cipher.PubKey) {
	t.Helper()
	g := append([]cipher.PubKey(nil), got...)
	w := append([]cipher.PubKey(nil), want...)
	sortPKs(g)
	sortPKs(w)
	if len(g) != len(w) {
		t.Errorf("%s = %v (%d), want %v (%d)", what, hexes(g), len(g), hexes(w), len(w))
		return
	}
	for i := range g {
		if g[i] != w[i] {
			t.Errorf("%s = %v, want %v", what, hexes(g), hexes(w))
			return
		}
	}
}

func hexes(pks []cipher.PubKey) []string {
	out := make([]string, len(pks))
	for i, pk := range pks {
		out[i] = pk.Hex()[:8]
	}
	return out
}

// assertBookkeepingMatchesSubs pins the invariant that the two per-peer
// counters track peerSubs exactly — a leak on either side feeds the staleness
// detector entries for peers that no longer exist.
func assertBookkeepingMatchesSubs(t *testing.T, s *Session) {
	t.Helper()
	s.peerSubsMu.RLock()
	defer s.peerSubsMu.RUnlock()
	for pk := range s.peerSubs {
		if s.peerLastInboundNs[pk] == nil {
			t.Errorf("peer %s has a sub but no peerLastInboundNs entry", pk.Hex()[:8])
		}
		if s.peerUpdateCount[pk] == nil {
			t.Errorf("peer %s has a sub but no peerUpdateCount entry", pk.Hex()[:8])
		}
	}
	if len(s.peerLastInboundNs) != len(s.peerSubs) {
		t.Errorf("peerLastInboundNs has %d entries, want %d (one per sub) — evictions leaked",
			len(s.peerLastInboundNs), len(s.peerSubs))
	}
	if len(s.peerUpdateCount) != len(s.peerSubs) {
		t.Errorf("peerUpdateCount has %d entries, want %d (one per sub) — evictions leaked",
			len(s.peerUpdateCount), len(s.peerSubs))
	}
}

// --- SetAllowlist -----------------------------------------------------------

// A member session DOES have an allowlist — its own publisher's, which
// gates who may follow its feed — and it has to track roster changes. It
// used to be refused here, which froze the list at Open and made a peer
// added afterwards permanently unable to subscribe to this member.
//
// What stays owner-only is the follow-everyone peer-sub reconciliation, so
// a member returns no evictions.
func TestSetAllowlist_MemberRefreshesAllowlistButNotPeerSubs(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	late, _ := cipher.GenerateKeyPair()
	s := newRosterSession(t, me, sk, Record{OwnerPK: other, Role: RoleMember})

	evicted, err := s.SetAllowlist([]cipher.PubKey{me, other, late})
	if err != nil {
		t.Fatalf("SetAllowlist on a member session: %v", err)
	}
	if evicted != nil {
		t.Errorf("evicted = %v, want nil — members don't run the owner's peer-sub rule", hexes(evicted))
	}
	// The relay gate's view of membership tracked the change.
	if !s.isMember(late) {
		t.Error("a member added after Open is still missing from the live member set")
	}
	// And no peer subs were opened by this call.
	if got := peerSubPKs(s); len(got) != 0 {
		t.Errorf("peerSubs = %v, want none from a member-role SetAllowlist", hexes(got))
	}
}

func TestSetAllowlist_RejectsNilPublisher(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	s := &Session{cfg: Config{MyPK: me, MySK: sk, Record: Record{OwnerPK: me, Role: RoleOwner}}}

	if _, err := s.SetAllowlist([]cipher.PubKey{me}); err == nil {
		t.Fatal("a session with no publisher cannot set an allowlist; want an error")
	}
}

func TestSetAllowlist_AddsPeerSubsExcludingSelf(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	s := newRosterSession(t, me, sk, Record{OwnerPK: me, Role: RoleOwner})

	evicted, err := s.SetAllowlist([]cipher.PubKey{me, a, b})
	if err != nil {
		t.Fatalf("SetAllowlist: %v", err)
	}
	if len(evicted) != 0 {
		t.Errorf("evicted = %v, want none on a first allowlist", hexes(evicted))
	}
	// Self is excluded — a visor does not subscribe to its own feed.
	assertPKSet(t, "peerSubs", peerSubPKs(s), []cipher.PubKey{a, b})
	assertBookkeepingMatchesSubs(t, s)

	// The members snapshot keeps the full list, self included.
	s.membersMu.RLock()
	got := append([]cipher.PubKey(nil), s.members...)
	s.membersMu.RUnlock()
	assertPKSet(t, "members snapshot", got, []cipher.PubKey{me, a, b})
}

func TestSetAllowlist_EvictsRemovedMembers(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	s := newRosterSession(t, me, sk, Record{OwnerPK: me, Role: RoleOwner})

	if _, err := s.SetAllowlist([]cipher.PubKey{me, a, b}); err != nil {
		t.Fatalf("seed SetAllowlist: %v", err)
	}

	// Remove b.
	evicted, err := s.SetAllowlist([]cipher.PubKey{me, a})
	if err != nil {
		t.Fatalf("SetAllowlist: %v", err)
	}
	assertPKSet(t, "evicted", evicted, []cipher.PubKey{b})
	assertPKSet(t, "peerSubs", peerSubPKs(s), []cipher.PubKey{a})
	assertBookkeepingMatchesSubs(t, s)
}

func TestSetAllowlist_EmptyListEvictsEveryone(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	s := newRosterSession(t, me, sk, Record{OwnerPK: me, Role: RoleOwner})

	if _, err := s.SetAllowlist([]cipher.PubKey{me, a, b}); err != nil {
		t.Fatalf("seed SetAllowlist: %v", err)
	}
	evicted, err := s.SetAllowlist(nil)
	if err != nil {
		t.Fatalf("SetAllowlist(nil): %v", err)
	}
	assertPKSet(t, "evicted", evicted, []cipher.PubKey{a, b})
	if got := peerSubPKs(s); len(got) != 0 {
		t.Errorf("peerSubs = %v, want empty", hexes(got))
	}
	assertBookkeepingMatchesSubs(t, s)
}

// TestSetAllowlist_ReusesExistingSubs pins the `if _, exists := ...; continue`
// arm: re-applying the same roster must not tear down and rebuild live
// subscriptions, which would drop their connections for no reason.
func TestSetAllowlist_ReusesExistingSubs(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	s := newRosterSession(t, me, sk, Record{OwnerPK: me, Role: RoleOwner})

	if _, err := s.SetAllowlist([]cipher.PubKey{me, a}); err != nil {
		t.Fatalf("seed SetAllowlist: %v", err)
	}
	s.peerSubsMu.RLock()
	first := s.peerSubs[a]
	s.peerSubsMu.RUnlock()
	if first == nil {
		t.Fatal("precondition: expected a sub for a")
	}

	evicted, err := s.SetAllowlist([]cipher.PubKey{me, a})
	if err != nil {
		t.Fatalf("SetAllowlist: %v", err)
	}
	if len(evicted) != 0 {
		t.Errorf("evicted = %v, want none — the roster did not change", hexes(evicted))
	}
	s.peerSubsMu.RLock()
	second := s.peerSubs[a]
	s.peerSubsMu.RUnlock()
	if first != second {
		t.Error("re-applying an unchanged roster replaced a live subscriber")
	}
}

// TestSetAllowlist_SnapshotsCallerSlice — the caller's slice must not alias
// session state, or a later mutation on their side would silently reshape the
// roster (and the publisher's allowlist) without going through this gate.
func TestSetAllowlist_SnapshotsCallerSlice(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	s := newRosterSession(t, me, sk, Record{OwnerPK: me, Role: RoleOwner})

	members := []cipher.PubKey{me, a}
	if _, err := s.SetAllowlist(members); err != nil {
		t.Fatalf("SetAllowlist: %v", err)
	}
	members[1] = b // caller mutates their slice afterwards

	s.membersMu.RLock()
	got := append([]cipher.PubKey(nil), s.members...)
	s.membersMu.RUnlock()
	assertPKSet(t, "members after the caller mutated its slice", got, []cipher.PubKey{me, a})
}

// TestSetAllowlist_EvictionKeepsTheSharedNodeAlive — the per-peer subscribers
// run on the publisher's CXO node. Closing one on eviction must not take the
// node (and therefore the whole session) down with it.
func TestSetAllowlist_EvictionKeepsTheSharedNodeAlive(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	s := newRosterSession(t, me, sk, Record{OwnerPK: me, Role: RoleOwner})

	if _, err := s.SetAllowlist([]cipher.PubKey{me, a}); err != nil {
		t.Fatalf("seed SetAllowlist: %v", err)
	}
	if _, err := s.SetAllowlist([]cipher.PubKey{me}); err != nil {
		t.Fatalf("evicting SetAllowlist: %v", err)
	}

	// The node still serves: a fresh subscriber can be created on it.
	other, _ := cipher.GenerateKeyPair()
	sub, err := treestore.NewSubscriberOnNode(s.pub.Node(), other, treestore.SubConfig{Logger: s.log})
	if err != nil {
		t.Fatalf("the shared CXO node did not survive an eviction: %v", err)
	}
	_ = sub.Close() //nolint:errcheck
}

// --- SetAdminRoster ---------------------------------------------------------

func TestSetAdminRoster_RejectsNilSessionOrPublisher(t *testing.T) {
	var nilSession *Session
	if _, err := nilSession.SetAdminRoster(nil); err == nil {
		t.Error("a nil session must return an error, not panic")
	}

	me, sk := cipher.GenerateKeyPair()
	s := &Session{cfg: Config{MyPK: me, MySK: sk, Record: Record{OwnerPK: me}}}
	if _, err := s.SetAdminRoster(nil); err == nil {
		t.Error("a session with no publisher must return an error")
	}
}

// TestSetAdminRoster_AdminFollowsEveryMember — an admin is an aggregator: it
// subscribes to every other member's feed so it can republish.
func TestSetAdminRoster_AdminFollowsEveryMember(t *testing.T) {
	me, sk := cipher.GenerateKeyPair() // me == an admin, not the founder
	owner, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	m, _ := cipher.GenerateKeyPair()

	s := newRosterSession(t, me, sk, Record{
		OwnerPK: owner,
		Role:    RoleMember,
		Members: []cipher.PubKey{owner, me, b, m},
	})

	evicted, err := s.SetAdminRoster([]cipher.PubKey{me})
	if err != nil {
		t.Fatalf("SetAdminRoster: %v", err)
	}
	if len(evicted) != 0 {
		t.Errorf("evicted = %v, want none", hexes(evicted))
	}
	assertPKSet(t, "peerSubs for an admin", peerSubPKs(s), []cipher.PubKey{owner, b, m})
	assertBookkeepingMatchesSubs(t, s)
}

// TestSetAdminRoster_NonAdminFollowsOnlyAdmins — a plain member follows only
// admin feeds (the founder counts as an admin implicitly). Subscribing to a
// non-admin peer would be redundant work at best.
func TestSetAdminRoster_NonAdminFollowsOnlyAdmins(t *testing.T) {
	me, sk := cipher.GenerateKeyPair() // plain member
	owner, _ := cipher.GenerateKeyPair()
	adminA, _ := cipher.GenerateKeyPair()
	plainB, _ := cipher.GenerateKeyPair()

	s := newRosterSession(t, me, sk, Record{
		OwnerPK: owner,
		Role:    RoleMember,
		Members: []cipher.PubKey{owner, adminA, plainB, me},
	})

	if _, err := s.SetAdminRoster([]cipher.PubKey{adminA}); err != nil {
		t.Fatalf("SetAdminRoster: %v", err)
	}
	// owner is implicitly an admin (founder); plainB is not.
	assertPKSet(t, "peerSubs for a non-admin", peerSubPKs(s), []cipher.PubKey{owner, adminA})
	assertBookkeepingMatchesSubs(t, s)
}

// TestSetAdminRoster_DemotionEvictsTheDemotedAdminsSub is the authority gate
// that matters: once a peer loses admin status, this visor must stop following
// its feed.
func TestSetAdminRoster_DemotionEvictsTheDemotedAdminsSub(t *testing.T) {
	me, sk := cipher.GenerateKeyPair() // plain member
	owner, _ := cipher.GenerateKeyPair()
	adminA, _ := cipher.GenerateKeyPair()
	plainB, _ := cipher.GenerateKeyPair()

	s := newRosterSession(t, me, sk, Record{
		OwnerPK: owner,
		Role:    RoleMember,
		Members: []cipher.PubKey{owner, adminA, plainB, me},
	})

	if _, err := s.SetAdminRoster([]cipher.PubKey{adminA}); err != nil {
		t.Fatalf("seed SetAdminRoster: %v", err)
	}
	assertPKSet(t, "peerSubs before demotion", peerSubPKs(s), []cipher.PubKey{owner, adminA})

	// Demote adminA. The founder stays an admin via IsAdmin's union.
	evicted, err := s.SetAdminRoster(nil)
	if err != nil {
		t.Fatalf("demoting SetAdminRoster: %v", err)
	}
	assertPKSet(t, "evicted on demotion", evicted, []cipher.PubKey{adminA})
	assertPKSet(t, "peerSubs after demotion", peerSubPKs(s), []cipher.PubKey{owner})
	assertBookkeepingMatchesSubs(t, s)
}

// TestSetAdminRoster_PromotionOfSelfWidensTheSubSet — being promoted turns
// this visor into an aggregator, so it must pick up the members it previously
// had no reason to follow.
func TestSetAdminRoster_PromotionOfSelfWidensTheSubSet(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	owner, _ := cipher.GenerateKeyPair()
	adminA, _ := cipher.GenerateKeyPair()
	plainB, _ := cipher.GenerateKeyPair()

	s := newRosterSession(t, me, sk, Record{
		OwnerPK: owner,
		Role:    RoleMember,
		Members: []cipher.PubKey{owner, adminA, plainB, me},
	})

	if _, err := s.SetAdminRoster([]cipher.PubKey{adminA}); err != nil {
		t.Fatalf("seed SetAdminRoster: %v", err)
	}
	assertPKSet(t, "peerSubs as a plain member", peerSubPKs(s), []cipher.PubKey{owner, adminA})

	// Promote self.
	evicted, err := s.SetAdminRoster([]cipher.PubKey{adminA, me})
	if err != nil {
		t.Fatalf("promoting SetAdminRoster: %v", err)
	}
	if len(evicted) != 0 {
		t.Errorf("evicted = %v, want none — a promotion only widens the set", hexes(evicted))
	}
	assertPKSet(t, "peerSubs as an admin", peerSubPKs(s), []cipher.PubKey{owner, adminA, plainB})
	assertBookkeepingMatchesSubs(t, s)
}

// TestSetAdminRoster_CachesSnapshotOnRecord — the new admin set is written
// back to cfg.Record.Admins so IsAdmin checks elsewhere (publish path, roster
// reconcile) read the same view, and it must be a defensive copy.
func TestSetAdminRoster_CachesSnapshotOnRecord(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	owner, _ := cipher.GenerateKeyPair()
	adminA, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	s := newRosterSession(t, me, sk, Record{
		OwnerPK: owner,
		Role:    RoleMember,
		Members: []cipher.PubKey{owner, adminA, me},
	})

	admins := []cipher.PubKey{adminA}
	if _, err := s.SetAdminRoster(admins); err != nil {
		t.Fatalf("SetAdminRoster: %v", err)
	}
	assertPKSet(t, "cached Record.Admins", s.cfg.Record.Admins, []cipher.PubKey{adminA})
	if !s.cfg.Record.IsAdmin(adminA) {
		t.Error("IsAdmin should see the newly cached admin")
	}

	admins[0] = other // caller mutates afterwards
	assertPKSet(t, "cached Record.Admins after the caller mutated its slice",
		s.cfg.Record.Admins, []cipher.PubKey{adminA})
}

// TestSetAdminRoster_PrefersLiveMembersSnapshot — an owner session keeps the
// authoritative roster in s.members (updated by SetAllowlist), which can be
// ahead of the cfg.Record.Members it was opened with. The admin-roster
// recompute must read the live one.
func TestSetAdminRoster_PrefersLiveMembersSnapshot(t *testing.T) {
	me, sk := cipher.GenerateKeyPair() // founder → implicitly an admin
	stale, _ := cipher.GenerateKeyPair()
	fresh, _ := cipher.GenerateKeyPair()

	s := newRosterSession(t, me, sk, Record{
		OwnerPK: me,
		Role:    RoleOwner,
		Members: []cipher.PubKey{me, stale}, // the record it was opened with
	})

	// SetAllowlist installs the live snapshot, replacing `stale` with `fresh`.
	if _, err := s.SetAllowlist([]cipher.PubKey{me, fresh}); err != nil {
		t.Fatalf("SetAllowlist: %v", err)
	}
	assertPKSet(t, "peerSubs after the allowlist change", peerSubPKs(s), []cipher.PubKey{fresh})

	// As founder I am an admin, so desired == every member but me. If this
	// read cfg.Record.Members it would resurrect `stale` and drop `fresh`.
	if _, err := s.SetAdminRoster(nil); err != nil {
		t.Fatalf("SetAdminRoster: %v", err)
	}
	assertPKSet(t, "peerSubs after the admin recompute", peerSubPKs(s), []cipher.PubKey{fresh})
	assertBookkeepingMatchesSubs(t, s)
}
