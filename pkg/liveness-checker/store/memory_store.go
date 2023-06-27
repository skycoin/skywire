// Package store pkg/liveness-checker/store/memory_store.go
package store

import (
	"context"
	"sync"

	"github.com/skycoin/skywire/internal/lc"
)

type memStore struct {
	serviceSummaries map[string]*lc.ServiceSummary
	mu               sync.RWMutex
}

// newMemoryStore creates new uptimes memory store.
func newMemoryStore() Store {
	return &memStore{
		serviceSummaries: make(map[string]*lc.ServiceSummary),
	}
}

func (s *memStore) AddServiceSummary(_ context.Context, key string, visorSum *lc.ServiceSummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serviceSummaries[key] = visorSum
	return nil
}

func (s *memStore) GetServiceByName(_ context.Context, name string) (*lc.ServiceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sSum := s.serviceSummaries[name]
	return sSum, nil
}

func (s *memStore) GetServiceSummaries(_ context.Context) (map[string]*lc.ServiceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sSums := s.serviceSummaries
	return sSums, nil
}

func (s *memStore) Close() {

}
