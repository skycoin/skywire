// Package group cmd/apps/skychat/group/pure_helpers_test.go
//
// Unit coverage for the package's pure helpers and record predicates
// that the CXO-fixture tests only exercise transitively: Mode.IsValid,
// Record.IsFounder/IsAdmin/EnsureFounderInAdmins, the roster set
// utilities, the session-level classifiers, and the store's MarkMessage.
package group

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestModeIsValid(t *testing.T) {
	if !ModePublic.IsValid() || !ModePrivate.IsValid() {
		t.Error("public and private modes should be valid")
	}
	if Mode("bogus").IsValid() || Mode("").IsValid() {
		t.Error("unknown/empty mode should be invalid")
	}
}

func TestRecordIsFounderIsAdmin(t *testing.T) {
	owner, admin, member := pkN(1), pkN(2), pkN(3)
	r := Record{
		OwnerPK: owner,
		Admins:  []cipher.PubKey{admin},
		Members: []cipher.PubKey{owner, admin, member},
	}
	if !r.IsFounder(owner) || r.IsFounder(admin) {
		t.Error("IsFounder should be true only for the owner")
	}
	// Founder is implicitly an admin; explicit admins are admins;
	// plain members are not.
	if !r.IsAdmin(owner) || !r.IsAdmin(admin) {
		t.Error("founder and explicit admin should both be admins")
	}
	if r.IsAdmin(member) {
		t.Error("plain member should not be an admin")
	}

	// Legacy record with a nil Admins slice still treats the founder as
	// an admin (the union check short-circuits on IsFounder).
	legacy := Record{OwnerPK: owner}
	if !legacy.IsAdmin(owner) {
		t.Error("legacy record: founder should still be admin")
	}
	if legacy.IsAdmin(member) {
		t.Error("legacy record: non-founder should not be admin")
	}
}

func TestEnsureFounderInAdmins(t *testing.T) {
	owner, other := pkN(1), pkN(2)

	// Zero OwnerPK -> no-op.
	empty := &Record{}
	empty.EnsureFounderInAdmins()
	if len(empty.Admins) != 0 {
		t.Errorf("zero owner should stay empty, got %v", empty.Admins)
	}

	// Founder absent -> prepended; idempotent thereafter.
	r := &Record{OwnerPK: owner, Admins: []cipher.PubKey{other}}
	r.EnsureFounderInAdmins()
	if len(r.Admins) != 2 || r.Admins[0] != owner {
		t.Fatalf("founder should be prepended, got %v", r.Admins)
	}
	r.EnsureFounderInAdmins()
	if len(r.Admins) != 2 {
		t.Errorf("second call should be a no-op, got %v", r.Admins)
	}

	// Founder already present -> unchanged.
	r2 := &Record{OwnerPK: owner, Admins: []cipher.PubKey{owner, other}}
	r2.EnsureFounderInAdmins()
	if len(r2.Admins) != 2 || r2.Admins[0] != owner {
		t.Errorf("already-present founder should be unchanged, got %v", r2.Admins)
	}
}

func TestContainsRemovePK(t *testing.T) {
	a, b, c := pkN(1), pkN(2), pkN(3)
	list := []cipher.PubKey{a, b}
	if !containsPK(list, a) || !containsPK(list, b) {
		t.Error("containsPK should find present keys")
	}
	if containsPK(list, c) {
		t.Error("containsPK should not find an absent key")
	}
	if got := removePK(list, a); len(got) != 1 || got[0] != b {
		t.Errorf("removePK(a) = %v, want [b]", got)
	}
	if got := removePK(list, c); len(got) != 2 {
		t.Errorf("removePK(absent) should keep all, got %v", got)
	}
}

func TestSubscribedPrefixes(t *testing.T) {
	got := subscribedPrefixes()
	want := []string{MessagePathPrefix, RosterPathPrefix, AdminPathPrefix, ModerationPathPrefix}
	if len(got) != len(want) {
		t.Fatalf("subscribedPrefixes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("prefix %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIsSubscribeRejected(t *testing.T) {
	if isSubscribeRejected(nil) {
		t.Error("nil error is not a subscribe rejection")
	}
	if isSubscribeRejected(errors.New("dial timeout")) {
		t.Error("a transient dial error is not a subscribe rejection")
	}
	if !isSubscribeRejected(errors.New("cxo: subscribe rejected by publisher")) {
		t.Error("a 'subscribe rejected' error should be classified as a rejection")
	}
}

func TestIsHeartbeat(t *testing.T) {
	if !IsHeartbeat(Message{Text: HeartbeatMarker}) {
		t.Error("the heartbeat marker should be recognized")
	}
	if IsHeartbeat(Message{Text: "hello"}) {
		t.Error("normal chat text is not a heartbeat")
	}
}

func TestNewRelayMsgID(t *testing.T) {
	seen := make(map[string]struct{}, 500)
	for range 500 {
		id := newRelayMsgID()
		if len(id) != 16 {
			t.Fatalf("relay msg id %q len=%d, want 16", id, len(id))
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate relay msg id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestSessionIsMember(t *testing.T) {
	a, b, other := pkN(1), pkN(2), pkN(3)
	s := &Session{members: []cipher.PubKey{a, b}}
	if !s.isMember(a) || !s.isMember(b) {
		t.Error("members should be recognized")
	}
	if s.isMember(other) {
		t.Error("a non-member should not be recognized")
	}
}

func TestUniqueWithSelf(t *testing.T) {
	self, x, y := pkN(1), pkN(2), pkN(3)
	got := uniqueWithSelf(self, []cipher.PubKey{x, x, y, self})
	if len(got) != 3 || got[0] != self {
		t.Fatalf("uniqueWithSelf = %v, want self-first + deduped [self x y]", got)
	}
	counts := map[cipher.PubKey]int{}
	for _, pk := range got {
		counts[pk]++
	}
	if counts[self] != 1 || counts[x] != 1 || counts[y] != 1 {
		t.Errorf("each key should appear exactly once, got %v", got)
	}
	if only := uniqueWithSelf(self, nil); len(only) != 1 || only[0] != self {
		t.Errorf("uniqueWithSelf(self, nil) = %v, want [self]", only)
	}
}

func TestStoreMarkMessage(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close() //nolint:errcheck

	id := "abcdef01-0000-0000-0000-000000000000"
	if err := s.Put(Record{
		ID: id, OwnerPK: pkN(1), Port: 40001, Mode: ModePublic, Role: RoleOwner, Status: StatusActive,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	ts := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	if err := s.MarkMessage(id, ts); err != nil {
		t.Fatalf("MarkMessage: %v", err)
	}
	got, ok, err := s.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: err=%v ok=%v", err, ok)
	}
	if !got.LastMessageAt.Equal(ts) {
		t.Errorf("LastMessageAt = %v, want %v", got.LastMessageAt, ts)
	}
	if err := s.MarkMessage("no-such-id", ts); err == nil {
		t.Error("MarkMessage on a missing record should error")
	}
}
