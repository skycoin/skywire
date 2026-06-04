package usermanager

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// memUserStore is a tiny in-memory UserStore for tests. User() returns a
// copy (as BoltUserStore does via gob-decode), so mutating the returned
// *User only persists via SetUser.
type memUserStore struct{ m map[string]User }

func newMemUserStore() *memUserStore { return &memUserStore{m: make(map[string]User)} }

func (s *memUserStore) User(name string) (*User, error) {
	u, ok := s.m[name]
	if !ok {
		return nil, errors.New("user not found")
	}
	cp := u
	return &cp, nil
}

func (s *memUserStore) AddUser(u User) error {
	if _, ok := s.m[u.Name]; ok {
		return ErrUserExists
	}
	s.m[u.Name] = u
	return nil
}

func (s *memUserStore) SetUser(u User) error         { s.m[u.Name] = u; return nil }
func (s *memUserStore) RemoveUser(name string) error { delete(s.m, name); return nil }
func (s *memUserStore) Close() error                 { return nil }

// newTestUserManager builds a UserManager using only the fields
// SetAdminPassword touches (store, sessions, mutex) — no cookie crypto.
func newTestUserManager(store UserStore) *UserManager {
	return &UserManager{
		db:       store,
		sessions: make(map[uuid.UUID]Session),
		mu:       new(sync.RWMutex),
	}
}

// TestSetAdminPassword_CreatesAdminWhenAbsent: a first-time set from the
// CLI (--force) must create the "admin" account, so no UI create-account
// round-trip is needed.
func TestSetAdminPassword_CreatesAdminWhenAbsent(t *testing.T) {
	store := newMemUserStore()
	um := newTestUserManager(store)

	if _, err := store.User("admin"); err == nil {
		t.Fatal("precondition: admin must not exist yet")
	}

	const pw = "Sky!wire9"
	if err := um.SetAdminPassword(pw); err != nil {
		t.Fatalf("SetAdminPassword (create): %v", err)
	}

	u, err := store.User("admin")
	if err != nil {
		t.Fatalf("admin account not created: %v", err)
	}
	if !u.VerifyPassword(pw) {
		t.Fatal("created admin does not verify the set password")
	}
}

// TestSetAdminPassword_ResetsWithoutOld: a forgotten-password reset must
// overwrite the existing admin password without supplying the old one.
func TestSetAdminPassword_ResetsWithoutOld(t *testing.T) {
	store := newMemUserStore()
	um := newTestUserManager(store)

	const oldPw, newPw = "Old!pass1", "New!pass2"
	if err := um.SetAdminPassword(oldPw); err != nil {
		t.Fatalf("setup set: %v", err)
	}

	if err := um.SetAdminPassword(newPw); err != nil {
		t.Fatalf("SetAdminPassword (reset): %v", err)
	}

	u, err := store.User("admin")
	if err != nil {
		t.Fatalf("admin missing after reset: %v", err)
	}
	if !u.VerifyPassword(newPw) {
		t.Fatal("reset password does not verify")
	}
	if u.VerifyPassword(oldPw) {
		t.Fatal("old password still verifies after reset")
	}
}

// TestSetAdminPassword_RejectsBadFormat: the force path must NOT bypass
// the password-format rules.
func TestSetAdminPassword_RejectsBadFormat(t *testing.T) {
	um := newTestUserManager(newMemUserStore())
	if err := um.SetAdminPassword("weak"); err == nil {
		t.Fatal("expected a weak password to be rejected")
	}
}
