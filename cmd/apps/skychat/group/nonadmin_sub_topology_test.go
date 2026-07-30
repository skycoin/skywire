// Package group — cmd/apps/skychat/group/nonadmin_sub_topology_test.go:
// pins the admin-aggregator subscription rule (#2685 design) on the
// pure-function helper that drives openMember + SetAdminRoster. The
// full Session/CXO integration rides the manual E2E plan (same
// convention as the rest of the package's tests); these tests cover
// the role-conditional subscription set in isolation so a refactor
// can't drift the contract Beta's admin-aggregator publisher depends
// on.
//
// Rule under test:
//
//	desired = { pk in Members : pk != myPK AND (IsAdmin(myPK) OR IsAdmin(pk)) }
//	          ∪ ringSuccessors(non-admin members, myPK, fanout)  // non-admins
//
// Admins follow every member. A non-admin follows every admin PLUS a
// bounded number of other non-admins, so the group stays readable when no
// admin is online. Passing fanout 0 pins the original admins-only shape,
// which is still what a group gets when an admin turns peer backfill off.

package group

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func sortedPKHexes(set map[cipher.PubKey]struct{}) []string {
	out := make([]string, 0, len(set))
	for pk := range set {
		out = append(out, pk.Hex())
	}
	sort.Strings(out)
	return out
}

func sortedHexList(pks ...cipher.PubKey) []string {
	out := make([]string, 0, len(pks))
	for _, pk := range pks {
		out = append(out, pk.Hex())
	}
	sort.Strings(out)
	return out
}

func TestDesiredPeerSubsForRole_AdminFollowsEveryMember(t *testing.T) {
	// Admin path: when this visor is admin (owner OR in Admins),
	// the desired set is every non-self member regardless of admin
	// status. This is the "input pipe" side of the topology — admins
	// observe every leaf so they can republish for non-admins.
	owner, _ := cipher.GenerateKeyPair()
	adminB, _ := cipher.GenerateKeyPair()
	memberA, _ := cipher.GenerateKeyPair()
	memberB, _ := cipher.GenerateKeyPair()
	r := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{owner, adminB},
		Members: []cipher.PubKey{owner, adminB, memberA, memberB},
	}

	t.Run("owner_sees_all_non_self", func(t *testing.T) {
		got := desiredPeerSubsForRole(r, owner, 0)
		want := sortedHexList(adminB, memberA, memberB)
		if !reflect.DeepEqual(sortedPKHexes(got), want) {
			t.Errorf("owner desired set: got %v, want %v", sortedPKHexes(got), want)
		}
	})

	t.Run("non_owner_admin_sees_all_non_self", func(t *testing.T) {
		got := desiredPeerSubsForRole(r, adminB, 0)
		want := sortedHexList(owner, memberA, memberB)
		if !reflect.DeepEqual(sortedPKHexes(got), want) {
			t.Errorf("admin-but-not-owner desired set: got %v, want %v", sortedPKHexes(got), want)
		}
	})
}

func TestDesiredPeerSubsForRole_NonAdminFollowsOnlyAdmins(t *testing.T) {
	// Core admin-aggregator rule: non-admin's desired set is just
	// the admins. The non-admin's leaves still propagate through
	// the admin-republish path; the non-admin sees other non-admins'
	// leaves transitively via any admin's canonical feed.
	owner, _ := cipher.GenerateKeyPair()
	adminB, _ := cipher.GenerateKeyPair()
	memberA, _ := cipher.GenerateKeyPair()
	memberB, _ := cipher.GenerateKeyPair()
	r := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{owner, adminB},
		Members: []cipher.PubKey{owner, adminB, memberA, memberB},
	}
	got := desiredPeerSubsForRole(r, memberA, 0)
	want := sortedHexList(owner, adminB)
	if !reflect.DeepEqual(sortedPKHexes(got), want) {
		t.Errorf("non-admin desired set: got %v, want %v (admin-aggregator: should be admins only)", sortedPKHexes(got), want)
	}
	// Pin the inverse: memberB (the other non-admin) is explicitly
	// NOT in the set — that's the linear-scaling win.
	if _, present := got[memberB]; present {
		t.Errorf("non-admin desired set: includes memberB (another non-admin) — should be admin-only")
	}
}

