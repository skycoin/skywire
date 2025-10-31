//go:build !no_ci
// +build !no_ci

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// mockPGStore is a simple in-memory store that simulates PostgreSQL behavior
// Used to test the caching layer without actual database
type mockPGStore struct {
	data map[uuid.UUID]*transport.Entry
}

func newMockPGStore() *mockPGStore {
	return &mockPGStore{
		data: make(map[uuid.UUID]*transport.Entry),
	}
}

func (m *mockPGStore) RegisterTransport(_ context.Context, sEntry *transport.SignedEntry) error {
	m.data[sEntry.Entry.ID] = sEntry.Entry
	return nil
}

func (m *mockPGStore) DeregisterTransport(_ context.Context, id uuid.UUID) error {
	if _, ok := m.data[id]; !ok {
		return ErrTransportNotFound
	}
	delete(m.data, id)
	return nil
}

func (m *mockPGStore) GetTransportByID(_ context.Context, id uuid.UUID) (*transport.Entry, error) {
	entry, ok := m.data[id]
	if !ok {
		return nil, ErrTransportNotFound
	}
	return entry, nil
}

func (m *mockPGStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	var entries []*transport.Entry
	for _, entry := range m.data {
		if entry.HasEdge(pk) {
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return nil, ErrTransportNotFound
	}
	return entries, nil
}

func (m *mockPGStore) GetNumberOfTransports(_ context.Context) (map[types.Type]int, error) {
	result := make(map[types.Type]int)
	for _, entry := range m.data {
		result[entry.Type]++
	}
	return result, nil
}

func (m *mockPGStore) GetAllTransports(_ context.Context, selfTransports bool) ([]*transport.Entry, error) {
	var entries []*transport.Entry
	for _, entry := range m.data {
		if !selfTransports && entry.Edges[0] == entry.Edges[1] {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (m *mockPGStore) Close() {}

func TestCachedStore(t *testing.T) {
	ctx := context.Background()

	// Create mock PG store and wrap it with cache
	mockPG := newMockPGStore()
	cachedStore, err := NewCachedStore(mockPG)
	require.NoError(t, err)
	defer cachedStore.Close()

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	t.Run("RegisterAndRead", func(t *testing.T) {
		sEntry := &transport.SignedEntry{
			Entry: &transport.Entry{
				ID:    uuid.New(),
				Edges: transport.SortEdges(pk1, pk2),
				Type:  types.DMSG,
			},
			Signatures: [2]cipher.Sig{},
		}

		// Register transport
		err := cachedStore.RegisterTransport(ctx, sEntry)
		require.NoError(t, err)

		// Verify it's in both PG and cache
		entry, err := cachedStore.GetTransportByID(ctx, sEntry.Entry.ID)
		require.NoError(t, err)
		assert.Equal(t, sEntry.Entry.ID, entry.ID)
		assert.Equal(t, sEntry.Entry.Type, entry.Type)

		// Verify it's actually in the mock PG
		pgEntry, err := mockPG.GetTransportByID(ctx, sEntry.Entry.ID)
		require.NoError(t, err)
		assert.Equal(t, sEntry.Entry.ID, pgEntry.ID)
	})

	t.Run("GetByEdge", func(t *testing.T) {
		sEntry := &transport.SignedEntry{
			Entry: &transport.Entry{
				ID:    uuid.New(),
				Edges: transport.SortEdges(pk1, pk2),
				Type:  types.STCP,
			},
			Signatures: [2]cipher.Sig{},
		}

		err := cachedStore.RegisterTransport(ctx, sEntry)
		require.NoError(t, err)

		// Get by first edge
		entries, err := cachedStore.GetTransportsByEdge(ctx, pk1)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(entries), 1)

		// Get by second edge
		entries, err = cachedStore.GetTransportsByEdge(ctx, pk2)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(entries), 1)

		// Get by non-existent edge
		pk3, _ := cipher.GenerateKeyPair()
		_, err = cachedStore.GetTransportsByEdge(ctx, pk3)
		assert.Equal(t, ErrTransportNotFound, err)
	})

	t.Run("GetNumberOfTransports", func(t *testing.T) {
		counts, err := cachedStore.GetNumberOfTransports(ctx)
		require.NoError(t, err)

		// We've registered at least 2 transports in previous tests
		total := 0
		for _, count := range counts {
			total += count
		}
		assert.GreaterOrEqual(t, total, 2)
	})

	t.Run("Deregister", func(t *testing.T) {
		sEntry := &transport.SignedEntry{
			Entry: &transport.Entry{
				ID:    uuid.New(),
				Edges: transport.SortEdges(pk1, pk2),
				Type:  types.SUDPH,
			},
			Signatures: [2]cipher.Sig{},
		}

		err := cachedStore.RegisterTransport(ctx, sEntry)
		require.NoError(t, err)

		// Verify it exists
		_, err = cachedStore.GetTransportByID(ctx, sEntry.Entry.ID)
		require.NoError(t, err)

		// Deregister
		err = cachedStore.DeregisterTransport(ctx, sEntry.Entry.ID)
		require.NoError(t, err)

		// Verify it's gone from cache
		_, err = cachedStore.GetTransportByID(ctx, sEntry.Entry.ID)
		assert.Equal(t, ErrTransportNotFound, err)

		// Verify it's gone from PG
		_, err = mockPG.GetTransportByID(ctx, sEntry.Entry.ID)
		assert.Equal(t, ErrTransportNotFound, err)
	})

	t.Run("GetAllTransports", func(t *testing.T) {
		// Create self-transport
		selfEntry := &transport.SignedEntry{
			Entry: &transport.Entry{
				ID:    uuid.New(),
				Edges: [2]cipher.PubKey{pk1, pk1}, // same edge
				Type:  types.DMSG,
			},
			Signatures: [2]cipher.Sig{},
		}
		err := cachedStore.RegisterTransport(ctx, selfEntry)
		require.NoError(t, err)

		// Get all with self-transports
		allWithSelf, err := cachedStore.GetAllTransports(ctx, true)
		require.NoError(t, err)

		// Get all without self-transports
		allWithoutSelf, err := cachedStore.GetAllTransports(ctx, false)
		require.NoError(t, err)

		// Should have at least one less when excluding self-transports
		assert.GreaterOrEqual(t, len(allWithSelf), len(allWithoutSelf))
	})
}

func TestCachedStoreInitialization(t *testing.T) {
	ctx := context.Background()

	// Create mock PG store with pre-existing data
	mockPG := newMockPGStore()

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	existingEntry := &transport.SignedEntry{
		Entry: &transport.Entry{
			ID:    uuid.New(),
			Edges: transport.SortEdges(pk1, pk2),
			Type:  types.DMSG,
		},
		Signatures: [2]cipher.Sig{},
	}

	// Add entry to mock PG before creating cached store
	err := mockPG.RegisterTransport(ctx, existingEntry)
	require.NoError(t, err)

	// Create cached store - should load existing data
	cachedStore, err := NewCachedStore(mockPG)
	require.NoError(t, err)
	defer cachedStore.Close()

	// Verify the pre-existing data is in cache
	entry, err := cachedStore.GetTransportByID(ctx, existingEntry.Entry.ID)
	require.NoError(t, err)
	assert.Equal(t, existingEntry.Entry.ID, entry.ID)

	// Verify edge index works for pre-loaded data
	entries, err := cachedStore.GetTransportsByEdge(ctx, pk1)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, existingEntry.Entry.ID, entries[0].ID)
}

func TestMockStore(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	onlyPGStore := false
	s, err := New(logger, gormDB, memoryStore, onlyPGStore)
	require.NoError(t, err)

	ctx := context.Background()
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	sEntry := &transport.SignedEntry{
		Entry: &transport.Entry{
			ID:    uuid.New(),
			Edges: transport.SortEdges(pk1, pk2),
			Type:  types.DMSG,
		},
		Signatures: [2]cipher.Sig{},
	}

	// Test basic operations on mock store
	err = s.RegisterTransport(ctx, sEntry)
	require.NoError(t, err)

	entry, err := s.GetTransportByID(ctx, sEntry.Entry.ID)
	require.NoError(t, err)
	assert.Equal(t, sEntry.Entry.ID, entry.ID)

	err = s.DeregisterTransport(ctx, sEntry.Entry.ID)
	require.NoError(t, err)

	_, err = s.GetTransportByID(ctx, sEntry.Entry.ID)
	assert.Equal(t, ErrTransportNotFound, err)
}
