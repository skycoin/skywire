// Package visor pkg/visor/embedded_skynetweb.go c3-vis-core
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
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/skyroute"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Default SOCKS5 listener port — chosen so it doesn't clash with
// dmsgweb's default (4445) and both resolvers can run side by side.
const defaultSkynetWebProxyPort = 4446

// EmbeddedSkynetWeb holds the runtime state for the visor-hosted
// skynetweb resolver.
type EmbeddedSkynetWeb struct {
	router    router.Router
	tpM       *transport.Manager
	skynetMux **transport.VStreamMux // pointer to visor's mux pointer (late-bound)
	localPK   cipher.PubKey
	selfDial  func(port uint16) (net.Conn, error) // in-process serve of a local service port (nil disables loopback)
	// selfDialAs is the PK-stamping variant of selfDial — see EmbeddedDmsgWeb.
	selfDialAs func(port uint16, remotePK cipher.PubKey) (net.Conn, error)
	cfg        *visorconfig.SkynetWebConfig
	log        *logging.Logger
	stats      *skynetweb.Stats
	// statusProvider serves the reserved in-process status hosts through this
	// proxy (http://status.skynet/ etc.). Set by initEmbeddedSkynetWeb; nil
	// disables the status hosts.
	statusProvider proxystatus.Provider

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}
	parentCtx context.Context
}