func TestDesiredPeerSubsForRole_OwnerOnly_NoAdmins_BootstrapCase(t *testing.T) {
	// Group with just an owner + one non-admin member (the
	// founder-only-admin bootstrap case). Non-admin still gets the
	// owner since the owner is implicitly admin via IsAdmin's
	// founder rule, regardless of the explicit Admins slice.
	owner, _ := cipher.GenerateKeyPair()
	member, _ := cipher.GenerateKeyPair()
	r := Record{
		OwnerPK: owner,
		Admins:  nil, // EnsureFounderInAdmins not called; founder is implicit
		Members: []cipher.PubKey{owner, member},
	}
	t.Run("owner_desired_set", func(t *testing.T) {
		got := desiredPeerSubsForRole(r, owner, 0)
		want := sortedHexList(member)
		if !reflect.DeepEqual(sortedPKHexes(got), want) {
			t.Errorf("owner: got %v, want %v", sortedPKHexes(got), want)
		}
	})
	t.Run("member_sees_owner", func(t *testing.T) {
		got := desiredPeerSubsForRole(r, member, 0)
		want := sortedHexList(owner)
		if !reflect.DeepEqual(sortedPKHexes(got), want) {
			t.Errorf("member: got %v, want %v (non-admin must always see owner via founder-as-implicit-admin)", sortedPKHexes(got), want)
		}
	})
}

func TestDesiredPeerSubsForRole_NeverIncludesSelf(t *testing.T) {
	// Self is always skipped. Holds across admin/non-admin roles
	// and across founder-vs-explicit-admin paths.
	owner, _ := cipher.GenerateKeyPair()
	adminB, _ := cipher.GenerateKeyPair()
	memberA, _ := cipher.GenerateKeyPair()
	r := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{owner, adminB},
		Members: []cipher.PubKey{owner, adminB, memberA},
	}
	for _, self := range []cipher.PubKey{owner, adminB, memberA} {
		got := desiredPeerSubsForRole(r, self, 0)
		if _, present := got[self]; present {
			t.Errorf("self (%s) appeared in desired set: %v", self.Hex()[:8], sortedPKHexes(got))
		}
	}
}

func TestDesiredPeerSubsForRole_PromoteFlipsNonAdminToFullMesh(t *testing.T) {
	// Promote semantic: a non-admin sees only admins; after they're
	// promoted, they see every non-self member. This is exactly the
	// case Manager.PromoteAdmin → Session.SetAdminRoster covers at
	// the live-session layer.
	owner, _ := cipher.GenerateKeyPair()
	adminB, _ := cipher.GenerateKeyPair()
	memberA, _ := cipher.GenerateKeyPair()
	memberB, _ := cipher.GenerateKeyPair()

	before := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{owner, adminB},
		Members: []cipher.PubKey{owner, adminB, memberA, memberB},
	}
	preSet := desiredPeerSubsForRole(before, memberA, 0)
	if len(preSet) != 2 {
		t.Fatalf("pre-promote: non-admin desired len = %d, want 2 (admins only)", len(preSet))
	}

	// Promote memberA.
	after := before
	after.Admins = append([]cipher.PubKey(nil), before.Admins...)
	after.Admins = append(after.Admins, memberA)
	postSet := desiredPeerSubsForRole(after, memberA, 0)
	if len(postSet) != 3 {
		t.Errorf("post-promote: now-admin desired len = %d, want 3 (full mesh non-self)", len(postSet))
	}
	if _, present := postSet[memberB]; !present {
		t.Errorf("post-promote: now-admin should subscribe to memberB (the remaining non-admin) — full-mesh input pipe expected")
	}
}

