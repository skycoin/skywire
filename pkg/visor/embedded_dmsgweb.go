// Package visor pkg/visor/embedded_dmsgweb.go
//
// EmbeddedDmsgWeb is the in-process version of the `skywire dmsg web`
// utility. It runs a localhost SOCKS5 proxy + HTTP bridge that
// resolves `.dmsg` (or a configured domain suffix) hostnames by
// tunneling requests through the visor's own dmsg client.
//
// Why in-process rather than a separate app: running standalone
// requires spinning up a second dmsg identity with its own sessions,
// doubling server load for the same PK's worth of connectivity. By
// sharing the visor's client the resolver piggybacks on sessions that
// already exist for routing / health / transport probes. This
// mirrors the setup-node sharing pattern (see embedded_route_setup.go).
//
// Runtime supervision (Start/Stop) is local to this type so the RPC
// layer can flip resolvers on and off without restarting the visor.
// Stats live on the struct (not inside pkg/dmsgweb) so counters
// survive Start→Stop→Start cycles.
package visor

import (
	"context"
	"fmt"
	"sync"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Default listener ports — match `skywire dmsg web` defaults so the
// two surfaces behave the same from a browser's perspective.
const (
	defaultDmsgWebProxyPort = 4445
	defaultDmsgWebPort      = 8080
)

// EmbeddedDmsgWeb holds the runtime state for the visor-hosted
// dmsgweb resolver. Safe for concurrent Start/Stop calls.
type EmbeddedDmsgWeb struct {
	dmsgC *dmsg.Client
	cfg   *visorconfig.DmsgWebConfig
	log   *logging.Logger
	stats *dmsgweb.Stats // persists across Start/Stop cycles

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}
	parentCtx context.Context // captured so Start() without args still works after construction
}

// newEmbeddedDmsgWeb is the constructor used by initEmbeddedDmsgWeb.
// Stats starts counting from construction, so UI can distinguish
// "resolver never ran" from "running but idle".
func newEmbeddedDmsgWeb(parentCtx context.Context, dmsgC *dmsg.Client, cfg *visorconfig.DmsgWebConfig, log *logging.Logger) *EmbeddedDmsgWeb {
	return &EmbeddedDmsgWeb{
		dmsgC:     dmsgC,
		cfg:       cfg,
		log:       log,
		stats:     dmsgweb.NewStats(),
		parentCtx: parentCtx,
	}
}

// Start spawns the resolver goroutine. Idempotent: calling Start
// while already running is a no-op. Returns an error if prerequisites
// (dmsg client) are missing.
func (e *EmbeddedDmsgWeb) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}
	if e.dmsgC == nil {
		return fmt.Errorf("dmsgweb: no dmsg client available")
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
	e.log.Info("Embedded dmsgweb started")
	return nil
}

// Stop signals the resolver to shut down and blocks until its
// goroutine returns. Idempotent.
func (e *EmbeddedDmsgWeb) Stop() error {
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
	e.log.Info("Embedded dmsgweb stopped")
	return nil
}

// IsRunning reports whether the resolver goroutine is currently alive.
func (e *EmbeddedDmsgWeb) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Stats returns a snapshot of cumulative counters (across all
// Start/Stop cycles).
func (e *EmbeddedDmsgWeb) Stats() dmsgweb.StatsSnapshot {
	return e.stats.Snapshot()
}

func (e *EmbeddedDmsgWeb) serve(ctx context.Context) {
	cfg := dmsgweb.Config{
		DomainSuffix:  stringOrDefault(e.cfg.DomainSuffix, dmsgweb.DefaultDomainSuffix),
		WebPorts:      []uint{uintOrDefault(e.cfg.WebPort, defaultDmsgWebPort)},
		ProxyPort:     uintOrDefault(e.cfg.ProxyPort, defaultDmsgWebProxyPort),
		UpstreamSOCKS: e.cfg.UpstreamSOCKS,
		Stats:         e.stats,
	}
	e.log.WithField("socks_port", cfg.ProxyPort).
		WithField("web_port", cfg.WebPorts[0]).
		WithField("domain", cfg.DomainSuffix).
		Info("Serving dmsgweb resolver")
	if err := dmsgweb.Run(ctx, e.log, e.dmsgC, cfg); err != nil && err != context.Canceled {
		e.log.WithError(err).Warn("dmsgweb runtime stopped")
	}
}

func stringOrDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func uintOrDefault(v, d uint) uint {
	if v == 0 {
		return d
	}
	return v
}

// initEmbeddedDmsgWeb constructs the runtime (always, if config is
// present) so it can be started and stopped via RPC later, then
// optionally calls Start() when Enable=true. This differs from the
// usual "skip if disabled" pattern because we want the UI to be able
// to flip Enable at runtime without a visor restart.
func initEmbeddedDmsgWeb(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf == nil || v.conf.DmsgWeb == nil {
		log.Debug("dmsg_web section absent; not constructing resolver")
		return nil
	}
	if v.dmsgC == nil {
		log.Warn("dmsg_web configured but dmsg client not available; skipping")
		return nil
	}

	// Wait for the visor's dmsg client to be ready before spinning up
	// the resolver so the first browser request doesn't race the
	// initial discovery publish.
	select {
	case <-v.dmsgC.Ready():
	case <-ctx.Done():
		return ctx.Err()
	}

	// Auto-chain: if the config doesn't explicitly set an upstream
	// and skynet_web is also configured, wire dmsgweb → skynetweb so
	// one browser proxy entry covers both .dmsg and .skynet.
	cfg := v.conf.DmsgWeb
	if cfg.UpstreamSOCKS == "" && v.conf.SkynetWeb != nil {
		cfg.UpstreamSOCKS = fmt.Sprintf("127.0.0.1:%d",
			uintOrDefault(v.conf.SkynetWeb.ProxyPort, defaultSkynetWebProxyPort))
		log.WithField("upstream", cfg.UpstreamSOCKS).
			Info("Auto-chaining dmsgweb → skynetweb for unified proxy")
	}

	runtime := newEmbeddedDmsgWeb(ctx, v.dmsgC, cfg, log)
	v.initLock.Lock()
	v.embeddedDmsgWeb = runtime
	v.initLock.Unlock()

	if cfg.Enable {
		if err := runtime.Start(); err != nil {
			log.WithError(err).Warn("failed to auto-start dmsgweb")
		}
	} else {
		log.Info("Embedded dmsgweb constructed but not started (enable=false); toggle via RPC")
	}
	return nil
}
