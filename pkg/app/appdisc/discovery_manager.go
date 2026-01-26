// Package appdisc updates app discovery
package appdisc

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
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
	client            *servicedisc.HTTPClient
	log               logrus.FieldLogger
	heartbeatInterval time.Duration
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	stopOnce          sync.Once
}

// newServiceUpdater creates a new serviceUpdater with heartbeat support.
func newServiceUpdater(log logrus.FieldLogger, client *servicedisc.HTTPClient, heartbeatInterval time.Duration) *serviceUpdater {
	if heartbeatInterval <= 0 {
		heartbeatInterval = skyenv.ServiceDiscUpdateInterval
	}
	return &serviceUpdater{
		client:            client,
		log:               log,
		heartbeatInterval: heartbeatInterval,
	}
}

func (u *serviceUpdater) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel

	// Initial registration
	if err := u.client.Register(ctx); err != nil {
		u.log.WithError(err).Error("Failed to register service")
		return
	}

	// Start heartbeat loop
	u.wg.Add(1)
	go u.heartbeatLoop(ctx)
}

func (u *serviceUpdater) heartbeatLoop(ctx context.Context) {
	defer u.wg.Done()

	ticker := time.NewTicker(u.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := u.client.Register(ctx); err != nil {
				u.log.WithError(err).Warn("Failed to send heartbeat to service discovery")
			} else {
				u.log.Debug("Service discovery heartbeat sent successfully")
			}
		}
	}
}

func (u *serviceUpdater) Stop() {
	u.stopOnce.Do(func() {
		if u.cancel != nil {
			u.cancel()
		}
		u.wg.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := u.client.DeleteEntry(ctx); err != nil {
			u.log.WithError(err).Warn("Failed to delete service entry")
		}
	})
}
