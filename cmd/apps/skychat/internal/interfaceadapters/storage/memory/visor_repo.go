// Package memory contains code of the chat repo of interfaceadapters
package memory

import (
	"fmt"
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
)

// VisorRepo Implements the Repository Interface to provide an in-memory storage provider
type VisorRepo struct {
	visors   map[cipher.PubKey]chat.Visor
	visorsMu sync.Mutex
}

// NewVisorRepo Constructor
func NewVisorRepo() *VisorRepo {
	r := VisorRepo{}
	r.visors = make(map[cipher.PubKey]chat.Visor)
	return &r
}

// GetByPK Returns the visor with the provided pk
func (r *VisorRepo) GetByPK(pk cipher.PubKey) (*chat.Visor, error) {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	visor, ok := r.visors[pk]
	if !ok {
		return nil, fmt.Errorf("visor not found")
	}
	return &visor, nil
}

// GetAll Returns all stored visors
func (r *VisorRepo) GetAll() ([]chat.Visor, error) {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	var values []chat.Visor
	for _, value := range r.visors {
		values = append(values, value)
	}

	return values, nil
}

// Add adds the provided visor to the repository
func (r *VisorRepo) Add(visor chat.Visor) error {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	r.visors[visor.GetPK()] = visor
	return nil
}

// Set sets the provided visor
func (r *VisorRepo) Set(visor chat.Visor) error {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	r.visors[visor.GetPK()] = visor
	return nil
}

// Delete deletes the chat with the provided pk
func (r *VisorRepo) Delete(pk cipher.PubKey) error {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	_, exists := r.visors[pk]
	if !exists {
		return fmt.Errorf("id %v not found", pk.String())
	}
	delete(r.visors, pk)
	return nil
}
