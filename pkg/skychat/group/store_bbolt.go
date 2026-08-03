//go:build !js

// Package group pkg/skychat/group/store_bbolt.go c4-app-chat
// persistence for chat groups (native builds; bbolt can't compile under
// js/wasm, so the browser visor uses the in-memory store_memory.go — same API,
// wire-identical JSON records).
//
// One bucket "groups" with key = group UUID, value = JSON Record.
// Layout parallels pairing.Store deliberately so an operator who
// understands one understands both.
package group

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/bbolthealth"
)

const groupsBucket = "groups"

// joinReqsBucket holds pending + decided join requests, keyed
// "<groupID>/<requesterPKHex>". A flat bucket with a composite key
// rather than a nested bucket per group: group IDs are fixed-length
// UUIDs containing no "/", so a prefix scan on "<groupID>/" is exact,
// and a flat bucket keeps the Delete-on-group-removal path a single
// cursor walk instead of bucket bookkeeping.
const joinReqsBucket = "group_join_requests"

// joinReqKey builds the composite key for a request.
func joinReqKey(groupID string, pk cipher.PubKey) []byte {
	return []byte(groupID + "/" + pk.Hex())
}

// joinReqPrefix is the scan prefix for every request against a group.
func joinReqPrefix(groupID string) []byte {
	return []byte(groupID + "/")
}

// Store is a bolt-backed group record store. Safe for concurrent use.
type Store struct {
	db *bolt.DB
	mu sync.RWMutex
	// sealer encrypts the record's key material at rest. Required — see
	// store_seal.go for why there is no plaintext fallback.
	sealer *recordSealer
	// migrated counts records OpenStore re-sealed on this open. Written
	// once before the store is handed to any caller, so it needs no lock.
	migrated int
}

// OpenStore opens (or creates) the bolt file at path. The parent dir
// is created if missing.
//
// sk is the visor's secret key, used to derive the at-rest key that seals
// each group's AES key. It is required: a store opened without it would
// write group keys in the clear, which is the state store_seal.go exists
// to end. Records written by a different visor's key will not open.
func OpenStore(path string, sk cipher.SecKey) (*Store, error) {
	sealer, err := newRecordSealer(sk)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("group: mkdir parent: %w", err)
	}
	// Repair-on-corrupt: group records are recoverable from CXO
	// re-subscription; recreating fresh avoids visor crash-loop on
	// a corrupt groups.db.
	if err := bbolthealth.RepairIfCorrupt(path); err != nil {
		return nil, fmt.Errorf("group: integrity-check %s: %w", path, err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("group: open bolt: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, e := tx.CreateBucketIfNotExists([]byte(groupsBucket)); e != nil {
			return e
		}
		_, e := tx.CreateBucketIfNotExists([]byte(joinReqsBucket))
		return e
	}); err != nil {
		_ = db.Close() //nolint:errcheck
		return nil, fmt.Errorf("group: init bucket: %w", err)
	}
	st := &Store{db: db, sealer: sealer}
	// Re-seal anything the previous build left in the clear. Without this
	// pass, sealing would only apply to groups that happen to be written
	// again — a group nobody touches would keep its plaintext key in the
	// file forever, which is most of the exposure this is meant to remove.
	if n, mErr := st.sealLegacyRecords(); mErr != nil {
		_ = db.Close() //nolint:errcheck
		return nil, fmt.Errorf("group: sealing existing group keys: %w", mErr)
	} else if n > 0 {
		st.migrated = n
	}
	return st, nil
}

// MigratedRecords reports how many records OpenStore had to re-seal
// because they still held plaintext key material. Non-zero exactly once
// per upgrade; the visor logs it so an operator can see the migration
// happened rather than having to infer it.
func (s *Store) MigratedRecords() int {
	if s == nil {
		return 0
	}
	return s.migrated
}

// sealLegacyRecords rewrites every record whose key material is still in
// the clear. One write transaction, so an interrupted upgrade either
// re-seals the whole file or none of it.
func (s *Store) sealLegacyRecords() (int, error) {
	var n int
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(groupsBucket))
		type pending struct {
			key  []byte
			body []byte
		}
		var writes []pending
		if err := b.ForEach(func(k, v []byte) error {
			var r Record
			// Read the raw JSON directly: decodeRecord would try to open
			// blobs, and here we specifically want the pre-seal shape.
			if err := json.Unmarshal(v, &r); err != nil {
				return nil // leave undecodable rows alone
			}
			if !hasPlaintextKeys(r) {
				return nil
			}
			body, err := s.encodeRecord(r)
			if err != nil {
				return err
			}
			writes = append(writes, pending{key: append([]byte(nil), k...), body: body})
			return nil
		}); err != nil {
			return err
		}
		for _, w := range writes {
			if err := b.Put(w.key, w.body); err != nil {
				return err
			}
		}
		n = len(writes)
		return nil
	})
	return n, err
}

