package appdisc

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/servicedisc"
)

// Updater updates the associated app discovery
type Updater interface {

	// Start starts the updater.
	Start()

	// Stop stops the updater.
	Stop()

	// ChangeValue changes the associated value of the discovery entry.
	ChangeValue(name string, v []byte) error
}

// emptyUpdater is for apps that do not require discovery updates.
type emptyUpdater struct{}

func (emptyUpdater) Start()                                  {}
func (emptyUpdater) Stop()                                   {}
func (emptyUpdater) ChangeValue(name string, v []byte) error { return nil }

// serviceUpdater updates service-discovery entry of locally running App.
type serviceUpdater struct {
	client   *servicedisc.HTTPClient
	interval time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
}

func (u *serviceUpdater) Start() {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.cancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel

	u.wg.Add(1)
	go func() {
		u.client.UpdateLoop(ctx, u.interval)
		u.wg.Done()
	}()
}

func (u *serviceUpdater) Stop() {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.cancel == nil {
		return
	}

	u.cancel()
	u.cancel = nil
	u.wg.Wait()
}

func (u *serviceUpdater) ChangeValue(name string, v []byte) error {
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
