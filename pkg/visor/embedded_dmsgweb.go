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
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Default SOCKS5 listener port — matches `skywire dmsg web` so the two
// surfaces behave identically from a browser's perspective.
const defaultDmsgWebProxyPort = 4445

// dmsgWebReadyWait bounds how long serve() waits for the dmsg client to be
// ready before binding the listener anyway.
const dmsgWebReadyWait = 20 * time.Second

// EmbeddedDmsgWeb holds the runtime state for the visor-hosted
// dmsgweb resolver. Safe for concurrent Start/Stop calls.
type EmbeddedDmsgWeb struct {
	dmsgC    *dmsg.Client
	cfg      *visorconfig.DmsgWebConfig
	log      *logging.Logger
	stats    *dmsgweb.Stats                      // persists across Start/Stop cycles
	localPK  cipher.PubKey                       // this visor's PK, for self-loopback + "self" alias
	selfDial func(port uint16) (net.Conn, error) // in-process serve of a local service port (nil disables loopback)

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}
	parentCtx context.Context // captured so Start() without args still works after construction
}

// newEmbeddedDmsgWeb is the constructor used by initEmbeddedDmsgWeb.
// Stats starts counting from construction, so UI can distinguish
// "resolver never ran" from "running but idle". localPK + selfDial
// enable self-loopback (serving requests for this visor's own PK
// in-process); pass a zero PK / nil selfDial to disable.
func newEmbeddedDmsgWeb(parentCtx context.Context, dmsgC *dmsg.Client, localPK cipher.PubKey, selfDial func(uint16) (net.Conn, error), cfg *visorconfig.DmsgWebConfig, log *logging.Logger) *EmbeddedDmsgWeb {
	return &EmbeddedDmsgWeb{
		dmsgC:     dmsgC,
		cfg:       cfg,
		log:       log,
		stats:     dmsgweb.NewStats(),
		localPK:   localPK,
		selfDial:  selfDial,
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

// SetUpstream changes the upstream SOCKS5 address and restarts the
// resolver so the new upstream takes effect immediately.
func (e *EmbeddedDmsgWeb) SetUpstream(addr string) error {
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
func (e *EmbeddedDmsgWeb) Upstream() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.UpstreamSOCKS
}

func (e *EmbeddedDmsgWeb) serve(ctx context.Context) {
	cfg := dmsgweb.Config{
		DomainSuffix:  stringOrDefault(e.cfg.DomainSuffix, dmsgweb.DefaultDomainSuffix),
		ProxyPort:     uintOrDefault(e.cfg.ProxyPort, defaultDmsgWebProxyPort),
		UpstreamSOCKS: e.cfg.UpstreamSOCKS,
		Stats:         e.stats,
	}

	// Self-loopback: serve requests for THIS visor's own PK in-process
	// (default on) instead of dialing over dmsg back to self. Aliases map
	// friendly labels (default "skywire") to a PK or self.
	if e.selfDial != nil && (e.cfg.SelfLoopback == nil || *e.cfg.SelfLoopback) {
		cfg.LocalPK = e.localPK
		cfg.SelfLoopback = true
		cfg.SelfDial = e.selfDial
	}
	aliases, err := resolverAliases(e.cfg.Aliases, e.localPK)
	if err != nil {
		e.log.WithError(err).Warn("dmsgweb: invalid alias config; using default skywire->self")
		aliases = map[string]cipher.PubKey{"skywire": e.localPK}
	}
	cfg.Aliases = aliases

	// Optional TLS MITM. CA load failure is non-fatal — the
	// resolver continues without MITM and logs the reason.
	if e.cfg.TLSMITM {
		if err := wireDmsgTLSMITM(&cfg, e.cfg, e.log); err != nil {
			e.log.WithError(err).Warn("dmsgweb TLS MITM disabled")
		}
	}

	// Best-effort, BOUNDED wait for the dmsg client so the first request
	// doesn't race the initial discovery publish — but never block the
	// listener bind indefinitely (the app must come up and accept
	// connections regardless; early requests just fail until dmsg is ready).
	select {
	case <-e.dmsgC.Ready():
	case <-time.After(dmsgWebReadyWait):
		e.log.Warn("dmsgweb: dmsg client not ready within timeout; serving anyway")
	case <-ctx.Done():
		return
	}

	e.log.WithField("socks_port", cfg.ProxyPort).
		WithField("domain", cfg.DomainSuffix).
		WithField("tls_mitm", cfg.TLSMITM).
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

// resolverAliases turns the config alias map (label → "self" | "<pk-hex>")
// into a label → PK map for ParseResolverHost. "self" resolves to localPK.
// The label "skywire" → self is included by default; map it to "" to
// remove that default, or to another target to override it. Shared by the
// dmsgweb and skynetweb resolvers.
func resolverAliases(cfg map[string]string, localPK cipher.PubKey) (map[string]cipher.PubKey, error) {
	out := map[string]cipher.PubKey{"skywire": localPK}
	for label, target := range cfg {
		switch target {
		case "":
			delete(out, label) // explicit disable / remove default
		case "self":
			out[label] = localPK
		default:
			var pk cipher.PubKey
			if err := pk.Set(target); err != nil {
				return nil, fmt.Errorf("alias %q: invalid PK %q: %w", label, target, err)
			}
			out[label] = pk
		}
	}
	return out, nil
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

	// NOTE: do NOT block here on v.dmsgC.Ready(). This init runs under a
	// bounded boot context; when dmsg is slow to publish (e.g. churn right
	// after a restart) the wait outlived that context, returned early, and
	// the app was never registered — so its SOCKS5 listener never bound
	// (4445), while skynetweb (which doesn't wait on dmsg) came up fine. The
	// readiness wait now lives in serve(), bounded, so the app always
	// registers and binds; only the first requests race a not-yet-ready dmsg.

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

	runtime := newEmbeddedDmsgWeb(ctx, v.dmsgC, v.conf.PK, v.services.SelfDial, cfg, log)
	v.initLock.Lock()
	v.embeddedDmsgWeb = runtime
	v.initLock.Unlock()

	// Register as an Internal app (RFC #2775 Phase 3.2). The
	// launcher's AutoStart pass brings the SOCKS5 listener up when
	// cfg.Enable=true; visor halt tears it down via procM.Close.
	// RestartPolicy=Never so operator-driven toggles via
	// `cli visor app start|stop dmsgweb` behave as expected (no
	// surprise auto-restart on stop — different from pty).
	launcher.RegisterApp(skyenvDmsgWebApp, buildDmsgWebAppFunc(runtime, log))
	return nil
}

// skyenvDmsgWebApp is the launcher-registered name for the embedded
// dmsgweb SOCKS5 proxy. Operators see it in `cli visor app ls` and
// can toggle it via `cli visor app start|stop dmsgweb`.
const skyenvDmsgWebApp = "dmsgweb"

// buildDmsgWebAppFunc wraps the EmbeddedDmsgWeb runtime in the
// AppFunc contract — the launcher invokes it on app start; the
// returned func opens an app.Client to complete the in-process
// IPC handshake, calls Start, blocks on ctx cancel, then calls
// Stop. Same shape as buildPtyAppFunc in init_dmsg.go.
func buildDmsgWebAppFunc(rt *EmbeddedDmsgWeb, log *logging.Logger) appcommon.AppFunc {
	return func(ctx context.Context, _ []string) error {
		appCl := app.NewClient(nil)
		defer appCl.Close()
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)
		if err := rt.Start(); err != nil {
			log.WithError(err).Warn("dmsgweb Start failed")
			appCl.SetErrorOrLog(err)
			return err
		}
		appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
		<-ctx.Done()
		if err := rt.Stop(); err != nil {
			log.WithError(err).Warn("dmsgweb Stop failed")
		}
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
		return nil
	}
}