func TestDesiredPeerSubsForRole_DemoteShrinksToAdminsOnly(t *testing.T) {
	// Demote semantic: an admin sees full mesh; after demotion they
	// see only the remaining admins. Mirror of the promote test.
	owner, _ := cipher.GenerateKeyPair()
	adminB, _ := cipher.GenerateKeyPair()
	adminC, _ := cipher.GenerateKeyPair()
	memberA, _ := cipher.GenerateKeyPair()

	before := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{owner, adminB, adminC},
		Members: []cipher.PubKey{owner, adminB, adminC, memberA},
	}
	preSet := desiredPeerSubsForRole(before, adminC, 0)
	if len(preSet) != 3 {
		t.Fatalf("pre-demote: admin desired len = %d, want 3 (full mesh non-self)", len(preSet))
	}

	// Demote adminC (the self).
	after := before
	after.Admins = []cipher.PubKey{owner, adminB} // adminC dropped
	postSet := desiredPeerSubsForRole(after, adminC, 0)
	if len(postSet) != 2 {
		t.Errorf("post-demote: now-non-admin desired len = %d, want 2 (admins only)", len(postSet))
	}
	if _, present := postSet[memberA]; present {
		t.Errorf("post-demote: now-non-admin should NOT subscribe to memberA (also non-admin) — admin-only expected")
	}
}

func TestDesiredPeerSubsForRole_EmptyMembers_EmptyDesired(t *testing.T) {
	// Degenerate: no members → empty desired set. Guards a
	// future refactor that might add a default-self-include or
	// founder-fallback there.
	owner, _ := cipher.GenerateKeyPair()
	r := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{owner},
		Members: nil,
	}
	got := desiredPeerSubsForRole(r, owner, 0)
	if len(got) != 0 {
		t.Errorf("empty-members desired set: got %d entries, want 0 (%v)", len(got), sortedPKHexes(got))
	}
}

// --- peer backfill: the fanout beyond the admins ---------------------------

// The point of the fanout: a non-admin follows other non-admins too, so
// the group does not go dark when its admins are offline.
func TestDesiredPeerSubsForRole_NonAdminAlsoFollowsPeers(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	m1, _ := cipher.GenerateKeyPair()
	m2, _ := cipher.GenerateKeyPair()
	m3, _ := cipher.GenerateKeyPair()
	r := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{owner},
		Members: []cipher.PubKey{owner, m1, m2, m3},
	}

	// Fanout 1: the admin plus exactly one peer.
	got := desiredPeerSubsForRole(r, m1, 1)
	if len(got) != 2 {
		t.Fatalf("fanout 1: desired = %v, want the owner + 1 peer", sortedPKHexes(got))
	}
	if _, hasOwner := got[owner]; !hasOwner {
		t.Error("fanout 1: the admin must always be followed")
	}
	if _, self := got[m1]; self {
		t.Error("fanout 1: self must never be followed")
	}

	// Fanout 2 with two peer candidates: both, and no duplicates of the
	// admin.
	got = desiredPeerSubsForRole(r, m1, 2)
	if len(got) != 3 {
		t.Errorf("fanout 2: desired = %v, want owner + m2 + m3", sortedPKHexes(got))
	}

	// Fanout larger than the candidate pool clamps rather than repeating.
	got = desiredPeerSubsForRole(r, m1, 99)
	if len(got) != 3 {
		t.Errorf("oversized fanout: desired = %v, want every other member once", sortedPKHexes(got))
	}

	// Zero and negative fanout restore admins-only exactly.
	for _, k := range []int{0, -1} {
		got = desiredPeerSubsForRole(r, m1, k)
		if len(got) != 1 {
			t.Errorf("fanout %d: desired = %v, want admins only", k, sortedPKHexes(got))
		}
	}
}

