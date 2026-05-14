// Package group — cmd/apps/skychat/group/store.go: bbolt-backed
// persistence for chat groups.
//
// One bucket "groups" with key = group UUID, value = JSON Record.
// Layout parallels pairing.Store deliberately so an operator who
// understands one understands both.
package group

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/skycoin/skywire/pkg/cipher"
)

const groupsBucket = "groups"

// Store is a bolt-backed group record store. Safe for concurrent use.
type Store struct {
	db *bolt.DB
	mu sync.RWMutex
}

// OpenStore opens (or creates) the bolt file at path. The parent dir
// is created if missing.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("group: mkdir parent: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("group: open bolt: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists([]byte(groupsBucket))
		return e
	}); err != nil {
		_ = db.Close() //nolint:errcheck
		return nil, fmt.Errorf("group: init bucket: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the bolt file handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Put writes (or replaces) the record for r.ID.
func (s *Store) Put(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == "" {
		return fmt.Errorf("group: Put: empty ID")
	}
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("group: marshal record: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(groupsBucket))
		return b.Put([]byte(r.ID), body)
	})
}

// Get returns the record for id and a boolean indicating presence.
func (s *Store) Get(id string) (Record, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var (
		r     Record
		found bool
	)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(groupsBucket))
		raw := b.Get([]byte(id))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &r)
	})
	// Legacy records persisted before the Admins field existed have
	// Admins == nil. Normalize on every read so IsAdmin and admin-
	// gated manager ops see a consistent shape. Idempotent for new
	// records that already have the founder explicit.
	r.EnsureFounderInAdmins()
	return r, found, err
}

// List returns every persisted record. Order is bolt's bucket-sorted
// order (lexicographic on the UUID string).
func (s *Store) List() ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Record
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(groupsBucket))
		return b.ForEach(func(_, v []byte) error {
			var r Record
			if err := json.Unmarshal(v, &r); err != nil {
				return err
			}
			// Mirror Get's legacy-record normalization so any
			// caller iterating List sees the same admin-shape
			// invariant Get returns.
			r.EnsureFounderInAdmins()
			out = append(out, r)
			return nil
		})
	})
	return out, err
}

// Delete removes the record for id. No-op if absent.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(groupsBucket))
		return b.Delete([]byte(id))
	})
}

// SetStatus updates only the Status field of an existing record.
// Returns an error if no record exists for id.
func (s *Store) SetStatus(id string, status Status) error {
	return s.update(id, func(r *Record) {
		r.Status = status
	})
}

// MarkMessage updates LastMessageAt to the given timestamp.
func (s *Store) MarkMessage(id string, ts time.Time) error {
	return s.update(id, func(r *Record) {
		r.LastMessageAt = ts
	})
}

// SetMembers replaces the Members slice. Owner-side: drives
// Publisher.SetAllowlist on the next push. Member-side: updates the
// "who's in this room" display.
func (s *Store) SetMembers(id string, members []cipher.PubKey) error {
	return s.update(id, func(r *Record) {
		r.Members = members
	})
}

// update is the shared read-modify-write helper. Acquires the write
// lock, loads the record, applies fn, and saves. Returns an error
// if no record exists for id.
func (s *Store) update(id string, fn func(*Record)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(groupsBucket))
		raw := b.Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("group: no record for %s", id)
		}
		var r Record
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		fn(&r)
		body, err := json.Marshal(&r)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), body)
	})
}
