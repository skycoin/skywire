// Package visor pkg/visor/embedded_dmsgweb.go c3-vis-core
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
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
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
	// selfDialAs is the PK-stamping variant of selfDial: it makes the
	// in-process self-loopback conn present remotePK as the caller. Used to
	// render the self view as authenticated (with localPK). Nil falls back to
	// the unauthenticated selfDial.
	selfDialAs func(port uint16, remotePK cipher.PubKey) (net.Conn, error)

	// serviceAliases maps canonical short labels (tpd, ar, rf, sd, ut, dmsgd,
	// rsn*, tsn*, dmsg*) to the configured deployment services' PKs, so the
	// resolving proxy can reach a service's HTTP API by name (e.g.
	// http://tpd.dmsg/). Derived once from the visor config at construction;
	// empty when no service has a resolvable dmsg PK.
	serviceAliases map[string]cipher.PubKey

	// directClient + directServerPKs let the resolver reach dmsg SERVERS, which
	// serve over dmsg-HTTP but register no discovery client entry. A dest in
	// directServerPKs is dialed through directClient by self-rendezvous instead
	// of the discovery dial. directClient is the visor's direct dmsg client
	// (v.dmsgDC); nil disables the path.
	directClient    *dmsg.Client
	directServerPKs map[cipher.PubKey]struct{}

	// statusProvider serves the reserved in-process status hosts through this
	// proxy (http://status.dmsg/ etc.). Set by initEmbeddedDmsgWeb; nil disables
	// the status hosts.
	statusProvider proxystatus.Provider

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
func newEmbeddedDmsgWeb(parentCtx context.Context, dmsgC, directClient *dmsg.Client, localPK cipher.PubKey, selfDial func(uint16) (net.Conn, error), selfDialAs func(uint16, cipher.PubKey) (net.Conn, error), serviceAliases map[string]cipher.PubKey, directServerPKs map[cipher.PubKey]struct{}, cfg *visorconfig.DmsgWebConfig, log *logging.Logger) *EmbeddedDmsgWeb {
	return &EmbeddedDmsgWeb{
		dmsgC:           dmsgC,
		cfg:             cfg,
		log:             log,
		stats:           dmsgweb.NewStats(),
		localPK:         localPK,
		selfDial:        selfDial,
		selfDialAs:      selfDialAs,
		serviceAliases:  serviceAliases,
		directClient:    directClient,
		directServerPKs: directServerPKs,
		parentCtx:       parentCtx,
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

// SetBind changes the SOCKS5 bind host and restarts the resolver so it takes
// effect immediately. "" = loopback (default); "0.0.0.0"/a LAN IP serves the LAN.
func (e *EmbeddedDmsgWeb) SetBind(addr string) error {
	e.mu.Lock()
	e.cfg.ProxyAddr = addr
	wasRunning := e.running
	e.mu.Unlock()

	if wasRunning {
		if err := e.Stop(); err != nil {
			return fmt.Errorf("stop before bind change: %w", err)
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
		ProxyAddr:     e.cfg.ProxyAddr,
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
		// Self-loopback-authenticated (default on): present THIS visor's own
		// PK as the caller so the in-process self view renders the
		// authenticated landing page (pprof, /visor.log, …). The visor's PK
		// is always on its own landing-page whitelist; local self-access
		// through one's own proxy is inherently authorized. Disable with
		// self_loopback_authenticated:false to render the self view anonymously.
		if e.selfDialAs != nil && (e.cfg.SelfLoopbackAuthenticated == nil || *e.cfg.SelfLoopbackAuthenticated) {
			localPK := e.localPK
			cfg.SelfDial = func(port uint16) (net.Conn, error) {
				return e.selfDialAs(port, localPK)
			}
		}
	}
	// Canonical service aliases (tpd, ar, rf, …) first, then this visor's own
	// alias on top so a colliding self-alias always wins.
	cfg.Aliases = map[string]cipher.PubKey{}
	for label, pk := range e.serviceAliases {
		cfg.Aliases[label] = pk
	}
	for label, pk := range resolverAliasMap(e.cfg.Alias, e.localPK) {
		cfg.Aliases[label] = pk
	}
	// Direct-client path for non-discovery dmsg servers.
	cfg.DirectClient = e.directClient
	cfg.DirectServerPKs = e.directServerPKs
	// Reserved in-process status hosts (http://status.dmsg/ etc.).
	cfg.StatusProvider = e.statusProvider

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

// resolverAliasMap builds the single-entry label → PK map ParseResolverHost
// uses to resolve a friendly name for THIS visor. The label defaults to
// "skywire" when alias is empty (so "skywire.dmsg"/"skywire.skynet" works
// out of the box). Shared by the dmsgweb and skynetweb resolvers.
func resolverAliasMap(alias string, localPK cipher.PubKey) map[string]cipher.PubKey {
	if alias == "" {
		alias = "skywire"
	}
	return map[string]cipher.PubKey{alias: localPK}
}

// serviceAliasMap derives canonical short aliases for the deployment services
// this visor is configured to use, by pulling each service's PK out of its
// dmsg:// URL. The resolving proxy then reaches a service's HTTP API by name —
// e.g. `curl -x socks5h://127.0.0.1:4445 http://tpd.dmsg/health`. Services
// without a resolvable dmsg PK (plain-HTTP-only or unset) are simply omitted.
// The label set mirrors the existing `cli` service namespace (tpd, ar, rf, …).
func serviceAliasMap(conf *visorconfig.V1) map[string]cipher.PubKey {
	m := map[string]cipher.PubKey{}
	if conf == nil {
		return m
	}
	// add registers the first candidate URL that carries a dmsg PK. The *Dmsg
	// field is preferred; the primary field is a fallback for configs that put
	// the dmsg URL there directly.
	add := func(alias string, candidates ...string) {
		for _, raw := range candidates {
			if pk := dmsgspec.PKFromDmsgURL(raw); !pk.Null() {
				m[alias] = pk
				return
			}
		}
	}
	if conf.Dmsg != nil {
		add("dmsgd", conf.Dmsg.DiscoveryDmsg, conf.Dmsg.Discovery)
	}
	if conf.Transport != nil {
		add("tpd", conf.Transport.DiscoveryDmsg, conf.Transport.Discovery)
		add("ar", conf.Transport.AddressResolverDmsg, conf.Transport.AddressResolver)
	}
	if conf.Routing != nil {
		add("rf", conf.Routing.RouteFinderDmsg, conf.Routing.RouteFinder)
	}
	if conf.Launcher != nil {
		add("sd", conf.Launcher.ServiceDiscDmsg, conf.Launcher.ServiceDisc)
	}
	if conf.UptimeTracker != nil {
		add("ut", conf.UptimeTracker.AddrDmsg, conf.UptimeTracker.Addr)
	}
	// Reward system (top-level config fields, not a sub-block).
	add("rewards", conf.RewardSystemDmsg, conf.RewardSystem)

	// Setup nodes are PK lists (not URLs) that serve /health over a dialable
	// dmsg stream — verified reachable through the resolving proxy. Index them
	// rsn0.. / tsn0.. with the bare alias pointing at the first.
	//
	// dmsg SERVERS are deliberately NOT aliased: they're relays whose /health
	// lives on a clearnet metrics port, not on a dmsg stream, so the proxy
	// cannot reach them by PK.
	addList := func(prefix string, pks []cipher.PubKey) {
		for i, pk := range pks {
			if pk.Null() {
				continue
			}
			m[fmt.Sprintf("%s%d", prefix, i)] = pk
			if i == 0 {
				m[prefix] = pk
			}
		}
	}
	addList("rsn", conf.EffectiveRouteSetupNodes())
	addList("tsn", conf.EffectiveTransportSetupPKs())
	// dmsg-server aliases (dmsg0..) are added separately from the LIVE server
	// list, not this static config — see dmsgServerAliases / liveDmsgServerEntries.
	return m
}

// liveDmsgServerEntries returns the visor's current dmsg-server set: the
// config's bootstrap servers merged with (and preferring) the on-disk cache
// the background loop refreshes from dmsg-discovery's all_servers. This is the
// same list the dmsg clients actually use, so aliases track reality instead of
// the possibly-stale embedded config (which is bootstrap-only).
func liveDmsgServerEntries(v *Visor) []*dmsgdisc.Entry {
	if v == nil || v.conf == nil || v.conf.Dmsg == nil {
		return nil
	}
	// Prefer the live cache (refreshed from dmsg-discovery's all_servers) so
	// aliases reflect what discovery currently advertises, NOT the stale
	// bootstrap config. MergePreferringCache unions config+cache by PK, which
	// would re-introduce decommissioned config-only PKs — so pass nil to get
	// exactly the cache contents instead.
	if v.dmsgServersCache != nil {
		if live := v.dmsgServersCache.MergePreferringCache(nil); len(live) > 0 {
			return live
		}
	}
	// Bootstrap fallback: empty cache (fresh visor, before the first refresh).
	return v.conf.Dmsg.ResolvedServers()
}

// dmsgServerAliases builds dmsg0.. aliases and the matching direct-dial PK set
// from a server list, sorted by PK for stable indices across restarts. dmsg
// servers serve /health (etc.) over dmsg-HTTP but register no discovery client
// entry, so they're reached via the resolver's direct client (self-rendezvous),
// not the discovery dial. Null PKs are skipped.
func dmsgServerAliases(servers []*dmsgdisc.Entry) (map[string]cipher.PubKey, map[cipher.PubKey]struct{}) {
	pks := make([]cipher.PubKey, 0, len(servers))
	for _, e := range servers {
		if e == nil || e.Static.Null() {
			continue
		}
		pks = append(pks, e.Static)
	}
	sort.Slice(pks, func(i, j int) bool { return pks[i].Hex() < pks[j].Hex() })
	aliases := make(map[string]cipher.PubKey, len(pks))
	set := make(map[cipher.PubKey]struct{}, len(pks))
	for i, pk := range pks {
		aliases[fmt.Sprintf("dmsg%d", i)] = pk
		set[pk] = struct{}{}
	}
	return aliases, set
}

// resolverAliasesAndDmsgServers assembles the full alias map (URL services +
// setup nodes from static config, plus dmsg0.. from the LIVE server list) and
// the direct-dial dmsg-server PK set, for the embedded dmsg resolver.
func resolverAliasesAndDmsgServers(v *Visor) (map[string]cipher.PubKey, map[cipher.PubKey]struct{}) {
	aliases := serviceAliasMap(v.conf)
	dmsgAliases, set := dmsgServerAliases(liveDmsgServerEntries(v))
	for label, pk := range dmsgAliases {
		aliases[label] = pk
	}
	return aliases, set
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

	aliases, dmsgSet := resolverAliasesAndDmsgServers(v)
	runtime := newEmbeddedDmsgWeb(ctx, v.dmsgC, v.dmsgDC, v.conf.PK, v.services.SelfDial, v.services.SelfDialAs, aliases, dmsgSet, cfg, log)
	runtime.statusProvider = v.proxyStatusProvider()
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
