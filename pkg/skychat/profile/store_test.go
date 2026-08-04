// Package profile pkg/skychat/profile/store_test.go c4-app-chat
package profile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "profile.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return s, path
}

// A visor that has never set a profile has an empty one. Not an error: it is
// the normal state, and every caller renders it as "nothing said".
func TestOpenStoreMissingFileIsEmpty(t *testing.T) {
	s, _ := tempStore(t)
	p, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !p.IsZero() {
		t.Fatalf("fresh store is not empty: %+v", p)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s, path := tempStore(t)
	avatar := pngOf(t, 32, 32)

	saved, err := s.Save(Profile{Name: "  Alice  Smith ", Avatar: avatar})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Name != "Alice Smith" {
		t.Fatalf("Save returned %q, want the normalized name", saved.Name)
	}
	if saved.AvatarMime != MimePNG {
		t.Fatalf("mime = %q", saved.AvatarMime)
	}

	// Cached read.
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "Alice Smith" || len(got.Avatar) != len(avatar) {
		t.Fatalf("cached load = %+v", got)
	}

	// And from disk, through a second store — the point of persisting.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err = s2.Load()
	if err != nil {
		t.Fatalf("reopened Load: %v", err)
	}
	if got.Name != "Alice Smith" || got.AvatarMime != MimePNG {
		t.Fatalf("reopened = %+v", got)
	}
}

// Updated is the store's own clock, not the caller's: letting an RPC caller
// set it would let a UI backdate a change.
func TestSaveStampsUpdatedIgnoringCaller(t *testing.T) {
	s, _ := tempStore(t)
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Now().UTC().Add(-time.Second)

	saved, err := s.Save(Profile{Name: "Bob", Updated: past})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !saved.Updated.After(before) {
		t.Fatalf("Updated = %v, want a fresh stamp (caller sent %v)", saved.Updated, past)
	}
}

func TestSaveRejectsBadAvatarAndLeavesPreviousIntact(t *testing.T) {
	s, _ := tempStore(t)
	if _, err := s.Save(Profile{Name: "Alice", Avatar: pngOf(t, 32, 32)}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := s.Save(Profile{Name: "Mallory", Avatar: []byte("not an image")}); err == nil {
		t.Fatal("bad avatar accepted")
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "Alice" {
		t.Fatalf("a rejected save changed the stored profile: %+v", got)
	}
}

func TestClearRemovesFileAndCache(t *testing.T) {
	s, path := tempStore(t)
	if _, err := s.Save(Profile{Name: "Alice"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	p, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !p.IsZero() {
		t.Fatalf("cleared store still reports %+v", p)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still present after Clear (stat err = %v)", err)
	}
	// Idempotent: clearing twice is what a user pressing the button twice
	// does, and it is not an error.
	if err := s.Clear(); err != nil {
		t.Fatalf("second Clear: %v", err)
	}
}

// A file edited by hand can hold an avatar that would no longer pass. Losing
// the whole profile over one bad field would be worse than losing the field.
func TestOpenStoreSalvagesNameFromBadAvatar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	body := `{"name":"Alice","avatar":"bm90IGFuIGltYWdl","avatar_mime":"image/png"}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	p, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Name != "Alice" {
		t.Fatalf("name lost: %+v", p)
	}
	if p.HasAvatar() {
		t.Fatalf("an undecodable avatar was kept: %+v", p)
	}
}

// Unparseable is different from absent: starting blank would look to the
// operator like their profile had been silently discarded.
func TestOpenStoreRefusesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("corrupt file opened without complaint")
	}
}

// The Manager holds this behind an interface and a nil provider is the
// no-profile case, so the nil receiver must not panic.
func TestNilStoreLoadsEmpty(t *testing.T) {
	var s *Store
	p, err := s.Load()
	if err != nil || !p.IsZero() {
		t.Fatalf("nil store: %+v, %v", p, err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("nil Clear: %v", err)
	}
	if s.Path() != "" {
		t.Fatalf("nil Path = %q", s.Path())
	}
}
