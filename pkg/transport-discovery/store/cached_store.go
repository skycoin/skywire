// Package store pkg/transport-discovery/store/cached_store.go
package store

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// cachedStore wraps a PostgreSQL store with an in-memory cache layer.
// All reads are served from memory, all writes go to PostgreSQL and update memory.
type cachedStore struct {
	pgStore   TransportStore                 // underlying PostgreSQL store
	cache     map[uuid.UUID]*transport.Entry // in-memory cache indexed by transport ID
	edgeIndex map[cipher.PubKey][]uuid.UUID  // index for quick edge lookups
	mu        sync.RWMutex                   // protects cache and edgeIndex
	closeC    chan struct{}
}

// NewCachedStore creates a new cached store wrapping the provided PostgreSQL store.
// It loads all existing data from PostgreSQL into memory on initialization.
func NewCachedStore(pgStore TransportStore) (TransportStore, error) {
	s := &cachedStore{
		pgStore:   pgStore,
		cache:     make(map[uuid.UUID]*transport.Entry),
		edgeIndex: make(map[cipher.PubKey][]uuid.UUID),
		closeC:    make(chan struct{}),
	}

	// Load all existing transports from PostgreSQL into memory
	if err := s.loadAllFromPG(context.Background()); err != nil {
		return nil, err
	}

	return s, nil
}

// loadAllFromPG loads all transports from PostgreSQL into the in-memory cache
func (s *cachedStore) loadAllFromPG(ctx context.Context) error {
	// Get all transports from PostgreSQL (including self-transports)
	entries, err := s.pgStore.GetAllTransports(ctx, true)
	if err != nil {
		// If no transports found, that's okay - we start with empty cache
		if err == ErrTransportNotFound {
			return nil
		}
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Populate cache and edge index
	for _, entry := range entries {
		s.cache[entry.ID] = entry
		s.addToEdgeIndex(entry)
	}

	return nil
}

// addToEdgeIndex adds a transport entry to the edge index (caller must hold lock)
func (s *cachedStore) addToEdgeIndex(entry *transport.Entry) {
	for _, edge := range entry.Edges {
		s.edgeIndex[edge] = append(s.edgeIndex[edge], entry.ID)
	}
}

// removeFromEdgeIndex removes a transport entry from the edge index (caller must hold lock)
func (s *cachedStore) removeFromEdgeIndex(entry *transport.Entry) {
	for _, edge := range entry.Edges {
		ids := s.edgeIndex[edge]
		for i, id := range ids {
			if id == entry.ID {
				// Remove this ID from the slice
				s.edgeIndex[edge] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		// Clean up empty slices
		if len(s.edgeIndex[edge]) == 0 {
			delete(s.edgeIndex, edge)
		}
	}
}

// RegisterTransport writes to PostgreSQL and updates the in-memory cache
func (s *cachedStore) RegisterTransport(ctx context.Context, sEntry *transport.SignedEntry) error {
	// Write to PostgreSQL first
	if err := s.pgStore.RegisterTransport(ctx, sEntry); err != nil {
		return err
	}

	// Update in-memory cache
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := sEntry.Entry

	// If this ID already exists, remove old edge index entries
	if existingEntry, exists := s.cache[entry.ID]; exists {
		s.removeFromEdgeIndex(existingEntry)
	}

	// Add/update cache
	s.cache[entry.ID] = entry
	s.addToEdgeIndex(entry)

	return nil
}

// DeregisterTransport removes from PostgreSQL and updates the in-memory cache
func (s *cachedStore) DeregisterTransport(ctx context.Context, id uuid.UUID) error {
	// Get the entry before deleting (for edge index cleanup)
	s.mu.RLock()
	entry, exists := s.cache[id]
	s.mu.RUnlock()

	if !exists {
		// Not in cache, might still be in PG (shouldn't happen, but handle gracefully)
		return s.pgStore.DeregisterTransport(ctx, id)
	}

	// Delete from PostgreSQL first
	if err := s.pgStore.DeregisterTransport(ctx, id); err != nil {
		return err
	}

	// Remove from in-memory cache
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.cache, id)
	s.removeFromEdgeIndex(entry)

	return nil
}

// GetTransportByID reads from in-memory cache
func (s *cachedStore) GetTransportByID(_ context.Context, id uuid.UUID) (*transport.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.cache[id]
	if !ok {
		return nil, ErrTransportNotFound
	}

	return entry, nil
}

// GetTransportsByEdge reads from in-memory cache using the edge index
func (s *cachedStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids, ok := s.edgeIndex[pk]
	if !ok || len(ids) == 0 {
		return nil, ErrTransportNotFound
	}

	entries := make([]*transport.Entry, 0, len(ids))
	for _, id := range ids {
		if entry, exists := s.cache[id]; exists {
			entries = append(entries, entry)
		}
	}

	if len(entries) == 0 {
		return nil, ErrTransportNotFound
	}

	return entries, nil
}

// GetNumberOfTransports reads from in-memory cache
func (s *cachedStore) GetNumberOfTransports(_ context.Context) (map[types.Type]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	response := make(map[types.Type]int)
	for _, entry := range s.cache {
		response[entry.Type]++
	}

	return response, nil
}

// GetAllTransports reads from in-memory cache
func (s *cachedStore) GetAllTransports(_ context.Context, selfTransports bool) ([]*transport.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]*transport.Entry, 0, len(s.cache))
	for _, entry := range s.cache {
		if !selfTransports && entry.Edges[0] == entry.Edges[1] {
			continue
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// Close closes both the cache and the underlying PostgreSQL store
func (s *cachedStore) Close() {
	close(s.closeC)
	s.pgStore.Close()
}
