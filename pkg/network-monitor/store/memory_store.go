// Package store pkg/network-monitor/store/memory_store.go
package store

import (
	"context"
	"errors"
	"sync"

	"github.com/skycoin/skywire/internal/nm"
	"github.com/skycoin/skywire/pkg/cipher"
)

type memStore struct {
	visorSummaries map[string]*nm.VisorSummary
	mu             sync.RWMutex
}

// newMemoryStore creates new uptimes memory store.
func newMemoryStore() Store {
	return &memStore{
		visorSummaries: make(map[string]*nm.VisorSummary),
	}
}

func (s *memStore) AddVisorSummary(_ context.Context, key cipher.PubKey, visorSum *nm.VisorSummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.visorSummaries[key.String()] = visorSum

	return nil
}

func (s *memStore) GetVisorByPk(pk string) (entry *nm.VisorSummary, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sum, ok := s.visorSummaries[pk]
	if !ok {
		return &nm.VisorSummary{}, errors.New("No visor entry found")
	}
	return sum, nil
}

func (s *memStore) GetAllSummaries() (map[string]nm.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	response := make(map[string]nm.Summary)

	for key, visorSum := range s.visorSummaries {
		response[key] = nm.Summary{
			Visor: visorSum,
		}
	}
	return response, nil
}

func (s *memStore) Close() {

}
