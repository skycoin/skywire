package managedisc

import (
	"context"
	"strconv"
	"sync"

	"github.com/skycoin/skywire/pkg/servicedisc"
)

// Manager manages the associated app discovery
type Manager interface {

	// Start starts the manager.
	Start()

	// Stop stops the manager.
	Stop()

	// ChangeValue changes the associated value of the discovery entry.
	ChangeValue(name string, v []byte) error
}

// emptyManager is for apps that do not require discovery manages.
type emptyManager struct{}

func (emptyManager) Start()                                  {}
func (emptyManager) Stop()                                   {}
func (emptyManager) ChangeValue(name string, v []byte) error { return nil }

// serviceManager manages service-discovery entry of locally running App.
type serviceManager struct {
	client *servicedisc.HTTPClient
	mu     sync.Mutex
}

func (u *serviceManager) Start() {
	u.mu.Lock()
	defer u.mu.Unlock()

	ctx := context.Background()
	if err := u.client.RegisterEntry(ctx); err != nil {
		return
	}
}

func (u *serviceManager) Stop() {
	u.mu.Lock()
	defer u.mu.Unlock()

	ctx := context.Background()
	if err := u.client.DeregisterEntry(ctx); err != nil {
		return
	}
}

func (u *serviceManager) ChangeValue(name string, v []byte) error {
	switch name {
	case ConnCountValue:
		n, err := strconv.Atoi(string(v))
		if err != nil {
			return err
		}
		go u.client.UpdateStats(servicedisc.Stats{ConnectedClients: n})
	}
	return nil
}
