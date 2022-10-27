// Package memory contains code of the client repo of interfaceadapters
package memory

import (
	"fmt"
	"sync"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
)

// ClientRepo Implements the Repository Interface to provide an in-memory storage provider
type ClientRepo struct {
	client client.Client
	cliMu  sync.Mutex
}

// NewClientRepo Constructor
func NewClientRepo() *ClientRepo {
	cR := ClientRepo{}

	cR.client, _ = cR.New() //nolint

	return &cR
}

// New fills repo with a new client, if none has been set
// also returns a client when a client has been set already
func (r *ClientRepo) New() (client.Client, error) {
	if !r.client.IsEmpty() {
		return r.client, fmt.Errorf("client already defined")
	}
	err := r.SetClient(*client.NewClient())
	if err != nil {
		return client.Client{}, err
	}
	return r.client, nil

}

// GetClient Returns the client
func (r *ClientRepo) GetClient() (*client.Client, error) {
	r.cliMu.Lock()
	defer r.cliMu.Unlock()

	if r.client.IsEmpty() {
		return nil, fmt.Errorf("client not found")
	}
	return &r.client, nil

}

// SetClient updates the provided client
func (r *ClientRepo) SetClient(client client.Client) error {
	r.cliMu.Lock()
	defer r.cliMu.Unlock()

	r.client = client
	return nil
}
