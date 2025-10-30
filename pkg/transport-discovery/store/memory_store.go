// Package store pkg/transport-discovery/store/memory_store.go
package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// MemoryStore implements TransportStore with full in-memory caching
type MemoryStore struct {
	log *logging.Logger

	transports map[uuid.UUID]*transport.Entry
	edgeIndex  map[string][]uuid.UUID
	typeCount  map[types.Type]int

	mu sync.RWMutex

	pgStore *postgresStore

	stats struct {
		totalReads     int64
		totalWrites    int64
		cacheHits      int64
		lastReload     time.Time
		transportCount int64
	}

	closeC chan struct{}
}

// NewMemoryStore creates a new in-memory store backed by PostgreSQL
func NewMemoryStore(logger *logging.Logger, cl *gorm.DB) (TransportStore, error) {
	pgStore := &postgresStore{
		log:    logger,
		client: cl,
		cache:  make(map[string]int64),
		closeC: make(chan struct{}),
	}

	if err := cl.AutoMigrate(Transport{}); err != nil {
		logger.Warn("failed to complete automigrate process")
	}

	ms := &MemoryStore{
		log:        logger,
		transports: make(map[uuid.UUID]*transport.Entry),
		edgeIndex:  make(map[string][]uuid.UUID),
		typeCount:  make(map[types.Type]int),
		pgStore:    pgStore,
		closeC:     make(chan struct{}),
	}

	if err := ms.reloadFromDB(); err != nil {
		return nil, fmt.Errorf("failed to load data from database: %v", err)
	}

	go ms.backgroundReloader()

	logger.Infof("MemoryStore initialized with %d transports", len(ms.transports))
	return ms, nil
}

// reloadFromDB loads all data from PostgreSQL into memory
func (ms *MemoryStore) reloadFromDB() error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	var tpRecords []Transport
	if err := ms.pgStore.client.Find(&tpRecords).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			return fmt.Errorf("failed to load transports: %v", err)
		}
	}

	ms.transports = make(map[uuid.UUID]*transport.Entry)
	ms.edgeIndex = make(map[string][]uuid.UUID)
	ms.typeCount = make(map[types.Type]int)

	ms.typeCount[types.STCP] = 0
	ms.typeCount[types.STCPR] = 0
	ms.typeCount[types.SUDPH] = 0
	ms.typeCount[types.DMSG] = 0

	for _, tpRecord := range tpRecords {
		entry, err := makeEntry(tpRecord)
		if err != nil {
			ms.log.Warnf("Failed to parse transport %s: %v", tpRecord.TransportID, err)
			continue
		}

		ms.transports[entry.ID] = &entry

		ms.addToEdgeIndexLocked(entry.Edges[0].Hex(), entry.ID)
		ms.addToEdgeIndexLocked(entry.Edges[1].Hex(), entry.ID)

		ms.typeCount[entry.Type]++
	}

	ms.stats.lastReload = time.Now()
	atomic.StoreInt64(&ms.stats.transportCount, int64(len(ms.transports)))

	return nil
}

// addToEdgeIndexLocked adds a transport ID to edge index (must hold write lock)
func (ms *MemoryStore) addToEdgeIndexLocked(edgeHex string, id uuid.UUID) {
	ms.edgeIndex[edgeHex] = append(ms.edgeIndex[edgeHex], id)
}

// removeFromEdgeIndexLocked removes a transport ID from edge index (must hold write lock)
func (ms *MemoryStore) removeFromEdgeIndexLocked(edgeHex string, id uuid.UUID) {
	ids := ms.edgeIndex[edgeHex]
	for i, tid := range ids {
		if tid == id {
			ids[i] = ids[len(ids)-1]
			ms.edgeIndex[edgeHex] = ids[:len(ids)-1]
			break
		}
	}

	if len(ms.edgeIndex[edgeHex]) == 0 {
		delete(ms.edgeIndex, edgeHex)
	}
}

// RegisterTransport registers a new transport (write-through)
func (ms *MemoryStore) RegisterTransport(ctx context.Context, sEntry *transport.SignedEntry) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if err := ms.pgStore.RegisterTransport(ctx, sEntry); err != nil {
		return err
	}

	entry := sEntry.Entry

	oldEntry, exists := ms.transports[entry.ID]
	if exists {
		if oldEntry.Edges[0] != entry.Edges[0] || oldEntry.Edges[1] != entry.Edges[1] {
			ms.removeFromEdgeIndexLocked(oldEntry.Edges[0].Hex(), entry.ID)
			ms.removeFromEdgeIndexLocked(oldEntry.Edges[1].Hex(), entry.ID)
			ms.typeCount[oldEntry.Type]--
		} else {
			return nil
		}
	}

	ms.transports[entry.ID] = entry

	ms.addToEdgeIndexLocked(entry.Edges[0].Hex(), entry.ID)
	if entry.Edges[1] != entry.Edges[0] {
		ms.addToEdgeIndexLocked(entry.Edges[1].Hex(), entry.ID)
	}

	ms.typeCount[entry.Type]++

	atomic.AddInt64(&ms.stats.totalWrites, 1)
	if !exists {
		atomic.AddInt64(&ms.stats.transportCount, 1)
	}

	return nil
}

