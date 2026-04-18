package dht

import (
	"sync"
	"time"
)

// DefaultItemTTL is how long an item lives without re-announcement.
const DefaultItemTTL = 2 * time.Hour

// Store holds mutable items keyed by their Target (SHA256(pk||salt)).
type Store struct {
	mu       sync.RWMutex
	items    map[NodeID]*storedItem
	maxItems int
	ttl      time.Duration
}

type storedItem struct {
	item     MutableItem
	storedAt time.Time
}

// NewStore creates a new item store.
func NewStore(maxItems int, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultItemTTL
	}
	return &Store{
		items:    make(map[NodeID]*storedItem),
		maxItems: maxItems,
		ttl:      ttl,
	}
}

// Put stores an item, enforcing monotonic sequence numbers.
// Returns ErrSeqNotMonotonic if the item's seq is <= the stored seq.
// Returns ErrInvalidSig if signature verification fails.
func (s *Store) Put(item MutableItem) error {
	if err := item.Verify(); err != nil {
		return err
	}

	target := item.Target()

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.items[target]; ok {
		if item.Seq <= existing.item.Seq {
			return ErrSeqNotMonotonic
		}
	}

	s.items[target] = &storedItem{
		item:     item,
		storedAt: time.Now(),
	}

	// Evict oldest items if over capacity.
	if s.maxItems > 0 && len(s.items) > s.maxItems {
		s.evictOldest()
	}

	return nil
}

// Get retrieves an item by target. Returns nil if not found or expired.
func (s *Store) Get(target NodeID) *MutableItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	si, ok := s.items[target]
	if !ok {
		return nil
	}
	if time.Since(si.storedAt) > s.ttl {
		return nil // expired
	}
	item := si.item // copy
	return &item
}

// Refresh resets the TTL for an item (re-announcement).
func (s *Store) Refresh(target NodeID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if si, ok := s.items[target]; ok {
		si.storedAt = time.Now()
	}
}

// Len returns the number of stored items.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// ExpireSweep removes all items whose TTL has elapsed.
func (s *Store) ExpireSweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for k, si := range s.items {
		if time.Since(si.storedAt) > s.ttl {
			delete(s.items, k)
			removed++
		}
	}
	return removed
}

// evictOldest removes the single oldest item. Caller must hold mu.
func (s *Store) evictOldest() {
	var oldestKey NodeID
	var oldestTime time.Time
	first := true
	for k, si := range s.items {
		if first || si.storedAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = si.storedAt
			first = false
		}
	}
	if !first {
		delete(s.items, oldestKey)
	}
}
