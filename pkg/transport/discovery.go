// Package transport pkg/transport/discovery.go
package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// DiscoveryClient performs Transport discovery operations.
type DiscoveryClient interface {
	RegisterTransports(ctx context.Context, entries ...*SignedEntry) error
	GetTransportByID(ctx context.Context, id uuid.UUID) (*Entry, error)
	GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*Entry, error)
	GetAllTransports(ctx context.Context) ([]*Entry, error)
	GetTransportStats(ctx context.Context, pk cipher.PubKey) (*TransportStats, error)
	GetAllTransportsStats(ctx context.Context) (*NetworkTransportStats, error)
	DeleteTransport(ctx context.Context, id uuid.UUID) error
}

// TransportStats contains statistics about transports for a given public key.
type TransportStats struct {
	Total  int            `json:"total"`
	ByType map[string]int `json:"by_type"`
}

// NetworkTransportStats contains network-wide transport statistics.
type NetworkTransportStats struct {
	TotalTransports int            `json:"total_transports"`
	ByType          map[string]int `json:"by_type"`
	UniqueVisors    int            `json:"unique_visors"`
}

type mockDiscoveryClient struct {
	sync.Mutex
	entries map[uuid.UUID]Entry
}

// NewDiscoveryMock construct a new mock transport discovery client.
func NewDiscoveryMock() DiscoveryClient {
	return &mockDiscoveryClient{entries: map[uuid.UUID]Entry{}}
}

func (td *mockDiscoveryClient) RegisterTransports(_ context.Context, entries ...*SignedEntry) error {
	td.Lock()
	for _, entry := range entries {
		td.entries[entry.Entry.ID] = *entry.Entry
	}
	td.Unlock()

	return nil
}

func (td *mockDiscoveryClient) GetTransportByID(_ context.Context, id uuid.UUID) (*Entry, error) {
	td.Lock()
	entry, ok := td.entries[id]
	td.Unlock()

	if !ok {
		return nil, errors.New("transport not found")
	}

	return &Entry{
		ID:    entry.ID,
		Edges: entry.Edges,
		Label: entry.Label,
		Type:  entry.Type,
	}, nil
}

func (td *mockDiscoveryClient) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*Entry, error) {
	td.Lock()
	res := make([]*Entry, 0)
	for _, entry := range td.entries {
		if entry.HasEdge(pk) {
			e := &Entry{}
			*e = entry
			res = append(res, e)
		}
	}
	td.Unlock()

	if len(res) == 0 {
		return nil, nil
	}

	return res, nil
}

func (td *mockDiscoveryClient) GetAllTransports(_ context.Context) ([]*Entry, error) {
	td.Lock()
	res := make([]*Entry, 0, len(td.entries))
	for _, entry := range td.entries {
		e := &Entry{}
		*e = entry
		res = append(res, e)
	}
	td.Unlock()

	return res, nil
}

func (td *mockDiscoveryClient) GetTransportStats(_ context.Context, pk cipher.PubKey) (*TransportStats, error) {
	td.Lock()
	byType := make(map[string]int)
	count := 0
	for _, entry := range td.entries {
		if entry.HasEdge(pk) {
			count++
			byType[string(entry.Type)]++
		}
	}
	td.Unlock()

	return &TransportStats{
		Total:  count,
		ByType: byType,
	}, nil
}

func (td *mockDiscoveryClient) GetAllTransportsStats(_ context.Context) (*NetworkTransportStats, error) {
	td.Lock()
	byType := make(map[string]int)
	uniqueVisors := make(map[cipher.PubKey]struct{})

	for _, entry := range td.entries {
		byType[string(entry.Type)]++
		for _, edge := range entry.Edges {
			uniqueVisors[edge] = struct{}{}
		}
	}
	td.Unlock()

	return &NetworkTransportStats{
		TotalTransports: len(td.entries),
		ByType:          byType,
		UniqueVisors:    len(uniqueVisors),
	}, nil
}

// NOTE that mock implementation doesn't checks whether the transport to be deleted is valid or not, this is, that
// it can be deleted by the visor who called DeleteTransport
func (td *mockDiscoveryClient) DeleteTransport(ctx context.Context, id uuid.UUID) error { //nolint:revive
	td.Lock()
	defer td.Unlock()

	_, ok := td.entries[id]
	if !ok {
		return fmt.Errorf("transport with id: %s not found in transport discovery", id)
	}

	delete(td.entries, id)
	return nil
}

// noopDiscoveryClient is a no-op transport discovery client that doesn't register transports.
// Used for self-transports which cannot be used for routing and shouldn't clutter TPD.
type noopDiscoveryClient struct{}

// NewNoopDiscoveryClient constructs a new no-op transport discovery client.
func NewNoopDiscoveryClient() DiscoveryClient {
	return &noopDiscoveryClient{}
}

func (nd *noopDiscoveryClient) RegisterTransports(_ context.Context, _ ...*SignedEntry) error {
	return nil
}

func (nd *noopDiscoveryClient) GetTransportByID(_ context.Context, _ uuid.UUID) (*Entry, error) {
	return nil, errors.New("transport not found")
}

func (nd *noopDiscoveryClient) GetTransportsByEdge(_ context.Context, _ cipher.PubKey) ([]*Entry, error) {
	return nil, nil
}

func (nd *noopDiscoveryClient) GetAllTransports(_ context.Context) ([]*Entry, error) {
	return nil, nil
}

func (nd *noopDiscoveryClient) GetTransportStats(_ context.Context, _ cipher.PubKey) (*TransportStats, error) {
	return &TransportStats{Total: 0, ByType: make(map[string]int)}, nil
}

func (nd *noopDiscoveryClient) GetAllTransportsStats(_ context.Context) (*NetworkTransportStats, error) {
	return &NetworkTransportStats{TotalTransports: 0, ByType: make(map[string]int), UniqueVisors: 0}, nil
}

func (nd *noopDiscoveryClient) DeleteTransport(_ context.Context, _ uuid.UUID) error {
	return nil
}
