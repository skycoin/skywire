package dht

import (
	"errors"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// DefaultItemTTL is how long an item lives without re-announcement.
const DefaultItemTTL = 2 * time.Hour

// Trust tiers determine eviction behavior.
type TrustTier int

const (
	// TierWhitelisted — hardcoded PKs, full replication, no eviction.
	TierWhitelisted TrustTier = iota
	// TierTrusted — active PKs, full replication unless abuse.
	TierTrusted
	// TierPublic — any PK, pool + LRU eviction.
	TierPublic
)

// ErrRateLimited is returned when a public PK exceeds its item quota.
var ErrRateLimited = errors.New("dht: rate limit exceeded for public key")

// TrustPolicy classifies publisher PKs into trust tiers.
type TrustPolicy struct {
	whitelisted map[cipher.PubKey]struct{}
	trusted     map[cipher.PubKey]struct{}
}

// NewTrustPolicy creates a trust policy from config lists.
func NewTrustPolicy(whitelisted, trusted []cipher.PubKey) *TrustPolicy {
	tp := &TrustPolicy{
		whitelisted: make(map[cipher.PubKey]struct{}, len(whitelisted)),
		trusted:     make(map[cipher.PubKey]struct{}, len(trusted)),
	}
	for _, pk := range whitelisted {
		tp.whitelisted[pk] = struct{}{}
	}
	for _, pk := range trusted {
		tp.trusted[pk] = struct{}{}
	}
	return tp
}

// Classify returns the trust tier for a publisher PK.
func (tp *TrustPolicy) Classify(pk cipher.PubKey) TrustTier {
	if _, ok := tp.whitelisted[pk]; ok {
		return TierWhitelisted
	}
	if _, ok := tp.trusted[pk]; ok {
		return TierTrusted
	}
	return TierPublic
}

// Store holds mutable items keyed by their Target (SHA256(pk||salt)).
// Items are classified into trust tiers for eviction decisions.
type Store struct {
	mu             sync.RWMutex
	items          map[NodeID]*storedItem
	maxItems       int // global capacity
	publicPoolSize int // max Tier 3 items
	rateLimitPerPK int // max items per public PK
	ttl            time.Duration
	trust          *TrustPolicy
}

type storedItem struct {
	item     MutableItem
	tier     TrustTier
	storedAt time.Time
}

// NewStore creates a new item store with trust policy.
func NewStore(maxItems int, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultItemTTL
	}
	return &Store{
		items:          make(map[NodeID]*storedItem),
		maxItems:       maxItems,
		publicPoolSize: 5000,
		rateLimitPerPK: 50,
		ttl:            ttl,
		trust:          NewTrustPolicy(nil, nil),
	}
}

// SetTrustPolicy updates the trust policy and pool limits.
func (s *Store) SetTrustPolicy(trust *TrustPolicy, publicPoolSize, rateLimitPerPK int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trust = trust
	if publicPoolSize > 0 {
		s.publicPoolSize = publicPoolSize
	}
	if rateLimitPerPK > 0 {
		s.rateLimitPerPK = rateLimitPerPK
	}
}

// Put stores an item, enforcing monotonic sequence numbers, trust
// tier eviction rules, and per-PK rate limits for public items.
func (s *Store) Put(item MutableItem) error {
	if err := item.Verify(); err != nil {
		return err
	}

	target := item.Target()
	tier := s.trust.Classify(item.K)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Enforce monotonic sequence.
	if existing, ok := s.items[target]; ok {
		if item.Seq <= existing.item.Seq {
			return ErrSeqNotMonotonic
		}
	}

	// Rate limit for public PKs.
	if tier == TierPublic && s.rateLimitPerPK > 0 {
		count := 0
		for _, si := range s.items {
			if si.item.K == item.K {
				count++
			}
		}
		// Allow update of existing item (same target) without counting.
		if _, isUpdate := s.items[target]; !isUpdate && count >= s.rateLimitPerPK {
			return ErrRateLimited
		}
	}

	s.items[target] = &storedItem{
		item:     item,
		tier:     tier,
		storedAt: time.Now(),
	}

	// Evict public items if the public pool is over capacity.
	if tier == TierPublic {
		s.evictPublicPool()
	}

	// Global capacity check.
	if s.maxItems > 0 && len(s.items) > s.maxItems {
		s.evictOldestPublic()
	}

	return nil
}

// Get retrieves an item by target. Returns nil if not found or expired.
// Whitelisted and trusted items never expire via TTL.
func (s *Store) Get(target NodeID) *MutableItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	si, ok := s.items[target]
	if !ok {
		return nil
	}
	// Whitelisted and trusted items don't expire.
	if si.tier == TierPublic && time.Since(si.storedAt) > s.ttl {
		return nil
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

// CountByTier returns item counts per trust tier.
func (s *Store) CountByTier() (whitelisted, trusted, public int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, si := range s.items {
		switch si.tier {
		case TierWhitelisted:
			whitelisted++
		case TierTrusted:
			trusted++
		case TierPublic:
			public++
		}
	}
	return
}

// ExpireSweep removes expired public items. Whitelisted and trusted
// items are never expired by TTL.
func (s *Store) ExpireSweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for k, si := range s.items {
		if si.tier == TierPublic && time.Since(si.storedAt) > s.ttl {
			delete(s.items, k)
			removed++
		}
	}
	return removed
}

// evictPublicPool removes the oldest public item if the public pool
// exceeds capacity. Caller must hold mu.
func (s *Store) evictPublicPool() {
	publicCount := 0
	for _, si := range s.items {
		if si.tier == TierPublic {
			publicCount++
		}
	}
	for publicCount > s.publicPoolSize {
		s.evictOldestPublic()
		publicCount--
	}
}

// evictOldestPublic removes the single oldest public item. Whitelisted
// and trusted items are never evicted. Caller must hold mu.
func (s *Store) evictOldestPublic() {
	var oldestKey NodeID
	var oldestTime time.Time
	found := false
	for k, si := range s.items {
		if si.tier != TierPublic {
			continue // never evict whitelisted or trusted
		}
		if !found || si.storedAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = si.storedAt
			found = true
		}
	}
	if found {
		delete(s.items, oldestKey)
	}
}
