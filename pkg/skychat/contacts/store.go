// Package contacts pkg/skychat/contacts/store.go c4-app-chat
// the address book: what THIS visor's operator calls each public key.
//
// # Why this is not localStorage any more
//
// A nickname used to live in the chat page's localStorage, which made it
// invisible to everything that is not that page. The two surfaces that most
// need a name are both outside it: a notification, whose title is composed in
// Go before any UI is involved, and a phone's native call screen, which is
// Kotlin and cannot read a WebView's storage. Both showed 66 hex characters
// for a person the user had already named — and a nickname also died with a
// cache clear, and never reached the same visor's other UI.
//
// So the name moves to where every consumer can reach it: one small file
// beside the profile, read through the app's HTTP surface.
//
// # What a nickname is, and why it wins
//
// It is the operator's private label for a key, chosen by them and stored
// only here. It is NOT the profile a peer publishes about itself
// (pkg/skychat/profile) — that is the peer's claim, fetched over the network,
// and a label the user chose must never be replaceable by the person it
// labels. The UI resolves in that order (nickname, then published name, then
// the shortened key) and writes a fetched name in here only when there is no
// nickname yet, which is what lets one map serve both.
//
// Local by design: nothing here is published, gossiped or sent to a peer.
package contacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/skychat/profile"
)

// pkPattern is the only key shape accepted. Storing anything else would let a
// caller fill the file with arbitrary strings that no display path can ever
// match against a real peer.
var pkPattern = regexp.MustCompile(`^[0-9a-f]{66}$`)

// MaxEntries bounds the address book. Generous for a human's contact list, and
// the reason it exists is that this file is written whole on every change: an
// unbounded map turns one rename into an ever-growing rewrite.
const MaxEntries = 5000

// ErrFull is returned when a new name would exceed [MaxEntries]. Renaming an
// existing contact always succeeds.
var ErrFull = errors.New("skychat contacts: address book is full")

// Store is the on-disk address book: public key → the name this operator gave
// it. Safe for concurrent use — the HTTP surface reads it on every render
// while a rename may be landing.
type Store struct {
	mu    sync.RWMutex
	path  string
	names map[string]string
}

// OpenStore opens (or prepares) the address book at path.
//
// A missing file is not an error — an operator who has named nobody has an
// empty book. A file that exists but cannot be parsed IS one: starting blank
// would look exactly like every nickname having been silently discarded.
func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("skychat contacts: OpenStore: path required")
	}
	s := &Store{path: path, names: make(map[string]string)}

	raw, err := os.ReadFile(path) //nolint:gosec // operator-configured app path
	switch {
	case os.IsNotExist(err):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("skychat contacts: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return s, nil
	}
	var onDisk map[string]string
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		return nil, fmt.Errorf("skychat contacts: parse %s: %w", path, err)
	}
	for pk, name := range onDisk {
		if key, clean, ok := normalize(pk, name); ok {
			s.names[key] = clean
		}
	}
	return s, nil
}

// Name returns the operator's name for pk, or "" when there is none.
func (s *Store) Name(pk string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.names[strings.ToLower(strings.TrimSpace(pk))]
}

// All returns a copy of the whole book.
func (s *Store) All() map[string]string {
	out := make(map[string]string)
	if s == nil {
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for pk, name := range s.names {
		out[pk] = name
	}
	return out
}

// Set names pk, or forgets it when name is empty. Returns the stored form.
func (s *Store) Set(pk, name string) (string, error) {
	if s == nil {
		return "", errors.New("skychat contacts: no store")
	}
	key := strings.ToLower(strings.TrimSpace(pk))
	if !pkPattern.MatchString(key) {
		return "", fmt.Errorf("skychat contacts: %q is not a public key", pk)
	}
	clean := profile.NormalizeName(name)

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case clean == "":
		delete(s.names, key)
	default:
		if _, exists := s.names[key]; !exists && len(s.names) >= MaxEntries {
			return "", ErrFull
		}
		s.names[key] = clean
	}
	return clean, s.flushLocked()
}

// Merge adds every name that is not already set, and reports how many landed.
//
// Never overwrites: this is the path a UI's own older store is imported
// through (localStorage, historically), and an import must not be able to
// revert a rename the operator made since. Malformed entries are skipped
// rather than failing the batch — one bad key in a migration should not cost
// the operator the other 200.
func (s *Store) Merge(names map[string]string) (int, error) {
	if s == nil {
		return 0, errors.New("skychat contacts: no store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	added := 0
	for pk, name := range names {
		key, clean, ok := normalize(pk, name)
		if !ok {
			continue
		}
		if _, exists := s.names[key]; exists {
			continue
		}
		if len(s.names) >= MaxEntries {
			break
		}
		s.names[key] = clean
		added++
	}
	if added == 0 {
		return 0, nil
	}
	return added, s.flushLocked()
}

// Path is where the book lives, for logs and diagnostics.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// flushLocked writes the whole book. Caller holds the write lock.
//
// Write-temp-then-rename, because the alternative is that a crash mid-write
// leaves a truncated file that OpenStore then refuses to parse — turning a
// power cut into "all my contacts are gone, and the app won't start the
// feature".
func (s *Store) flushLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("skychat contacts: create dir: %w", err)
	}
	blob, err := json.MarshalIndent(s.names, "", "  ")
	if err != nil {
		return fmt.Errorf("skychat contacts: encode: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return fmt.Errorf("skychat contacts: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp) //nolint:errcheck // best-effort cleanup
		return fmt.Errorf("skychat contacts: replace: %w", err)
	}
	return nil
}

// normalize validates a key and cleans a name, reporting whether the pair is
// storable at all.
func normalize(pk, name string) (key, clean string, ok bool) {
	key = strings.ToLower(strings.TrimSpace(pk))
	if !pkPattern.MatchString(key) {
		return "", "", false
	}
	clean = profile.NormalizeName(name)
	return key, clean, clean != ""
}
