package memory

import (
	"fmt"
	"sync"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
)

//ClientRepo Implements the Repository Interface to provide an in-memory storage provider
type ClientRepo struct {
	client client.Client
	cliMu  sync.Mutex
}

//NewRepo Constructor
func NewClientRepo() *ClientRepo {
	cR := ClientRepo{}

	cR.client, _ = cR.New() //nolint

	return &cR
}

//New fills repo with a new client, if none has been set
//also returns a client when a client has been set already
func (r *ClientRepo) New() (client.Client, error) {
	if !r.client.IsEmtpy() {
		return r.client, fmt.Errorf("client already defined")
	} else {
		r.SetClient(*client.NewClient())
		return r.client, nil
	}
}

//Get Returns the client
func (r *ClientRepo) GetClient() (*client.Client, error) {
	r.cliMu.Lock()
	defer r.cliMu.Unlock()

	if r.client.IsEmtpy() {
		return nil, fmt.Errorf("client not found")
	} else {
		return &r.client, nil
	}
}

//Update the provided client
func (r *ClientRepo) SetClient(client client.Client) error {
	r.cliMu.Lock()
	defer r.cliMu.Unlock()

	r.client = client
	return nil
}
