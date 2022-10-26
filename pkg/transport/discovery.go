// Package transport pkg/transport/discovery.go
package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// DiscoveryClient performs Transport discovery operations.
type DiscoveryClient interface {
	RegisterTransports(ctx context.Context, entries ...*SignedEntry) error
	GetTransportByID(ctx context.Context, id uuid.UUID) (*Entry, error)
	GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*Entry, error)
	DeleteTransport(ctx context.Context, id uuid.UUID) error
}

type mockDiscoveryClient struct {
	sync.Mutex
	entries map[uuid.UUID]Entry
}

// NewDiscoveryMock construct a new mock transport discovery client.
func NewDiscoveryMock() DiscoveryClient {
	return &mockDiscoveryClient{entries: map[uuid.UUID]Entry{}}
}

func (td *mockDiscoveryClient) RegisterTransports(ctx context.Context, entries ...*SignedEntry) error {
	td.Lock()
	for _, entry := range entries {
		td.entries[entry.Entry.ID] = *entry.Entry
	}
	td.Unlock()

	return nil
}

func (td *mockDiscoveryClient) GetTransportByID(ctx context.Context, id uuid.UUID) (*Entry, error) {
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

func (td *mockDiscoveryClient) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*Entry, error) {
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

// NOTE that mock implementation doesn't checks whether the transport to be deleted is valid or not, this is, that
// it can be deleted by the visor who called DeleteTransport
func (td *mockDiscoveryClient) DeleteTransport(ctx context.Context, id uuid.UUID) error {
	td.Lock()
	defer td.Unlock()

	_, ok := td.entries[id]
	if !ok {
		return fmt.Errorf("transport with id: %s not found in transport discovery", id)
	}

	delete(td.entries, id)
	return nil
}