// The group's own setting is the ceiling: an admin turning backfill off
// means members must stop following each other, whatever this visor's
// local fanout says.
func TestDesiredPeerSubsForRole_GroupSettingOverridesFanout(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	m1, _ := cipher.GenerateKeyPair()
	m2, _ := cipher.GenerateKeyPair()
	r := Record{
		OwnerPK:              owner,
		Admins:               []cipher.PubKey{owner},
		Members:              []cipher.PubKey{owner, m1, m2},
		PeerBackfillDisabled: true,
	}
	got := desiredPeerSubsForRole(r, m1, 5)
	if len(got) != 1 {
		t.Fatalf("backfill disabled: desired = %v, want admins only", sortedPKHexes(got))
	}
	if _, present := got[m2]; present {
		t.Error("backfill disabled: a non-admin peer is still being followed")
	}
	// An admin still follows everyone — the setting is about non-admins
	// carrying each other's history, not about admins doing their job.
	if got := desiredPeerSubsForRole(r, owner, 5); len(got) != 2 {
		t.Errorf("admin with backfill disabled: desired = %v, want every other member", sortedPKHexes(got))
	}
}

// The ring has to be deterministic (every visor computes the same
// topology with no coordination), evenly loaded (no member becomes the
// hotspot everyone follows), and connected (no islands, or leaves would
// never reach part of the group).
func TestRingSuccessors(t *testing.T) {
	const n = 12
	members := make([]cipher.PubKey, 0, n)
	for range n {
		pk, _ := cipher.GenerateKeyPair()
		members = append(members, pk)
	}

	// Deterministic: same inputs, same answer, regardless of input order.
	self := members[0]
	others := append([]cipher.PubKey(nil), members[1:]...)
	first := ringSuccessors(others, self, 2)
	shuffled := append([]cipher.PubKey(nil), others...)
	shuffled[0], shuffled[len(shuffled)-1] = shuffled[len(shuffled)-1], shuffled[0]
	second := ringSuccessors(shuffled, self, 2)
	if !reflect.DeepEqual(sortedHexList(first...), sortedHexList(second...)) {
		t.Error("ringSuccessors is not order-independent")
	}

	// Even in-degree and full connectivity across the whole ring.
	inDegree := map[cipher.PubKey]int{}
	adj := map[cipher.PubKey][]cipher.PubKey{}
	for _, me := range members {
		cands := make([]cipher.PubKey, 0, n-1)
		for _, pk := range members {
			if pk != me {
				cands = append(cands, pk)
			}
		}
		succ := ringSuccessors(cands, me, 2)
		if len(succ) != 2 {
			t.Fatalf("member got %d successors, want 2", len(succ))
		}
		for _, pk := range succ {
			if pk == me {
				t.Fatal("a member was made its own successor")
			}
			inDegree[pk]++
			adj[me] = append(adj[me], pk)
		}
	}
	for pk, d := range inDegree {
		if d < 1 || d > 4 {
			t.Errorf("member %s is followed by %d peers — the ring is lopsided", pk.Hex()[:8], d)
		}
	}
	if len(inDegree) != n {
		t.Errorf("%d of %d members are followed by nobody", n-len(inDegree), n)
	}

	// Reachability from an arbitrary start, following successors: a
	// connected ring means one online member's feed can carry the room.
	seen := map[cipher.PubKey]bool{members[0]: true}
	queue := []cipher.PubKey{members[0]}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nx := range adj[cur] {
			if !seen[nx] {
				seen[nx] = true
				queue = append(queue, nx)
			}
		}
	}
	if len(seen) != n {
		t.Errorf("ring is not connected: reached %d of %d members", len(seen), n)
	}

	// Degenerate inputs.
	if got := ringSuccessors(nil, self, 2); got != nil {
		t.Errorf("no candidates: got %v, want none", got)
	}
	if got := ringSuccessors(others, self, 0); got != nil {
		t.Errorf("zero k: got %v, want none", got)
	}
	if got := ringSuccessors(others, self, -3); got != nil {
		t.Errorf("negative k: got %v, want none", got)
	}
}

