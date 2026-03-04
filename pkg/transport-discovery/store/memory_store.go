// Package store pkg/transport-discovery/store/memory_store.go
package store

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// memoryStore is an in-memory store used for testing purposes only.
type memoryStore struct {
	transports map[uuid.UUID]*transport.Entry

	err error
	mu  sync.Mutex
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		transports: map[uuid.UUID]*transport.Entry{},
	}
}

func (s *memoryStore) SetError(err error) {
	s.err = err
}

func (s *memoryStore) RegisterTransport(_ context.Context, entry *transport.SignedEntry) error {
	if s.err != nil {
		return s.err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.Entry == nil {
		return ErrBadEntry
	}

	entry.Registered = time.Now().UnixNano()
	s.transports[entry.Entry.ID] = entry.Entry

	return nil
}

func (s *memoryStore) DeregisterTransport(_ context.Context, id uuid.UUID) error {
	if s.err != nil {
		return s.err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.transports[id]
	if !ok {
		return ErrTransportNotFound
	}

	delete(s.transports, id)

	return nil
}

func (s *memoryStore) GetTransportByID(_ context.Context, id uuid.UUID) (*transport.Entry, error) {
	if s.err != nil {
		return nil, s.err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.transports[id]
	if !ok {
		return nil, ErrTransportNotFound
	}

	return v, nil
}

func (s *memoryStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	if s.err != nil {
		return nil, s.err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res := make([]*transport.Entry, 0)

	for _, entry := range s.transports {
		if entry != nil && entry.HasEdge(pk) {
			res = append(res, entry)
		}
	}

	if len(res) == 0 {
		return nil, ErrTransportNotFound
	}

	return res, nil
}

func (s *memoryStore) GetNumberOfTransports(context.Context) (map[types.Type]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	response := make(map[types.Type]int)
	for _, entry := range s.transports {
		response[entry.Type]++
	}
	return response, nil
}

func (s *memoryStore) GetAllTransports(_ context.Context, selfTransports bool) ([]*transport.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var response []*transport.Entry
	for _, entry := range s.transports {
		if !selfTransports {
			if entry.Edges[0] == entry.Edges[1] {
				continue
			}
		}
		response = append(response, entry)
	}
	return response, nil
}

func (s *memoryStore) Close() {

}

// GetTransportBandwidth returns empty slice for memory store (no bandwidth tracking).
func (s *memoryStore) GetTransportBandwidth(_ context.Context, _ uuid.UUID, _ string, _ int) ([]BandwidthAggregation, error) {
	return []BandwidthAggregation{}, nil
}

// GetVisorBandwidth returns empty slice for memory store (no bandwidth tracking).
func (s *memoryStore) GetVisorBandwidth(_ context.Context, _ cipher.PubKey, _ string, _ int) ([]BandwidthAggregation, error) {
	return []BandwidthAggregation{}, nil
}

// GetAllVisorSummaries groups in-memory transports by visor PK, returns uptime summaries.
// Online status is determined by having active transports.
// Version and Daily fields require uptime tracker integration.
func (s *memoryStore) GetAllVisorSummaries(_ context.Context) ([]VisorSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	visorOnline := make(map[cipher.PubKey]bool)

	for _, entry := range s.transports {
		for _, edge := range entry.Edges {
			visorOnline[edge] = true
		}
	}

	var result []VisorSummary
	for pk := range visorOnline {
		result = append(result, VisorSummary{
			PK:     pk,
			Online: true,
		})
	}

	return result, nil
}

// BackupAndCleanOldBandwidth is a no-op for memory store.
func (s *memoryStore) BackupAndCleanOldBandwidth(_ context.Context, _ string) error {
	return nil
}

// GetNetworkMetrics returns empty network metrics for memory store.
func (s *memoryStore) GetNetworkMetrics(_ context.Context, _ MetricsQuery) (*NetworkMetricResponse, error) {
	return &NetworkMetricResponse{
		Daily:      []DailyAggregate{},
		Cumulative: &CumulativeAggregate{ByType: make(map[string]*TypeMetricAggregate)},
	}, nil
}

// GetVisorAggregateMetrics returns empty visor metrics for memory store.
func (s *memoryStore) GetVisorAggregateMetrics(_ context.Context, pks []cipher.PubKey, _ MetricsQuery) (map[string]*VisorMetricResponse, error) {
	result := make(map[string]*VisorMetricResponse)
	for _, pk := range pks {
		result[pk.Hex()] = &VisorMetricResponse{
			Daily:      []DailyVisorAggregate{},
			Cumulative: &VisorCumulativeAggregate{},
		}
	}
	return result, nil
}

// GetAllTransportMetrics returns metrics for all transports in memory store.
func (s *memoryStore) GetAllTransportMetrics(ctx context.Context, query MetricsQuery) ([]TransportMetric, error) {
	entries, err := s.GetAllTransports(ctx, true)
	if err != nil {
		return nil, err
	}
	return s.buildTransportMetrics(entries, query), nil
}

// GetTransportMetricsByIDs returns metrics for specific transports.
func (s *memoryStore) GetTransportMetricsByIDs(ctx context.Context, ids []uuid.UUID, query MetricsQuery) ([]TransportMetric, error) {
	var entries []*transport.Entry
	for _, id := range ids {
		entry, err := s.GetTransportByID(ctx, id)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return s.buildTransportMetrics(entries, query), nil
}

// GetTransportMetricsByVisors returns metrics for transports of specified visors.
func (s *memoryStore) GetTransportMetricsByVisors(ctx context.Context, pks []cipher.PubKey, query MetricsQuery) ([]TransportMetric, error) {
	seen := make(map[uuid.UUID]bool)
	var entries []*transport.Entry

	for _, pk := range pks {
		visorEntries, err := s.GetTransportsByEdge(ctx, pk)
		if err != nil {
			continue
		}
		for _, entry := range visorEntries {
			if !seen[entry.ID] {
				seen[entry.ID] = true
				entries = append(entries, entry)
			}
		}
	}
	return s.buildTransportMetrics(entries, query), nil
}

// buildTransportMetrics builds TransportMetric slice from entries (memory store version).
func (s *memoryStore) buildTransportMetrics(entries []*transport.Entry, query MetricsQuery) []TransportMetric {
	var results []TransportMetric

	for _, entry := range entries {
		// Apply type filter
		if query.Type != "" && string(entry.Type) != query.Type {
			continue
		}

		metric := TransportMetric{
			ID:    entry.ID.String(),
			Type:  string(entry.Type),
			Live:  true, // All in-memory transports are considered live
			Daily: []DailyEdgeBandwidth{},
		}

		if query.Edges {
			metric.Edges = []string{entry.Edges[0].Hex(), entry.Edges[1].Hex()}
		}

		results = append(results, metric)
	}

	return results
}
