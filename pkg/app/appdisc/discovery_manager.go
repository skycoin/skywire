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

// PublicVisorUpdater wraps a serviceUpdater with public visor validation logic.
// It monitors for external STCPR connections and transport count to determine
// if the visor should stay registered in service discovery.
type PublicVisorUpdater struct {
	inner               *serviceUpdater
	log                 logrus.FieldLogger
	registrationTimeout time.Duration
	maxTransports       int
	getTransportCount   func() int // callback to get current transport count

	validated       bool // whether external connection was received
	validatedMu     sync.RWMutex
	externalCh      chan struct{} // receives signal on external STCPR connection
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	stopOnce        sync.Once
	deregisteredFor string // reason for deregistration (empty if still registered)
}

// NewPublicVisorUpdater creates a new public visor updater with validation logic.
func NewPublicVisorUpdater(
	log logrus.FieldLogger,
	inner *serviceUpdater,
	registrationTimeout time.Duration,
	maxTransports int,
	getTransportCount func() int,
) *PublicVisorUpdater {
	return &PublicVisorUpdater{
		inner:               inner,
		log:                 log,
		registrationTimeout: registrationTimeout,
		maxTransports:       maxTransports,
		getTransportCount:   getTransportCount,
		externalCh:          make(chan struct{}, 1),
	}
}

// OnExternalSTCPR should be called when an external STCPR connection is received.
// This validates that the visor is reachable from the internet.
func (u *PublicVisorUpdater) OnExternalSTCPR() {
	u.validatedMu.Lock()
	if !u.validated {
		u.validated = true
		u.log.Info("Public visor validated: received external STCPR connection")
	}
	u.validatedMu.Unlock()

	// Non-blocking send to wake up the monitor loop
	select {
	case u.externalCh <- struct{}{}:
	default:
	}
}

// IsValidated returns whether the visor has received an external connection.
func (u *PublicVisorUpdater) IsValidated() bool {
	u.validatedMu.RLock()
	defer u.validatedMu.RUnlock()
	return u.validated
}

// Start starts the updater with validation monitoring.
func (u *PublicVisorUpdater) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel

	// Start the underlying service updater
	u.inner.Start()

	// Start the validation monitor
	u.wg.Add(1)
	go u.monitorLoop(ctx)
}

func (u *PublicVisorUpdater) monitorLoop(ctx context.Context) {
	defer u.wg.Done()

	var timeoutCh <-chan time.Time
	if u.registrationTimeout > 0 {
		timer := time.NewTimer(u.registrationTimeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	// Check transport count periodically
	transportTicker := time.NewTicker(1 * time.Minute)
	defer transportTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-timeoutCh:
			// Registration timeout elapsed
			if !u.IsValidated() {
				u.log.Warn("Public visor validation timeout: no external STCPR connection received. Deregistering from service discovery.")
				u.deregister("no_external_stcpr")
				return
			}
			// Validated, disable further timeout checks
			timeoutCh = nil

		case <-u.externalCh:
			// External connection received - visor is validated
			// Continue monitoring for max transports

		case <-transportTicker.C:
			// Check transport count
			if u.maxTransports > 0 && u.getTransportCount != nil {
				count := u.getTransportCount()
				if count >= u.maxTransports {
					u.log.Infof("Public visor reached max transports (%d/%d). Deregistering from service discovery.", count, u.maxTransports)
					u.deregister("max_transports")
					return
				}
			}
		}
	}
}

func (u *PublicVisorUpdater) deregister(reason string) {
	u.deregisteredFor = reason
	u.inner.Stop()
}

// Stop stops the updater.
func (u *PublicVisorUpdater) Stop() {
	u.stopOnce.Do(func() {
		if u.cancel != nil {
			u.cancel()
		}
		u.wg.Wait()

		// Stop inner updater if not already stopped
		if u.deregisteredFor == "" {
			u.inner.Stop()
		}
	})
}
