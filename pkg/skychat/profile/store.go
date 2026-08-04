// Package profile pkg/skychat/profile/store.go c4-app-chat
// where a visor keeps the profile it publishes.
//
// One small JSON file, written whole. Not bbolt like the group store and
// not sealed like it either, and both differences are deliberate: this is a
// single record that changes when a human edits a dialog, and its entire
// contents are handed to any stranger who asks for them. Encrypting at rest
// something that is published by design would protect nothing while making
// the file unreadable to the operator who wrote it.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is the on-disk home of this visor's published profile.
//
// Safe for concurrent use: the probe listener reads it on any stranger's
// request while the operator may be saving a new one over RPC.
type Store struct {
	mu   sync.RWMutex
	path string

	// cached is the last known good profile, so the common path — a
	// stranger asking who we are — is answered without touching the disk.
	// Populated on open and replaced on every successful save.
	cached Profile
}

// OpenStore opens (or prepares) the profile file at path.
//
// A missing file is not an error: a visor that has never set a profile has
// an empty one, which is a legitimate answer. A file that exists but cannot
// be parsed IS an error — quietly starting from blank would look to the
// operator like their profile had been silently discarded, which is a worse
// outcome than refusing to start the feature and saying why.
func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("skychat profile: OpenStore: path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("skychat profile: OpenStore: mkdir: %w", err)
	}
	s := &Store{path: path}
	b, err := os.ReadFile(path) //nolint:gosec // operator-controlled path from the visor config
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("skychat profile: OpenStore: read: %w", err)
	}
	if len(b) == 0 {
		return s, nil
	}
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("skychat profile: OpenStore: parse %s: %w", path, err)
	}
	// Re-validated on load rather than trusted: the file is editable by
	// hand, and an avatar that grew past the cap on disk must not become
	// one this visor serves. A bad avatar drops the avatar and keeps the
	// name — losing the whole profile over one field would be worse.
	if norm, err := Normalize(p); err == nil {
		s.cached = norm
	} else {
		s.cached = Profile{Name: NormalizeName(p.Name), Updated: p.Updated.UTC()}
	}
	return s, nil
}

// Load returns the current profile. Never fails: an unset profile is the
// zero value, which every caller renders as "nothing said".
func (s *Store) Load() (Profile, error) {
	if s == nil {
		return Profile{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cached, nil
}

// Save normalizes and persists a profile, returning what was actually
// stored — which is what the caller should echo back to the user, since
// normalization may have trimmed the name.
//
// Written to a temporary file and renamed, so a crash mid-write leaves the
// previous profile intact rather than a truncated one. The cache is
// replaced only after the rename succeeds, for the same reason: memory and
// disk should never disagree about what this visor publishes.
func (s *Store) Save(p Profile) (Profile, error) {
	if s == nil {
		return Profile{}, errors.New("skychat profile: Save: no store")
	}
	// Stamped here rather than trusting the caller's clock field: Updated
	// means "when this visor wrote it", and letting an RPC caller set it
	// would let a UI backdate a change.
	p.Updated = time.Now().UTC()
	norm, err := Normalize(p)
	if err != nil {
		return Profile{}, err
	}
	b, err := json.MarshalIndent(&norm, "", "  ")
	if err != nil {
		return Profile{}, fmt.Errorf("skychat profile: Save: encode: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".profile-*.tmp")
	if err != nil {
		return Profile{}, fmt.Errorf("skychat profile: Save: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck // no-op once the rename has taken it

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close() //nolint:errcheck
		return Profile{}, fmt.Errorf("skychat profile: Save: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close() //nolint:errcheck
		return Profile{}, fmt.Errorf("skychat profile: Save: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Profile{}, fmt.Errorf("skychat profile: Save: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		return Profile{}, fmt.Errorf("skychat profile: Save: chmod: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return Profile{}, fmt.Errorf("skychat profile: Save: rename: %w", err)
	}
	s.cached = norm
	return norm, nil
}

// Clear removes the published profile entirely — the "stop telling people
// who I am" action. Idempotent, and leaves no file behind so a fresh read
// sees the same state a never-configured visor has.
func (s *Store) Clear() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = Profile{}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("skychat profile: Clear: %w", err)
	}
	return nil
}

// Path reports where this store writes, for diagnostics.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}
