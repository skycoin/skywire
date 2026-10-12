// Package privacy pkg/skychat/privacy/store.go c4-app-chat
//
// Package privacy keeps who may message this visor: a block list, and, when
// approval is on, the people already let in. Local, never sent to a peer.
package privacy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// MaxEntries bounds each list, since the file is rewritten whole on a change.
const MaxEntries = 5000

// ErrFull is returned when a list would grow past MaxEntries.
var ErrFull = errors.New("skychat privacy: list is full")

var pkPattern = regexp.MustCompile(`^[0-9a-f]{66}$`)

// Verdict is what to do with a message from a peer.
type Verdict int

const (
	// Allow delivers the message as usual.
	Allow Verdict = iota
	// Request keeps the message but shows it as a request to accept or block.
	Request
	// Reject drops the message: the peer is blocked.
	Reject
)

type onDisk struct {
	Approval bool     `json:"approval"`
	Blocked  []string `json:"blocked"`
	Accepted []string `json:"accepted"`
}

// Store is the on-disk privacy state. Safe for concurrent use.
type Store struct {
	mu       sync.RWMutex
	path     string
	approval bool
	blocked  map[string]bool
	accepted map[string]bool
}

// OpenStore opens the store at path. A missing file is an empty store.
func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("skychat privacy: OpenStore: path required")
	}
	s := &Store{path: path, blocked: map[string]bool{}, accepted: map[string]bool{}}
	raw, err := os.ReadFile(path) //nolint:gosec // operator-configured app path
	switch {
	case os.IsNotExist(err):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("skychat privacy: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return s, nil
	}
	var d onDisk
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("skychat privacy: parse %s: %w", path, err)
	}
	s.approval = d.Approval
	for _, pk := range d.Blocked {
		if key, ok := normalize(pk); ok {
			s.blocked[key] = true
		}
	}
	for _, pk := range d.Accepted {
		if key, ok := normalize(pk); ok {
			s.accepted[key] = true
		}
	}
	return s, nil
}

// Verdict decides a message from pk. known reports whether pk is let in for a
// reason this store does not hold, such as a name in the address book.
func (s *Store) Verdict(pk string, known bool) Verdict {
	if s == nil {
		return Allow
	}
	key := strings.ToLower(strings.TrimSpace(pk))
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch {
	case s.blocked[key]:
		return Reject
	case s.approval && !known && !s.accepted[key]:
		return Request
	default:
		return Allow
	}
}

// Approval reports whether new peers must be accepted before they are let in.
func (s *Store) Approval() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.approval
}

// SetApproval turns approval on or off. Turning it on accepts every key in
// existing, so conversations already under way do not become requests.
func (s *Store) SetApproval(on bool, existing []string) error {
	if s == nil {
		return errors.New("skychat privacy: no store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if on && !s.approval {
		for _, pk := range existing {
			if key, ok := normalize(pk); ok && len(s.accepted) < MaxEntries {
				s.accepted[key] = true
			}
		}
	}
	s.approval = on
	return s.flushLocked()
}

// Accept lets pk in when approval is on.
func (s *Store) Accept(pk string) error {
	return s.setIn(s.acceptedMap, pk, true)
}

// SetBlocked blocks or unblocks pk.
func (s *Store) SetBlocked(pk string, blocked bool) error {
	return s.setIn(s.blockedMap, pk, blocked)
}

// Accepted reports whether pk was let in explicitly.
func (s *Store) Accepted(pk string) bool {
	return s.has(s.acceptedMap, pk)
}

// Blocked reports whether pk is blocked.
func (s *Store) Blocked(pk string) bool {
	return s.has(s.blockedMap, pk)
}

// BlockedList returns the blocked keys, sorted.
func (s *Store) BlockedList() []string {
	out := []string{}
	if s == nil {
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for pk := range s.blocked {
		out = append(out, pk)
	}
	sort.Strings(out)
	return out
}

func (s *Store) acceptedMap() map[string]bool { return s.accepted }
func (s *Store) blockedMap() map[string]bool  { return s.blocked }

func (s *Store) has(set func() map[string]bool, pk string) bool {
	if s == nil {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(pk))
	s.mu.RLock()
	defer s.mu.RUnlock()
	return set()[key]
}

func (s *Store) setIn(set func() map[string]bool, pk string, on bool) error {
	if s == nil {
		return errors.New("skychat privacy: no store")
	}
	key, ok := normalize(pk)
	if !ok {
		return fmt.Errorf("skychat privacy: %q is not a public key", pk)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := set()
	if m[key] == on {
		return nil
	}
	if on {
		if len(m) >= MaxEntries {
			return ErrFull
		}
		m[key] = true
	} else {
		delete(m, key)
	}
	return s.flushLocked()
}

// flushLocked writes the store through a temp file and a rename, so a crash
// mid-write cannot leave a file OpenStore refuses. Caller holds the lock.
func (s *Store) flushLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("skychat privacy: create dir: %w", err)
	}
	d := onDisk{Approval: s.approval, Blocked: keys(s.blocked), Accepted: keys(s.accepted)}
	blob, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("skychat privacy: encode: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return fmt.Errorf("skychat privacy: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp) //nolint:errcheck // best-effort cleanup
		return fmt.Errorf("skychat privacy: replace: %w", err)
	}
	return nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func normalize(pk string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(pk))
	return key, pkPattern.MatchString(key)
}