func newEmbeddedSkynetWeb(parentCtx context.Context, r router.Router, tpM *transport.Manager, skynetMuxPtr **transport.VStreamMux, localPK cipher.PubKey, selfDial func(uint16) (net.Conn, error), selfDialAs func(uint16, cipher.PubKey) (net.Conn, error), cfg *visorconfig.SkynetWebConfig, log *logging.Logger) *EmbeddedSkynetWeb {
	return &EmbeddedSkynetWeb{
		router:     r,
		tpM:        tpM,
		skynetMux:  skynetMuxPtr,
		localPK:    localPK,
		selfDial:   selfDial,
		selfDialAs: selfDialAs,
		cfg:        cfg,
		log:        log,
		stats:      skynetweb.NewStats(),
		parentCtx:  parentCtx,
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

// SetBind changes the SOCKS5 bind host and restarts the resolver so it takes
// effect immediately. "" = loopback (default); "0.0.0.0"/a LAN IP serves the LAN.
func (e *EmbeddedSkynetWeb) SetBind(addr string) error {
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
func (e *EmbeddedSkynetWeb) Upstream() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.UpstreamSOCKS
}

func (e *EmbeddedSkynetWeb) serve(ctx context.Context) {
	cfg := skynetweb.Config{
		DomainSuffix:  stringOrDefault(e.cfg.DomainSuffix, skynetweb.DefaultDomainSuffix),
		ProxyPort:     uintOrDefault(e.cfg.ProxyPort, defaultSkynetWebProxyPort),
		ProxyAddr:     e.cfg.ProxyAddr,
		UpstreamSOCKS: e.cfg.UpstreamSOCKS,
		Stats:         e.stats,
	}

	// Self-loopback: serve requests for THIS visor's own PK in-process
	// (default on) instead of dialing a skynet route back to self. Set
	// self_loopback:false to exercise the real self-route (valid for
	// skynet). Aliases map friendly labels (default "skywire") to a PK.
	if e.selfDial != nil && (e.cfg.SelfLoopback == nil || *e.cfg.SelfLoopback) {
		cfg.LocalPK = e.localPK
		cfg.SelfLoopback = true
		cfg.SelfDial = e.selfDial
		// Self-loopback-authenticated (default on): see EmbeddedDmsgWeb.serve.
		if e.selfDialAs != nil && (e.cfg.SelfLoopbackAuthenticated == nil || *e.cfg.SelfLoopbackAuthenticated) {
			localPK := e.localPK
			cfg.SelfDial = func(port uint16) (net.Conn, error) {
				return e.selfDialAs(port, localPK)
			}
		}
	}
	cfg.Aliases = resolverAliasMap(e.cfg.Alias, e.localPK)
	// Reserved in-process status hosts (http://status.skynet/ etc.).
	cfg.StatusProvider = e.statusProvider

	// Optional TLS MITM mode. Loading the CA can fail (file
	// missing, permissions, malformed) — those failures are not
	// fatal: the resolver continues without MITM and logs the
	// reason. This keeps the visor running even if the visitor's
	// CA install is partial.
	if e.cfg.TLSMITM {
		if err := wireSkynetTLSMITM(&cfg, e.cfg, e.log); err != nil {
			e.log.WithError(err).Warn("skynetweb TLS MITM disabled")
		}
	}

	e.log.WithField("socks_port", cfg.ProxyPort).
		WithField("domain", cfg.DomainSuffix).
		WithField("tls_mitm", cfg.TLSMITM).
		Info("Serving skynetweb resolver")

	// Pass a pointer-to-pointer so the dialer can dereference the mux
	// at *dial* time, not serve time. The mux is wired up by
	// initSkywireForwardConn, which can finish after this serve loop
	// starts (runtime RPC toggle race) — capturing once would lock the
	// dialer into the route-based fallback even after the mux exists.
	dialer := &routerSkynetDialer{
		router:       e.router,
		localPK:      e.localPK,
		log:          e.log,
		tpM:          e.tpM,
		skynetMuxPtr: e.skynetMux,
		routeTimeout: time.Duration(e.cfg.RouteTimeout),
	}
	// Hold + reuse multihop routes across the resolving proxy's short-lived SOCKS5
	// connections: without this, every connection dials a fresh route and closes it,
	// so multihop routes die and re-set-up each time (the "only works for direct
	// routes" limitation). The pool keeps one route group per destination warm and
	// yamux-muxes over it; idle groups reclaim after ~10 min. See pkg/skyroute.
	dialer.pool = skyroute.New(skyroute.DefaultIdleTTL, e.log)
	go func() {
		<-ctx.Done()
		_ = dialer.pool.Close() //nolint:errcheck
	}()
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
	tpM          *transport.Manager     // for direct transport dialing
	skynetMuxPtr **transport.VStreamMux // shared with forwarding server; deref at dial time
	routeTimeout time.Duration          // 0 = use DefaultRouteKeepAlive
	nextPort     uint32                 // ephemeral port counter for route fallback
	pool         *skyroute.Pool         // holds+reuses multihop routes per dest (nil = disabled)
}

// dialRouteForPool dials the destination's mux forwarding port over a route, for
// skyroute.Pool. Mirrors the route-based path in DialSkynet (fresh ephemeral local
// port, optional keep-alive override) but targets muxPort so the pool can yamux the
// route group.
func (d *routerSkynetDialer) dialRouteForPool(ctx context.Context, dest cipher.PubKey, muxPort uint16) (net.Conn, error) {
	var opts *router.DialOptions
	if d.routeTimeout > 0 {
		opts = router.DefaultDialOptions()
		opts.KeepAlive = d.routeTimeout
	}
	lPort := routing.Port(atomic.AddUint32(&d.nextPort, 1)) //nolint:gosec
	return d.router.DialRoutes(ctx, dest, lPort, routing.Port(muxPort), opts)
}

func (d *routerSkynetDialer) DialSkynet(ctx context.Context, remote cipher.PubKey, port uint16, route []skynetweb.RouteLabel) (net.Conn, error) {
	// Explicit source-route from the hostname (<hop>.<dest>.skynet): build the
	// exact forward+reverse hops and dial them, bypassing the direct-transport
	// fast path and the route-finder. Single-path only (no mux).
	if len(route) > 0 {
		return d.dialSourceRoute(ctx, remote, port, route)
	}

	// Try direct transport first — no route setup needed, no RSN dependency.
	// Uses the shared VStreamMux (same instance as the forwarding server).
	// Dereference at dial time so we pick up a mux that finished
	// initializing after this dialer was constructed.
	var mux *transport.VStreamMux
	if d.skynetMuxPtr != nil {
		mux = *d.skynetMuxPtr
	}
	if mux != nil {
		stream, err := mux.Dial(remote, "")
		if err == nil {
			d.log.WithField("remote", remote.String()).
				WithField("port", port).
				Debug("Skynet: using direct transport (no route)")
			conn := &vstreamConn{VStream: stream}
			if err := skynetweb.PerformHandshake(conn, port); err != nil {
				// A handshake failure on a LIVE direct transport (the server
				// returned an error / the port is closed) is a real error:
				// return it rather than silently falling through to a multihop
				// route, which masks the error and burns route-setup budget.
				// Only a transport-DIAL failure (mux.Dial err) falls through.
				conn.Close() //nolint:errcheck,gosec
				return nil, err
			}
			return conn, nil
		}
		// No direct transport (dial failed) — try a 1-hop relay (a peer we
		// share a direct transport with that also reaches the destination)
		// before falling back to a multihop route. PK-addressed forwarding,
		// no route setup.
		if conn, rErr := d.dialViaRelay(ctx, remote, port); rErr == nil {
			return conn, nil
		}
		// No usable relay — fall through to route-based dial.
	}

	// Reuse a HELD, yamux-muxed multihop route via the pool: dial the mux forwarding
	// port ONCE, then open a stream per connection. The held route group's keepalive
	// keeps every hop warm, so reconnects don't re-run route setup — the fix for
	// multihop skynet routes dying under the resolving proxy. Falls back to a fresh
	// 1:1 route dial only against older visors that don't serve the mux port.
	if d.pool != nil {
		// Key the pool on the destination PK: the route-finder picks the path, so all
		// plain <pk>.skynet fetches to this dest share one warm route group.
		dest := remote
		stream, err := d.pool.OpenStream(ctx, dest.Hex(), func(ctx context.Context, muxPort uint16) (net.Conn, error) {
			return d.dialRouteForPool(ctx, dest, muxPort)
		})
		if err == nil {
			if hErr := skynetweb.PerformHandshake(stream, port); hErr != nil {
				_ = stream.Close() //nolint:errcheck,gosec
				return nil, hErr
			}
			return stream, nil
		}
		if !errors.Is(err, skyroute.ErrNoMux) {
			// A real route-setup failure — the 1:1 path would fail identically, so
			// surface it rather than masking it behind a second route attempt.
			return nil, err
		}
		d.log.WithField("remote", remote.String()).Debug("Skynet: mux forwarding unavailable; using 1:1 route")
	}

	var opts *router.DialOptions
	if d.routeTimeout > 0 {
		opts = router.DefaultDialOptions()
		opts.KeepAlive = d.routeTimeout
	}
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

// dialSourceRoute dials an explicit forward+reverse hop-set parsed from the
// hostname (route-finder bypassed). It first tries a HELD, yamux-muxed route via
// the pool — keyed on destination+hops so it doesn't collide with the route-finder
// path's per-dest group — so an explicit multihop source route stays warm across
// short SOCKS5 connections instead of being re-set-up each time (the same decay the
// plain path already fixed). It falls back to a fresh 1:1 dial against older far
// ends that don't serve the mux forwarding port.
func (d *routerSkynetDialer) dialSourceRoute(ctx context.Context, dest cipher.PubKey, port uint16, route []skynetweb.RouteLabel) (net.Conn, error) {
	if d.pool != nil {
		key := sourceRouteKey(dest, route)
		stream, err := d.pool.OpenStream(ctx, key, func(ctx context.Context, muxPort uint16) (net.Conn, error) {
			return d.dialSourceRouteConn(ctx, dest, route, muxPort)
		})
		if err == nil {
			if hErr := skynetweb.PerformHandshake(stream, port); hErr != nil {
				_ = stream.Close() //nolint:errcheck,gosec
				return nil, hErr
			}
			return stream, nil
		}
		if !errors.Is(err, skyroute.ErrNoMux) {
			// A real route-setup failure — the 1:1 path would fail identically.
			return nil, err
		}
		d.log.WithField("dest", dest.String()).Debug("Skynet: source-route mux forwarding unavailable; using 1:1 route")
	}

	// 1:1 fallback (far ends without the mux forwarding port): dial the explicit
	// hops to the single-connection forwarding port.
	conn, err := d.dialSourceRouteConn(ctx, dest, route, skyenv.SkyForwardingServerPort)
	if err != nil {
		return nil, err
	}
	if err := skynetweb.PerformHandshake(conn, port); err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return conn, nil
}

// dialSourceRouteConn builds explicit forward+reverse hops from the parsed route and
// dials them to fwdPort with DialRoutes, returning the raw route-group conn with no
// skynet handshake (the pool yamux-wraps it first; the 1:1 fallback handshakes
// itself). Single-path; the reverse path mirrors the forward path (transport IDs
// are symmetric, so the same TpIDs are reused with edges swapped).
func (d *routerSkynetDialer) dialSourceRouteConn(ctx context.Context, dest cipher.PubKey, route []skynetweb.RouteLabel, fwdPort uint16) (net.Conn, error) {
	fwd, rev, err := d.buildSourceRoute(ctx, dest, route)
	if err != nil {
		return nil, fmt.Errorf("skynet source-route to %s: %w", dest, err)
	}
	opts := router.DefaultDialOptions()
	if d.routeTimeout > 0 {
		opts.KeepAlive = d.routeTimeout
	}
	opts.ForwardHops = fwd
	opts.ReverseHops = rev
	lPort := routing.Port(atomic.AddUint32(&d.nextPort, 1)) //nolint:gosec
	return d.router.DialRoutes(ctx, dest, lPort, routing.Port(fwdPort), opts)
}

// sourceRouteKey derives a stable pool key for an explicit source route: the
// destination plus each hop label (PK or transport ID) in order. Distinct hop-sets
// to the same destination get independent warm route groups, and neither collides
// with the route-finder path (which keys on the bare destination hex).
func sourceRouteKey(dest cipher.PubKey, route []skynetweb.RouteLabel) string {
	var b strings.Builder
	b.WriteString(dest.Hex())
	for _, rl := range route {
		b.WriteByte('|')
		if rl.IsTpID {
			b.WriteString("t:")
			b.WriteString(rl.TpID.String())
		} else {
			b.WriteString("p:")
			b.WriteString(rl.PK.Hex())
		}
	}
	return b.String()
}

// buildSourceRoute turns the parsed route into forward+reverse routing hops. A
// TpID label resolves to its edges (and thus the next visor); a PK label names
// the next visor, whose transport is found or — for this visor's own hop —
// created. A final hop to dest is appended unless the chain already ends there.
func (d *routerSkynetDialer) buildSourceRoute(ctx context.Context, dest cipher.PubKey, route []skynetweb.RouteLabel) (fwd, rev []routing.Hop, err error) {
	current := d.localPK
	for _, rl := range route {
		var next cipher.PubKey
		var tpID uuid.UUID
		if rl.IsTpID {
			tpID = rl.TpID
			a, b, rerr := d.resolveTpEdges(ctx, tpID)
			if rerr != nil {
				return nil, nil, rerr
			}
			switch current {
			case a:
				next = b
			case b:
				next = a
			default:
				return nil, nil, fmt.Errorf("transport %s does not connect from %s", tpID, current)
			}
		} else {
			next = rl.PK
			id, terr := d.findOrCreateTransport(ctx, current, next)
			if terr != nil {
				return nil, nil, terr
			}
			tpID = id
		}
		fwd = append(fwd, routing.Hop{TpID: tpID, From: current, To: next})
		current = next
	}
	if current != dest {
		id, terr := d.findOrCreateTransport(ctx, current, dest)
		if terr != nil {
			return nil, nil, terr
		}
		fwd = append(fwd, routing.Hop{TpID: id, From: current, To: dest})
	}
	rev = make([]routing.Hop, len(fwd))
	for i, h := range fwd {
		rev[len(fwd)-1-i] = routing.Hop{TpID: h.TpID, From: h.To, To: h.From}
	}
	return fwd, rev, nil
}

// resolveTpEdges returns the two visor PKs a transport connects — from the local
// transport manager (this visor's own transports) or the transport discovery
// (downstream hops).
func (d *routerSkynetDialer) resolveTpEdges(ctx context.Context, tpID uuid.UUID) (cipher.PubKey, cipher.PubKey, error) {
	if mt, e := d.tpM.GetTransportByID(tpID); e == nil {
		return d.localPK, mt.Remote(), nil
	}
	entry, e := d.tpM.Conf.DiscoveryClient.GetTransportByID(ctx, tpID)
	if e != nil {
		return cipher.PubKey{}, cipher.PubKey{}, fmt.Errorf("resolve transport %s: %w", tpID, e)
	}
	return entry.Edges[0], entry.Edges[1], nil
}

// findOrCreateTransport returns the ID of a transport connecting from→to. When
// `from` is this visor it reuses an existing stcpr/sudph transport or creates
// one; for an intermediate pair it can only find an existing transport via the
// discovery (no remote-create yet). Skynet routes never use dmsg transports.
func (d *routerSkynetDialer) findOrCreateTransport(ctx context.Context, from, to cipher.PubKey) (uuid.UUID, error) {
	if from == d.localPK {
		for _, t := range []types.Type{types.STCPR, types.SUDPH} {
			if mt, e := d.tpM.GetTransport(to, t); e == nil {
				return mt.Entry.ID, nil
			}
		}
		var lastErr error
		for _, t := range []types.Type{types.STCPR, types.SUDPH} {
			mt, e := d.tpM.SaveTransport(ctx, to, t, transport.LabelUser)
			if e == nil {
				return mt.Entry.ID, nil
			}
			lastErr = e
		}
		return uuid.UUID{}, fmt.Errorf("no transport to %s and could not create one: %w", to, lastErr)
	}
	entries, e := d.tpM.Conf.DiscoveryClient.GetTransportsByEdge(ctx, from)
	if e != nil {
		return uuid.UUID{}, fmt.Errorf("look up transports of %s: %w", from, e)
	}
	for _, en := range entries {
		if en.Edges[0] == to || en.Edges[1] == to {
			return en.ID, nil
		}
	}
	return uuid.UUID{}, fmt.Errorf("no known transport between %s and %s (remote-create not supported yet)", from, to)
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
	runtime := newEmbeddedSkynetWeb(ctx, v.router, v.tpM, &v.skynetFwdMux, v.conf.PK, v.services.SelfDial, v.services.SelfDialAs, v.conf.SkynetWeb, log)
	runtime.statusProvider = v.proxyStatusProvider()
	v.initLock.Lock()
	v.embeddedSkynetWeb = runtime
	v.initLock.Unlock()

	// Register as an Internal app (RFC #2775 Phase 3.2). See
	// buildDmsgWebAppFunc in embedded_dmsgweb.go for the matching
	// pattern; both proxies use RestartPolicy=Never (operator
	// toggles are intentional, not crash-recovery candidates).
	launcher.RegisterApp(skyenvSkynetWebApp, buildSkynetWebAppFunc(runtime, log))
	return nil
}

// skyenvSkynetWebApp is the launcher-registered name for the
// embedded skynetweb SOCKS5 proxy.
const skyenvSkynetWebApp = "skynetweb"

// buildSkynetWebAppFunc — same shape as buildDmsgWebAppFunc.
func buildSkynetWebAppFunc(rt *EmbeddedSkynetWeb, log *logging.Logger) appcommon.AppFunc {
	return func(ctx context.Context, _ []string) error {
		appCl := app.NewClient(nil)
		defer appCl.Close()
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)
		if err := rt.Start(); err != nil {
			log.WithError(err).Warn("skynetweb Start failed")
			appCl.SetErrorOrLog(err)
			return err
		}
		appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
		<-ctx.Done()
		if err := rt.Stop(); err != nil {
			log.WithError(err).Warn("skynetweb Stop failed")
		}
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
		return nil
	}
}
