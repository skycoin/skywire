// Package visor pkg/visor/embedded_skynetweb.go
//
// EmbeddedSkynetWeb hosts the `.skynet` resolving proxy inside the
// visor. Mirrors EmbeddedDmsgWeb's lifecycle (Start/Stop + Stats)
// but speaks the skynet protocol (route + handshake) instead of
// DMSG.
//
// Skynet dialing requires establishing a route group — a
// router-internal operation. This file defines the adapter
// (routerSkynetDialer) that implements pkg/skynetweb.SkynetDialer on
// top of router.Router.
package visor

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Defaults — chosen to not clash with DmsgWeb's defaults (4445/8080)
// so both resolvers can run side by side.
const (
	defaultSkynetWebProxyPort = 4446
	defaultSkynetWebPort      = 8081
)

// EmbeddedSkynetWeb holds the runtime state for the visor-hosted
// skynetweb resolver.
type EmbeddedSkynetWeb struct {
	router  router.Router
	localPK cipher.PubKey
	cfg     *visorconfig.SkynetWebConfig
	log     *logging.Logger
	stats   *skynetweb.Stats

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}
	parentCtx context.Context
}

func newEmbeddedSkynetWeb(parentCtx context.Context, r router.Router, localPK cipher.PubKey, cfg *visorconfig.SkynetWebConfig, log *logging.Logger) *EmbeddedSkynetWeb {
	return &EmbeddedSkynetWeb{
		router:    r,
		localPK:   localPK,
		cfg:       cfg,
		log:       log,
		stats:     skynetweb.NewStats(),
		parentCtx: parentCtx,
	}
}

// Start spawns the resolver goroutine. Idempotent.
func (e *EmbeddedSkynetWeb) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}
	if e.router == nil {
		return fmt.Errorf("skynetweb: no router available")
	}
	ctx, cancel := context.WithCancel(e.parentCtx)
	e.cancel = cancel
	e.done = make(chan struct{})
	e.running = true

	go func() {
		defer close(e.done)
		e.serve(ctx)
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()
	e.log.Info("Embedded skynetweb started")
	return nil
}

// Stop cancels the running resolver and waits for it to exit.
func (e *EmbeddedSkynetWeb) Stop() error {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return nil
	}
	cancel := e.cancel
	done := e.done
	e.mu.Unlock()

	cancel()
	<-done
	e.log.Info("Embedded skynetweb stopped")
	return nil
}

// IsRunning reports whether the resolver goroutine is currently alive.
func (e *EmbeddedSkynetWeb) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Stats returns cumulative counters.
func (e *EmbeddedSkynetWeb) Stats() skynetweb.StatsSnapshot {
	return e.stats.Snapshot()
}

// SetUpstream changes the upstream SOCKS5 address and restarts the
// resolver so the new upstream takes effect immediately.
// Pass "" to clear the upstream (non-.skynet traffic connects direct).
func (e *EmbeddedSkynetWeb) SetUpstream(addr string) error {
	e.mu.Lock()
	e.cfg.UpstreamSOCKS = addr
	wasRunning := e.running
	e.mu.Unlock()

	if wasRunning {
		if err := e.Stop(); err != nil {
			return fmt.Errorf("stop before upstream change: %w", err)
		}
		return e.Start()
	}
	return nil
}

// Upstream returns the current upstream SOCKS5 address.
func (e *EmbeddedSkynetWeb) Upstream() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.UpstreamSOCKS
}

func (e *EmbeddedSkynetWeb) serve(ctx context.Context) {
	cfg := skynetweb.Config{
		DomainSuffix:  stringOrDefault(e.cfg.DomainSuffix, skynetweb.DefaultDomainSuffix),
		ProxyPort:     uintOrDefault(e.cfg.ProxyPort, defaultSkynetWebProxyPort),
		UpstreamSOCKS: e.cfg.UpstreamSOCKS,
		Stats:         e.stats,
	}
	e.log.WithField("socks_port", cfg.ProxyPort).
		WithField("domain", cfg.DomainSuffix).
		Info("Serving skynetweb resolver")

	dialer := &routerSkynetDialer{
		router:       e.router,
		localPK:      e.localPK,
		log:          e.log,
		routeTimeout: time.Duration(e.cfg.RouteTimeout),
	}
	if err := skynetweb.Run(ctx, e.log, dialer, cfg); err != nil && err != context.Canceled {
		e.log.WithError(err).Warn("skynetweb runtime stopped")
	}
}

// routerSkynetDialer adapts router.Router to skynetweb.SkynetDialer.
// See skynetweb.SkynetDialer for the contract.
type routerSkynetDialer struct {
	router       router.Router
	localPK      cipher.PubKey
	log          *logging.Logger
	routeTimeout time.Duration // 0 = use DefaultRouteKeepAlive
	// Ephemeral port counter for unique route descriptors.
	nextPort uint32
}

func (d *routerSkynetDialer) DialSkynet(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error) {
	var opts *router.DialOptions
	if d.routeTimeout > 0 {
		opts = router.DefaultDialOptions()
		opts.KeepAlive = d.routeTimeout
	}
	// Use a unique ephemeral lPort for each dial so route descriptors
	// never collide. The skynet forwarding server only cares about the
	// port in the ClientMsg handshake, not the route descriptor's lPort.
	lPort := routing.Port(atomic.AddUint32(&d.nextPort, 1)) //nolint:gosec // overflow wraps intentionally
	conn, err := d.router.DialRoutes(ctx, remote, lPort, routing.Port(skyenv.SkyForwardingServerPort), opts)
	if err != nil {
		return nil, err
	}
	if err := skynetweb.PerformHandshake(conn, port); err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return conn, nil
}

func initEmbeddedSkynetWeb(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf == nil || v.conf.SkynetWeb == nil {
		log.Debug("skynet_web section absent; not constructing resolver")
		return nil
	}
	if v.router == nil {
		log.Warn("skynet_web configured but router not available; skipping")
		return nil
	}
	runtime := newEmbeddedSkynetWeb(ctx, v.router, v.conf.PK, v.conf.SkynetWeb, log)
	v.initLock.Lock()
	v.embeddedSkynetWeb = runtime
	v.initLock.Unlock()

	if v.conf.SkynetWeb.Enable {
		if err := runtime.Start(); err != nil {
			log.WithError(err).Warn("failed to auto-start skynetweb")
		}
	} else {
		log.Info("Embedded skynetweb constructed but not started (enable=false); toggle via RPC")
	}
	return nil
}
