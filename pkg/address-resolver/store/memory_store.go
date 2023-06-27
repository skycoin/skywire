package store

import (
	"context"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

type memStore struct {
	mu        sync.Mutex
	visorData map[network.Type]map[string]addrresolver.VisorData
}

func newMemoryStore() *memStore {
	return &memStore{
		visorData: make(map[network.Type]map[string]addrresolver.VisorData),
	}
}

func (s *memStore) Bind(_ context.Context, netType network.Type, pk cipher.PubKey, visorData addrresolver.VisorData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.visorData[netType]; !ok {
		s.visorData[netType] = make(map[string]addrresolver.VisorData)
	}

	s.visorData[netType][pk.String()] = visorData

	return nil
}

func (s *memStore) DelBind(_ context.Context, netType network.Type, pk cipher.PubKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.visorData[netType], pk.String())
	return nil
}

func (s *memStore) Resolve(_ context.Context, netType network.Type, pk cipher.PubKey) (addrresolver.VisorData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tpTypeData, ok := s.visorData[netType]
	if !ok {
		return addrresolver.VisorData{}, ErrNoEntry
	}

	data, ok := tpTypeData[pk.String()]
	if !ok {
		return addrresolver.VisorData{}, ErrNoEntry
	}

	return data, nil
}

func (s *memStore) GetAll(_ context.Context, netType network.Type) (pks []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for pk := range s.visorData[netType] {
		pks = append(pks, pk)
	}

	return pks, nil
}