// DeregisterTransport removes a transport (write-through)
func (ms *MemoryStore) DeregisterTransport(ctx context.Context, id uuid.UUID) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if err := ms.pgStore.DeregisterTransport(ctx, id); err != nil {
		return err
	}

	entry, exists := ms.transports[id]
	if !exists {
		return nil
	}

	ms.removeFromEdgeIndexLocked(entry.Edges[0].Hex(), id)
	if entry.Edges[1] != entry.Edges[0] {
		ms.removeFromEdgeIndexLocked(entry.Edges[1].Hex(), id)
	}

	ms.typeCount[entry.Type]--

	delete(ms.transports, id)

	atomic.AddInt64(&ms.stats.totalWrites, 1)
	atomic.AddInt64(&ms.stats.transportCount, -1)

	return nil
}

// GetTransportByID retrieves a transport by ID (memory-only read)
func (ms *MemoryStore) GetTransportByID(_ context.Context, id uuid.UUID) (*transport.Entry, error) {
	atomic.AddInt64(&ms.stats.totalReads, 1)

	entry, exists := ms.transports[id]
	if !exists {
		return nil, ErrTransportNotFound
	}

	atomic.AddInt64(&ms.stats.cacheHits, 1)

	entryCopy := *entry
	return &entryCopy, nil
}

// GetTransportsByEdge retrieves all transports for a given edge (memory-only read)
func (ms *MemoryStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	atomic.AddInt64(&ms.stats.totalReads, 1)

	edgeHex := pk.Hex()
	transportIDs, exists := ms.edgeIndex[edgeHex]

	if !exists || len(transportIDs) == 0 {
		return []*transport.Entry{}, nil
	}

	atomic.AddInt64(&ms.stats.cacheHits, 1)

	entries := make([]*transport.Entry, 0, len(transportIDs))
	for _, id := range transportIDs {
		if entry, exists := ms.transports[id]; exists {
			entryCopy := *entry
			entries = append(entries, &entryCopy)
		}
	}

	return entries, nil
}

// GetAllTransports retrieves all transports (memory-only read)
func (ms *MemoryStore) GetAllTransports(_ context.Context, selfTransports bool) ([]*transport.Entry, error) {
	atomic.AddInt64(&ms.stats.totalReads, 1)
	atomic.AddInt64(&ms.stats.cacheHits, 1)

	entries := make([]*transport.Entry, 0, len(ms.transports))

	for _, entry := range ms.transports {
		if !selfTransports && entry.Edges[0] == entry.Edges[1] {
			continue
		}

		entryCopy := *entry
		entries = append(entries, &entryCopy)
	}

	if len(entries) == 0 {
		return []*transport.Entry{}, nil
	}

	return entries, nil
}

// GetNumberOfTransports returns count by type (memory-only read)
func (ms *MemoryStore) GetNumberOfTransports(_ context.Context) (map[types.Type]int, error) {
	atomic.AddInt64(&ms.stats.totalReads, 1)
	atomic.AddInt64(&ms.stats.cacheHits, 1)

	result := make(map[types.Type]int)
	for k, v := range ms.typeCount {
		result[k] = v
	}

	return result, nil
}

// GetStats returns performance statistics
func (ms *MemoryStore) GetStats() map[string]interface{} {
	totalReads := atomic.LoadInt64(&ms.stats.totalReads)
	cacheHits := atomic.LoadInt64(&ms.stats.cacheHits)

	hitRate := float64(0)
	if totalReads > 0 {
		hitRate = float64(cacheHits) / float64(totalReads) * 100
	}

	memoryUsageBytes := len(ms.transports) * 500
	memoryUsageMB := float64(memoryUsageBytes) / 1024 / 1024

	return map[string]interface{}{
		"total_transports": atomic.LoadInt64(&ms.stats.transportCount),
		"total_reads":      totalReads,
		"total_writes":     atomic.LoadInt64(&ms.stats.totalWrites),
		"cache_hits":       cacheHits,
		"cache_hit_rate":   fmt.Sprintf("%.2f%%", hitRate),
		"memory_usage_mb":  fmt.Sprintf("%.2f", memoryUsageMB),
		"last_reload":      ms.stats.lastReload.Format(time.RFC3339),
		"edge_index_size":  len(ms.edgeIndex),
		"uptime":           time.Since(ms.stats.lastReload).String(),
	}
}

// backgroundReloader periodically reloads data from PostgreSQL (safety)
func (ms *MemoryStore) backgroundReloader() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ms.log.Debug("Starting periodic reload from database")
			if err := ms.reloadFromDB(); err != nil {
				ms.log.Errorf("Failed to reload from database: %v", err)
			} else {
				ms.log.Debugf("Successfully reloaded %d transports from database",
					atomic.LoadInt64(&ms.stats.transportCount))
			}
		case <-ms.closeC:
			return
		}
	}
}

// Close closes the store
func (ms *MemoryStore) Close() {
	close(ms.closeC)
	ms.pgStore.Close()
}

func makeEntry(record Transport) (transport.Entry, error) {
	cipher1 := cipher.PubKey{}
	if err := cipher1.UnmarshalText([]byte(record.EdgeA)); err != nil {
		return transport.Entry{}, err
	}

	cipher2 := cipher.PubKey{}
	if err := cipher2.UnmarshalText([]byte(record.EdgeB)); err != nil {
		return transport.Entry{}, err
	}

	entry := transport.Entry{}
	entry.Label = transport.Label(record.Label)
	entry.Type = types.Type(record.Type)
	entry.ID = uuid.MustParse(record.TransportID)
	entry.Edges = [2]cipher.PubKey{cipher1, cipher2}

	return entry, nil
}

// Transport is model (structure) for transports table
type Transport struct { //TODO (mohammed): good to use transport.Entry model here
	ID          uint `gorm:"primarykey"`
	CreatedAt   time.Time
	TransportID string `gorm:"unique"`
	EdgeA       string
	EdgeB       string
	Type        string
	Label       string
}