// The local knob and the group setting are independent switches, and
// mirroring must not happen for a member that follows nobody.
func TestShouldMirrorLeaves(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	member, _ := cipher.GenerateKeyPair()
	base := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{owner},
		Members: []cipher.PubKey{owner, member},
	}

	// An admin always mirrors — it is the guaranteed path and predates
	// the setting.
	admin := &Session{cfg: Config{MyPK: owner, Record: base}}
	if !admin.shouldMirrorLeaves() {
		t.Error("an admin should always mirror")
	}
	off := base
	off.PeerBackfillDisabled = true
	adminOff := &Session{cfg: Config{MyPK: owner, Record: off}}
	if !adminOff.shouldMirrorLeaves() {
		t.Error("an admin should mirror even with peer backfill off")
	}

	// A non-admin mirrors when the group allows it and this visor pays
	// for the fanout.
	plain := &Session{cfg: Config{MyPK: member, Record: base}}
	if !plain.shouldMirrorLeaves() {
		t.Error("a non-admin should mirror when backfill is on")
	}
	// Group says no.
	plainOff := &Session{cfg: Config{MyPK: member, Record: off}}
	if plainOff.shouldMirrorLeaves() {
		t.Error("a non-admin must not mirror when the group disabled backfill")
	}
	// This visor says no: taking availability without contributing it
	// would be the wrong default.
	optedOut := &Session{cfg: Config{MyPK: member, Record: base, PeerFanout: -1}}
	if optedOut.shouldMirrorLeaves() {
		t.Error("a non-admin that follows nobody must not mirror either")
	}
}

func TestConfigPeerFanout(t *testing.T) {
	if got := (Config{}).peerFanout(); got != defaultPeerFanout {
		t.Errorf("unset fanout = %d, want the default %d", got, defaultPeerFanout)
	}
	if got := (Config{PeerFanout: 5}).peerFanout(); got != 5 {
		t.Errorf("explicit fanout = %d, want 5", got)
	}
	if got := (Config{PeerFanout: -1}).peerFanout(); got != 0 {
		t.Errorf("negative fanout = %d, want 0 (opted out)", got)
	}
}

// The toggle has to survive the receive path: applied from a signed leaf,
// written onto the record, and carried in the snapshot handed to the
// Manager for persistence. A field missing from ModState would look like
// it applied and then silently revert.
func TestApplyModLeaf_PeerBackfillToggle(t *testing.T) {
	s, adminSK, _ := guardSession(t)
	gid := s.cfg.Record.ID
	base := time.Now().UTC().Add(-time.Hour)

	var persisted []ModState
	s.onModChange = func(st ModState) { persisted = append(persisted, st) }

	if !s.cfg.Record.PeerBackfillEnabled() {
		t.Fatal("setup: backfill should start enabled")
	}

	// Off.
	s.applyModLeaf(signedMod(t, gid, ModOpPeerBackfillOff, cipher.PubKey{}, adminSK, base))
	if s.cfg.Record.PeerBackfillEnabled() {
		t.Fatal("the off leaf did not apply")
	}
	if len(persisted) != 1 || !persisted[0].PeerBackfillDisabled {
		t.Fatalf("the snapshot handed to the manager lost the flag: %+v", persisted)
	}

	// A second, NEWER leaf turns it back on — the group-scoped watermark
	// must not lock the target after one decision.
	s.applyModLeaf(signedMod(t, gid, ModOpPeerBackfillOn, cipher.PubKey{}, adminSK, base.Add(time.Minute)))
	if !s.cfg.Record.PeerBackfillEnabled() {
		t.Fatal("the on leaf did not apply")
	}

	// A replay of the older off leaf must lose to the newer decision.
	s.applyModLeaf(signedMod(t, gid, ModOpPeerBackfillOff, cipher.PubKey{}, adminSK, base))
	if !s.cfg.Record.PeerBackfillEnabled() {
		t.Error("REPLAY: a superseded backfill-off leaf was re-applied")
	}

	// A read-only toggle shares the group-scoped watermark, so it has to
	// be able to follow a backfill change rather than being refused as
	// stale — they are different decisions about the same group.
	s.applyModLeaf(signedMod(t, gid, ModOpReadOnly, cipher.PubKey{}, adminSK, base.Add(2*time.Minute)))
	if !s.cfg.Record.ReadOnly {
		t.Error("a later read-only leaf was refused after a backfill toggle")
	}
}
