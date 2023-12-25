// Package store pkg/transport-discovery/store/memory_store.go
package store

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// ErrBadEntry is returned is entry is malformed.
var ErrBadEntry = errors.New("bad entry format")

type memStore struct {
	transports map[uuid.UUID]*transport.Entry

	err error
	mu  sync.Mutex
}

func newMemoryStore() *memStore {
	return &memStore{
		transports: map[uuid.UUID]*transport.Entry{},
	}
}

func (s *memStore) SetError(err error) {
	s.err = err
}

func (s *memStore) RegisterTransport(_ context.Context, entry *transport.SignedEntry) error {
	if s.err != nil {
		return s.err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.Entry == nil {
		return ErrBadEntry
	}

	s.transports[entry.Entry.ID] = entry.Entry

	return nil
}

func (s *memStore) DeregisterTransport(_ context.Context, id uuid.UUID) error {
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

func (s *memStore) GetTransportByID(_ context.Context, id uuid.UUID) (*transport.Entry, error) {
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

func (s *memStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
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

func (s *memStore) GetNumberOfTransports(context.Context) (map[network.Type]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	response := make(map[network.Type]int)
	for _, entry := range s.transports {
		response[entry.Type]++
	}
	return response, nil
}

func (s *memStore) GetAllTransports(context.Context) ([]*transport.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var response []*transport.Entry
	for _, entry := range s.transports {
		response = append(response, entry)
	}
	return response, nil
}

func (s *memStore) Close() {

}
