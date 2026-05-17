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
//	desired = { pk in Members : pk != myPK AND (r.IsAdmin(myPK) OR r.IsAdmin(pk)) }
//
// Admins follow every member; non-admins follow only admins.

package group

import (
	"reflect"
	"sort"
	"testing"

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
		got := desiredPeerSubsForRole(r, owner)
		want := sortedHexList(adminB, memberA, memberB)
		if !reflect.DeepEqual(sortedPKHexes(got), want) {
			t.Errorf("owner desired set: got %v, want %v", sortedPKHexes(got), want)
		}
	})

	t.Run("non_owner_admin_sees_all_non_self", func(t *testing.T) {
		got := desiredPeerSubsForRole(r, adminB)
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
	got := desiredPeerSubsForRole(r, memberA)
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
		got := desiredPeerSubsForRole(r, owner)
		want := sortedHexList(member)
		if !reflect.DeepEqual(sortedPKHexes(got), want) {
			t.Errorf("owner: got %v, want %v", sortedPKHexes(got), want)
		}
	})
	t.Run("member_sees_owner", func(t *testing.T) {
		got := desiredPeerSubsForRole(r, member)
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
		got := desiredPeerSubsForRole(r, self)
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
	preSet := desiredPeerSubsForRole(before, memberA)
	if len(preSet) != 2 {
		t.Fatalf("pre-promote: non-admin desired len = %d, want 2 (admins only)", len(preSet))
	}

	// Promote memberA.
	after := before
	after.Admins = append([]cipher.PubKey(nil), before.Admins...)
	after.Admins = append(after.Admins, memberA)
	postSet := desiredPeerSubsForRole(after, memberA)
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
	preSet := desiredPeerSubsForRole(before, adminC)
	if len(preSet) != 3 {
		t.Fatalf("pre-demote: admin desired len = %d, want 3 (full mesh non-self)", len(preSet))
	}

	// Demote adminC (the self).
	after := before
	after.Admins = []cipher.PubKey{owner, adminB} // adminC dropped
	postSet := desiredPeerSubsForRole(after, adminC)
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
	got := desiredPeerSubsForRole(r, owner)
	if len(got) != 0 {
		t.Errorf("empty-members desired set: got %d entries, want 0 (%v)", len(got), sortedPKHexes(got))
	}
}
