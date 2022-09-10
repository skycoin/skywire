// Package appdisc updates app discovery
package appdisc

import (
	"context"
	"sync"

	"github.com/skycoin/skywire/pkg/servicedisc"
)

// Updater updates the associated app discovery
type Updater interface {

	// Start starts the updater.
	Start()

	// Stop stops the updater.
	Stop()
}

// emptyUpdater is for apps that do not require discovery updates.
type emptyUpdater struct{}

func (emptyUpdater) Start() {}
func (emptyUpdater) Stop()  {}

// serviceUpdater updates service-discovery entry of locally running App.
type serviceUpdater struct {
	client   *servicedisc.HTTPClient
	stopOnce sync.Once
}

func (u *serviceUpdater) Start() {
	ctx := context.Background()
	if err := u.client.Register(ctx); err != nil {
		return
	}
}

func (u *serviceUpdater) Stop() {
	u.stopOnce.Do(func() {
		ctx := context.Background()
		if err := u.client.DeleteEntry(ctx); err != nil {
			return
		}
	})
}
