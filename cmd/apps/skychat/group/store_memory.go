//go:build js

// cmd/apps/skychat/group/store_memory.go: in-memory group record store for
// js/wasm, where bbolt can't compile (arch-const MaxAllocSize + mmap) and a
// browser tab has no filesystem. Drop-in for the bbolt store (store_bbolt.go):
// same *Store type + method set, and records are held as JSON bytes keyed by
// group ID so value semantics — independent copies on read, legacy-Admins
// normalization — match the bbolt path exactly.
//
// Ephemeral: a browser visor's group records reset on tab reload. That's fine
// for the federated model — every member is authoritative only for its own
// feed, so anything this visor published is retained by the peers that
// subscribed to it, and on reload it re-subscribes and history-replays. A
// durable variant can hydrate this map from + flush it to IndexedDB behind the
// identical API (in-memory stays the synchronous working set; IndexedDB is the
// async persistence tier — IndexedDB, not localStorage, for the quota headroom
// and native binary storage).

// Package group cmd/apps/skychat/group/store_memory.go c4-app-chat
package group

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Store is an in-memory group record store. Safe for concurrent use.
type Store struct {
	mu  sync.RWMutex
	kvs map[string][]byte // group ID -> JSON Record (mirrors the bbolt value shape)
	// joinReqs mirrors the bbolt joinReqsBucket, same composite
	// "<groupID>/<pkHex>" key so both backends iterate identically.
	joinReqs map[string][]byte
}

// OpenStore returns an in-memory store. path is accepted for signature parity
// with the bbolt store but ignored — there is no filesystem in js/wasm.
func OpenStore(path string) (*Store, error) {
	_ = path
	return &Store{
		kvs:      make(map[string][]byte),
		joinReqs: make(map[string][]byte),
	}, nil
}

// joinReqKey builds the composite key for a request. Mirrors the bbolt
// key layout exactly so the two backends stay wire-identical.
func joinReqKey(groupID string, pk cipher.PubKey) string {
	return groupID + "/" + pk.Hex()
}

// Close is a no-op (nothing to release).
func (s *Store) Close() error { return nil }

// Put writes (or replaces) the record for r.ID.
func (s *Store) Put(r Record) error {
	if r.ID == "" {
		return fmt.Errorf("group: Put: empty ID")
	}
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("group: marshal record: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kvs[r.ID] = body
	return nil
}

// Get returns the record for id and a boolean indicating presence.
func (s *Store) Get(id string) (Record, bool, error) {
	s.mu.RLock()
	raw, ok := s.kvs[id]
	s.mu.RUnlock()
	var r Record
	if !ok {
		return r, false, nil
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, true, err
	}
	// Match store_bbolt.Get: normalize legacy records (nil Admins) so IsAdmin
	// and admin-gated ops see a consistent shape.
	r.EnsureFounderInAdmins()
	r.EnsureKind()
	return r, true, nil
}

// List returns every persisted record in lexicographic ID order (matching
// bbolt's bucket-sorted iteration so order-dependent callers behave identically).
func (s *Store) List() ([]Record, error) {
	s.mu.RLock()
	ids := make([]string, 0, len(s.kvs))
	for id := range s.kvs {
		ids = append(ids, id)
	}
	raws := make([][]byte, len(ids))
	sort.Strings(ids)
	for i, id := range ids {
		raws[i] = s.kvs[id]
	}
	s.mu.RUnlock()
	out := make([]Record, 0, len(ids))
	for _, raw := range raws {
		var r Record
		if err := json.Unmarshal(raw, &r); err != nil {
			return out, err
		}
		r.EnsureFounderInAdmins()
		r.EnsureKind()
		out = append(out, r)
	}
	return out, nil
}

// Delete removes the record for id. No-op if absent.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.kvs, id)
	return nil
}

// SetStatus updates only the Status field of an existing record.
func (s *Store) SetStatus(id string, status Status) error {
	return s.update(id, func(r *Record) { r.Status = status })
}

// MarkMessage updates LastMessageAt to the given timestamp.
func (s *Store) MarkMessage(id string, ts time.Time) error {
	return s.update(id, func(r *Record) { r.LastMessageAt = ts })
}

// SetMembers replaces the Members slice.
func (s *Store) SetMembers(id string, members []cipher.PubKey) error {
	return s.update(id, func(r *Record) { r.Members = members })
}

// SetModeration persists a converged moderation snapshot.
func (s *Store) SetModeration(id string, st ModState) error {
	return s.update(id, func(r *Record) { st.applyTo(r) })
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
	s.joinReqs[joinReqKey(req.GroupID, req.PK)] = body
	return nil
}

// GetJoinRequest returns the request for (groupID, pk) if one exists.
func (s *Store) GetJoinRequest(groupID string, pk cipher.PubKey) (JoinRequest, bool, error) {
	s.mu.RLock()
	raw, ok := s.joinReqs[joinReqKey(groupID, pk)]
	s.mu.RUnlock()
	var req JoinRequest
	if !ok {
		return req, false, nil
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, true, err
	}
	return req, true, nil
}

// ListJoinRequests returns every request recorded against groupID,
// newest first.
func (s *Store) ListJoinRequests(groupID string) ([]JoinRequest, error) {
	prefix := groupID + "/"
	s.mu.RLock()
	raws := make([][]byte, 0, len(s.joinReqs))
	for k, v := range s.joinReqs {
		if strings.HasPrefix(k, prefix) {
			raws = append(raws, v)
		}
	}
	s.mu.RUnlock()
	out := make([]JoinRequest, 0, len(raws))
	for _, raw := range raws {
		var req JoinRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return out, err
		}
		out = append(out, req)
	}
	sortJoinRequests(out)
	return out, nil
}

// DeleteJoinRequests drops every request against groupID.
func (s *Store) DeleteJoinRequests(groupID string) error {
	prefix := groupID + "/"
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.joinReqs {
		if strings.HasPrefix(k, prefix) {
			delete(s.joinReqs, k)
		}
	}
	return nil
}

// update is the shared read-modify-write helper. Returns an error if no record
// exists for id.
func (s *Store) update(id string, fn func(*Record)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.kvs[id]
	if !ok {
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
	s.kvs[id] = body
	return nil
}
