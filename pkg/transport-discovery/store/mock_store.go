// Package store pkg/transport-discovery/store/mock_store.go
package store

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// ErrBadEntry is returned is entry is malformed.
var ErrBadEntry = errors.New("bad entry format")

// mockStore is an in-memory store used for testing purposes only.
// It allows setting errors for testing error conditions.
type mockStore struct {
	transports map[uuid.UUID]*transport.Entry

	err error
	mu  sync.Mutex
}

func newMockStore() *mockStore {
	return &mockStore{
		transports: map[uuid.UUID]*transport.Entry{},
	}
}

func (s *mockStore) SetError(err error) {
	s.err = err
}

func (s *mockStore) RegisterTransport(_ context.Context, entry *transport.SignedEntry) error {
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

func (s *mockStore) DeregisterTransport(_ context.Context, id uuid.UUID) error {
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

func (s *mockStore) GetTransportByID(_ context.Context, id uuid.UUID) (*transport.Entry, error) {
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

func (s *mockStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
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

func (s *mockStore) GetTransportsByEdgeNoCache(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
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

func (s *mockStore) GetNumberOfTransports(context.Context) (map[types.Type]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	response := make(map[types.Type]int)
	for _, entry := range s.transports {
		response[entry.Type]++
	}
	return response, nil
}

func (s *mockStore) GetAllTransports(_ context.Context, selfTransports bool) ([]*transport.Entry, error) {
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

func (s *mockStore) Close() {

}
