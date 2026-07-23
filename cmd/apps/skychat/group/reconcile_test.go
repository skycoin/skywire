// Package group cmd/apps/skychat/group/reconcile_test.go
//
// Coverage for the receive-side roster/admin reconciler (#3426): a
// subscriber that observes a signed roster/admin mutation leaf must
// converge its local member/admin set ONLY when the mutation is
// signature-valid AND issued by a current admin — and must never let a
// remove/demote strip the founder. Driven through Session.onUpdate with
// synthesized leaves (no network), mirroring the admin-republish and
// gossip-emit in-proc fixtures.
package group

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
)

// rosterChangeCapture records the reconciler's onRosterChange callback.
type rosterChangeCapture struct {
	mu          sync.Mutex
	calls       int
	lastMembers []cipher.PubKey
	lastAdmins  []cipher.PubKey
}

func (c *rosterChangeCapture) onChange(members, admins []cipher.PubKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.lastMembers = members
	c.lastAdmins = admins
}

func (c *rosterChangeCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// reconcileSession builds a minimal member-role Session (no publisher —
// SetAllowlist/SetAdminRoster error harmlessly on the nil publisher, the
// reconciler swallows that at debug and still applies the record change).
func reconcileSession(t *testing.T, myPK cipher.PubKey, mySK cipher.SecKey, rec Record) (*Session, *rosterChangeCapture) {
	t.Helper()
	cap := &rosterChangeCapture{}
	s := &Session{
		cfg:   Config{MyPK: myPK, MySK: mySK, Record: rec},
		dedup: newRecentSet(inboxDedupCap),
		log:   logging.MustGetLogger("group.reconcile-test"),
	}
	s.SetRosterChangeHandler(cap.onChange)
	return s, cap
}

func signedRosterLeaf(t *testing.T, gid uuid.UUID, op RosterOp, peerPK cipher.PubKey, issuerSK cipher.SecKey, seq uint64) (string, []byte) {
	t.Helper()
	m := RosterMutation{GroupID: gid, Op: op, PeerPK: peerPK, IssuedAt: time.Now().UTC()}
	if err := SignRoster(&m, issuerSK); err != nil {
		t.Fatalf("SignRoster: %v", err)
	}
	body, err := MarshalRoster(m)
	if err != nil {
		t.Fatalf("MarshalRoster: %v", err)
	}
	return fmt.Sprintf("%s/%05d", RosterPathPrefix, seq), body
}

func signedAdminLeaf(t *testing.T, gid uuid.UUID, op AdminOp, peerPK cipher.PubKey, issuerSK cipher.SecKey, seq uint64) (string, []byte) {
	t.Helper()
	m := AdminMutation{GroupID: gid, Op: op, PeerPK: peerPK, IssuedAt: time.Now().UTC()}
	if err := SignAdmin(&m, issuerSK); err != nil {
		t.Fatalf("SignAdmin: %v", err)
	}
	body, err := MarshalAdmin(m)
	if err != nil {
		t.Fatalf("MarshalAdmin: %v", err)
	}
	return fmt.Sprintf("%s/%05d", AdminPathPrefix, seq), body
}

func TestApplyRosterLeaf_AdminAddConverges(t *testing.T) {
	gid := uuid.New()
	ownerPK, ownerSK := cipher.GenerateKeyPair()
	myPK, mySK := cipher.GenerateKeyPair()
	newMember, _ := cipher.GenerateKeyPair()
	rec := Record{ID: gid.String(), OwnerPK: ownerPK, Members: []cipher.PubKey{ownerPK, myPK}, Role: RoleMember}
	s, cap := reconcileSession(t, myPK, mySK, rec)

	path, body := signedRosterLeaf(t, gid, RosterOpAdd, newMember, ownerSK, 1)
	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if !containsPK(s.cfg.Record.Members, newMember) {
		t.Error("member set should converge to include the admin-added member")
	}
	if cap.count() == 0 {
		t.Error("onRosterChange should have fired on the applied mutation")
	}
}

func TestApplyRosterLeaf_NonAdminIssuerRejected(t *testing.T) {
	gid := uuid.New()
	ownerPK, _ := cipher.GenerateKeyPair()
	myPK, mySK := cipher.GenerateKeyPair()
	nonAdminPK, nonAdminSK := cipher.GenerateKeyPair() // a plain member, not an admin
	newMember, _ := cipher.GenerateKeyPair()
	rec := Record{ID: gid.String(), OwnerPK: ownerPK, Members: []cipher.PubKey{ownerPK, myPK, nonAdminPK}, Role: RoleMember}
	s, cap := reconcileSession(t, myPK, mySK, rec)

	// A valid signature, but the issuer has no roster authority.
	path, body := signedRosterLeaf(t, gid, RosterOpAdd, newMember, nonAdminSK, 1)
	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if containsPK(s.cfg.Record.Members, newMember) {
		t.Error("a non-admin's roster mutation must be rejected (the authority gate)")
	}
	if cap.count() != 0 {
		t.Error("onRosterChange must not fire on a rejected mutation")
	}
}

func TestApplyRosterLeaf_AdminRemoveDropsMember(t *testing.T) {
	gid := uuid.New()
	ownerPK, ownerSK := cipher.GenerateKeyPair()
	myPK, mySK := cipher.GenerateKeyPair()
	victim, _ := cipher.GenerateKeyPair()
	rec := Record{ID: gid.String(), OwnerPK: ownerPK, Members: []cipher.PubKey{ownerPK, myPK, victim}, Role: RoleMember}
	s, _ := reconcileSession(t, myPK, mySK, rec)

	path, body := signedRosterLeaf(t, gid, RosterOpRemove, victim, ownerSK, 1)
	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if containsPK(s.cfg.Record.Members, victim) {
		t.Error("an admin's remove mutation should drop the member")
	}
}

func TestApplyRosterLeaf_RemoveNeverStripsFounder(t *testing.T) {
	gid := uuid.New()
	ownerPK, _ := cipher.GenerateKeyPair()
	admin2PK, admin2SK := cipher.GenerateKeyPair()
	myPK, mySK := cipher.GenerateKeyPair()
	rec := Record{
		ID: gid.String(), OwnerPK: ownerPK,
		Admins:  []cipher.PubKey{admin2PK},
		Members: []cipher.PubKey{ownerPK, admin2PK, myPK},
		Role:    RoleMember,
	}
	s, _ := reconcileSession(t, myPK, mySK, rec)

	// A real admin issues a remove targeting the founder — must be refused.
	path, body := signedRosterLeaf(t, gid, RosterOpRemove, ownerPK, admin2SK, 1)
	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if !containsPK(s.cfg.Record.Members, ownerPK) {
		t.Error("the founder must never be removed from the member set")
	}
}

func TestApplyAdminLeaf_PromoteConverges(t *testing.T) {
	gid := uuid.New()
	ownerPK, ownerSK := cipher.GenerateKeyPair()
	myPK, mySK := cipher.GenerateKeyPair()
	promoted, _ := cipher.GenerateKeyPair()
	rec := Record{ID: gid.String(), OwnerPK: ownerPK, Members: []cipher.PubKey{ownerPK, myPK, promoted}, Role: RoleMember}
	s, _ := reconcileSession(t, myPK, mySK, rec)

	path, body := signedAdminLeaf(t, gid, AdminOpPromote, promoted, ownerSK, 1)
	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if !s.cfg.Record.IsAdmin(promoted) {
		t.Error("admin promote should add the peer to the admin set")
	}
}

func TestApplyAdminLeaf_CannotDemoteFounder(t *testing.T) {
	gid := uuid.New()
	ownerPK, _ := cipher.GenerateKeyPair()
	admin2PK, admin2SK := cipher.GenerateKeyPair()
	myPK, mySK := cipher.GenerateKeyPair()
	rec := Record{
		ID: gid.String(), OwnerPK: ownerPK,
		Admins:  []cipher.PubKey{admin2PK},
		Members: []cipher.PubKey{ownerPK, admin2PK, myPK},
		Role:    RoleMember,
	}
	s, _ := reconcileSession(t, myPK, mySK, rec)

	// A valid admin tries to demote the founder — the reconciler refuses.
	path, body := signedAdminLeaf(t, gid, AdminOpDemote, ownerPK, admin2SK, 1)
	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if !s.cfg.Record.IsAdmin(ownerPK) {
		t.Error("the founder must never be demotable")
	}
}

func TestApplyRosterLeaf_WrongGroupIgnored(t *testing.T) {
	gid := uuid.New()
	otherGid := uuid.New()
	ownerPK, ownerSK := cipher.GenerateKeyPair()
	myPK, mySK := cipher.GenerateKeyPair()
	newMember, _ := cipher.GenerateKeyPair()
	rec := Record{ID: gid.String(), OwnerPK: ownerPK, Members: []cipher.PubKey{ownerPK, myPK}, Role: RoleMember}
	s, cap := reconcileSession(t, myPK, mySK, rec)

	// Mutation carries a different group id — must be ignored entirely.
	path, body := signedRosterLeaf(t, otherGid, RosterOpAdd, newMember, ownerSK, 1)
	s.onUpdate([]treestore.UpdateEvent{{Path: path, Value: body}})

	if containsPK(s.cfg.Record.Members, newMember) {
		t.Error("a mutation for a different group must not apply")
	}
	if cap.count() != 0 {
		t.Error("onRosterChange must not fire for a foreign-group mutation")
	}
}
