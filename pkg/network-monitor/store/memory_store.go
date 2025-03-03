// Package store pkg/network-monitor/store/memory_store.go
package store

import (
	"sync"

	"github.com/skycoin/skywire/internal/nm"
)

type memStore struct {
	networkStatus nm.Status
	mu            sync.RWMutex
}

// newMemoryStore creates new uptimes memory store.
func newMemoryStore() Store {
	return &memStore{
		networkStatus: nm.Status{},
	}
}

func (s *memStore) GetNetworkStatus() (nm.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.networkStatus, nil
}

func (s *memStore) SetNetworkStatus(status nm.Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networkStatus = status
	return nil
}

func (s *memStore) Close() {

}
