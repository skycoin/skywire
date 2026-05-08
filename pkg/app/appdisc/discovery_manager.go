// Package appdisc updates app discovery
package appdisc

import (
	"context"
	"sync"
	"sync/atomic"
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
	// paused, when true, makes the heartbeat loop skip its Register
	// call. Pause() additionally calls DeleteEntry once so the SD
	// entry goes away promptly; Resume() flips the flag back and
	// kicks an immediate re-registration. The loop itself stays
	// alive across pause cycles — operators using PublicVisorUpdater
	// expect transient drain (e.g. max_transports) to be reversible
	// when the situation clears, not a permanent removal.
	paused atomic.Bool
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
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is stored in u.cancel and called in Stop()
	u.cancel = cancel

	// Best-effort initial registration. A failure here used to return
	// early without starting the heartbeat loop, permanently disabling
	// re-registration for the rest of the visor's lifetime — which
	// turned a single transient SD failure (probe timeout, SD restart,
	// dmsg flake) into "this visor never appears in service-discovery
	// until it is restarted." Always start the heartbeat: it retries
	// every ServiceDiscUpdateInterval (90s), which is exactly the
	// recovery mechanism callers expect.
	if err := u.client.Register(ctx); err != nil {
		u.log.WithError(err).Warn("Initial service discovery registration failed; will retry via heartbeat")
	}

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
			if u.paused.Load() {
				// Drained state — caller (e.g. PublicVisorUpdater
				// after hitting max_transports) wants no SD entry
				// for now. Stay alive so Resume can flip us back.
				continue
			}
			if err := u.client.Register(ctx); err != nil {
				u.log.WithError(err).Warn("Failed to send heartbeat to service discovery")
			} else {
				u.log.Debug("Service discovery heartbeat sent successfully")
			}
		}
	}
}

// Pause flags the heartbeat loop to stop publishing the SD entry and
// removes any currently-published entry. The loop itself keeps running
// so Resume can flip the flag back without a fresh goroutine. Idempotent.
func (u *serviceUpdater) Pause() {
	if !u.paused.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := u.client.DeleteEntry(ctx); err != nil {
		u.log.WithError(err).Warn("Failed to delete service entry on pause")
	}
}

// Resume flips paused off and immediately re-registers so the SD
// entry reappears without waiting for the next heartbeat tick.
// Idempotent. No-op if Stop has already been called (cancel is nil).
func (u *serviceUpdater) Resume() {
	if !u.paused.CompareAndSwap(true, false) {
		return
	}
	if u.cancel == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := u.client.Register(ctx); err != nil {
		u.log.WithError(err).Warn("Re-registration on resume failed; will retry via heartbeat")
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
	// drained tracks whether we've called inner.Pause() because the
	// transport count crossed maxTransports. While drained the SD
	// entry is gone; the monitor loop watches for the count to drop
	// back below the resume threshold (90% of maxTransports — small
	// hysteresis to prevent flapping right at the boundary) and then
	// calls inner.Resume(). Without this, hitting the cap once was a
	// permanent removal until visor restart, and the visor's other
	// public siblings frequently disappeared from service discovery
	// after a single transient spike.
	drained bool
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
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is stored in u.cancel and called in Stop()
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
			// Check transport count, manage drained <-> active state.
			if u.maxTransports > 0 && u.getTransportCount != nil {
				count := u.getTransportCount()
				switch {
				case !u.drained && count >= u.maxTransports:
					u.log.Infof("Public visor reached max transports (%d/%d). Pausing service-discovery registration.", count, u.maxTransports)
					u.inner.Pause()
					u.drained = true
				case u.drained && count < (u.maxTransports*9)/10:
					// Hysteresis: only resume once we're meaningfully
					// under the cap (90%). Prevents flapping when the
					// count oscillates around maxTransports.
					u.log.Infof("Public visor transport count fell to %d/%d. Resuming service-discovery registration.", count, u.maxTransports)
					u.inner.Resume()
					u.drained = false
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