// Close releases the bolt file handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Put writes (or replaces) the record for r.ID.
//
// Replay watermarks are the one field that is merged rather than
// replaced. Every manager op is read-modify-Put, and the reconciler
// advances watermarks continuously in the background, so a Put built on
// a record read a moment earlier would carry stale watermarks and undo
// that progress — reopening the replay window for whatever target the
// reconciler had just pinned. A lost watermark never re-converges (see
// the retention note in replay_guard.go), and the resulting failure is
// the silent one the guard exists to prevent: a replayed RosterOpAdd
// re-admitting someone an admin evicted. No caller ever legitimately
// lowers a watermark, so merging costs nothing.
func (s *Store) Put(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == "" {
		return fmt.Errorf("group: Put: empty ID")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(groupsBucket))
		if raw := b.Get([]byte(r.ID)); raw != nil {
			var prev Record
			// Watermarks and Kind are not sealed, so the raw JSON is enough
			// here and a record we cannot open still contributes its
			// watermarks.
			if err := json.Unmarshal(raw, &prev); err == nil {
				r.MutationSeen = mergeWatermarks(prev.MutationSeen, r.MutationSeen)
				// Enforced here rather than in each caller because this is
				// the one chokepoint every write goes through — see
				// checkKindStable for why a channel must stay one.
				if err := checkKindStable(r.ID, prev.Kind, r.Kind); err != nil {
					return err
				}
			}
		}
		body, err := s.encodeRecord(r)
		if err != nil {
			return fmt.Errorf("group: marshal record: %w", err)
		}
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
		return s.decodeRecord(raw, &r)
	})
	// Legacy records persisted before the Admins field existed have
	// Admins == nil. Normalize on every read so IsAdmin and admin-
	// gated manager ops see a consistent shape. Idempotent for new
	// records that already have the founder explicit.
	r.EnsureFounderInAdmins()
	r.EnsureKind()
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
			if err := s.decodeRecord(v, &r); err != nil {
				return err
			}
			// Mirror Get's legacy-record normalization so any
			// caller iterating List sees the same admin-shape
			// and group-kind invariants Get returns.
			r.EnsureFounderInAdmins()
			r.EnsureKind()
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

// SetModeration persists a converged moderation snapshot.
func (s *Store) SetModeration(id string, st ModState) error {
	return s.update(id, func(r *Record) { st.applyTo(r) })
}

// SetKeyState persists a rotated group key plus the ring of retired keys.
// Read-modify-write inside the transaction, like every other setter here,
// so it cannot clobber a concurrent roster or moderation change.
func (s *Store) SetKeyState(id string, st KeyState) error {
	return s.update(id, func(r *Record) { st.applyTo(r) })
}

// SetMutationSeen persists the replay guard's watermarks. Merges rather
// than replaces: a snapshot taken by one reconciler path can be written
// while another has already advanced a different target, and the guard
// must never move a watermark backwards.
func (s *Store) SetMutationSeen(id string, seen map[string]time.Time) error {
	return s.update(id, func(r *Record) { r.MutationSeen = mergeWatermarks(r.MutationSeen, seen) })
}

// PutJoinRequest writes (or replaces) a join request.
func (s *Store) PutJoinRequest(req JoinRequest) error {
	if req.GroupID == "" || req.PK == (cipher.PubKey{}) {
		return fmt.Errorf("group: PutJoinRequest: group id and pk required")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("group: marshal join request: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(joinReqsBucket)).Put(joinReqKey(req.GroupID, req.PK), body)
	})
}

// GetJoinRequest returns the request for (groupID, pk) if one exists.
func (s *Store) GetJoinRequest(groupID string, pk cipher.PubKey) (JoinRequest, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var (
		req   JoinRequest
		found bool
	)
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(joinReqsBucket)).Get(joinReqKey(groupID, pk))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &req)
	})
	return req, found, err
}

// ListJoinRequests returns every request recorded against groupID,
// newest first so an admin's queue reads top-down.
func (s *Store) ListJoinRequests(groupID string) ([]JoinRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []JoinRequest
	err := s.db.View(func(tx *bolt.Tx) error {
		cur := tx.Bucket([]byte(joinReqsBucket)).Cursor()
		prefix := joinReqPrefix(groupID)
		for k, v := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cur.Next() {
			var req JoinRequest
			if err := json.Unmarshal(v, &req); err != nil {
				return err
			}
			out = append(out, req)
		}
		return nil
	})
	sortJoinRequests(out)
	return out, err
}

// DeleteJoinRequests drops every request against groupID. Called when
// the group is deleted or left so the queue doesn't outlive its group.
func (s *Store) DeleteJoinRequests(groupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(joinReqsBucket))
		cur := b.Cursor()
		prefix := joinReqPrefix(groupID)
		// Collect first, then delete: mutating through a cursor while
		// iterating it is undefined in bbolt.
		var keys [][]byte
		for k, _ := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cur.Next() {
			keys = append(keys, append([]byte(nil), k...))
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
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
		// Open the key material before the mutation and re-seal after: a
		// setter that only touches the roster must not drop the group key,
		// and one that replaces it (SetKeyState) hands us plaintext.
		if err := s.decodeRecord(raw, &r); err != nil {
			return err
		}
		fn(&r)
		body, err := s.encodeRecord(r)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), body)
	})
}
