// Package visor pkg/visor/init_router.go c3-vis-core
// init_router.go contains router initialization logic.
package visor

import (
	"context"
	"fmt"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/policy"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

// getRouteSetupHooks aka autotransport
// nolint: gocyclo
//
//gocyclo:ignore
func getRouteSetupHooks(ctx context.Context, v *Visor, log *logging.Logger) []router.RouteSetupHook {
	retrier := netutil.NewRetrier(log, time.Second, time.Second*20, 3, 1.3)
	return []router.RouteSetupHook{
		func(rPK cipher.PubKey, tm *transport.Manager) error {
			establishedTransports, _ := v.Transports([]string{string(types.STCPR), string(types.SUDPH), string(types.DMSG)}, []cipher.PubKey{v.conf.PK}, false) //nolint:errcheck
			for _, transportSum := range establishedTransports {
				if transportSum.Remote.Hex() == rPK.Hex() {
					log.Debugf("Established transport exist. Type: %s", transportSum.Type)
					return nil
				}
			}

			allTransports, err := v.arClient.Transports(ctx)
			if err != nil {
				log.WithError(err).Warn("failed to fetch AR transport")
			}

			// After (or instead of) the AR/STUN-gated STCPR/SUDPH attempts below,
			// try the REMAINING creatable direct types (QUIC, STCP, WEBRTC, WS, WT)
			// in preference order before falling back to the DMSG relay —
			// EnsureBestTransport(skip STCPR,SUDPH), so we don't re-attempt the two
			// this hook already handles with AR/NAT gating. This keeps the data plane
			// peer-to-peer: a dmsg transport is created only if EVERY direct type
			// fails (EnsureBestTransport logs that at WARN as a red flag). Previously
			// this fell straight to DMSG, skipping webrtc/ws/wt/quic/stcp entirely and
			// over-using the relay on NAT'd visors.
			directFallback := func() error {
				return tm.EnsureBestTransport(ctx, rPK, types.STCPR, types.SUDPH)
			}
			// check visor's AR transport
			if allTransports == nil && !v.conf.Transport.PublicAutoconnect {
				// skips if there's no AR transports
				log.Warn("empty AR transports")
				return directFallback()
			}
			transports, ok := allTransports[rPK]
			if !ok {
				log.WithField("pk", rPK.String()).Warn("pk not found in the transports")
				// check if automatic transport is available, if it does,
				// continue with route creation
				return directFallback()
			}
			// What the peer advertises to the address resolver. dmsg needs no
			// advertisement — every visor is reachable over it.
			advertised := make(map[types.Type]bool, len(transports))
			for _, trans := range transports {
				advertised[types.Type(trans)] = true
			}

			// SUDPH additionally needs a ready STUN client and a NAT type that
			// can hole-punch. Evaluated LAZILY: the wait below costs up to 20 s,
			// and a preference order that reaches dmsg or stcpr first must not
			// pay it. (Bounded — an unbounded <-v.stun.ready blocks direct-
			// transport setup for this peer forever when STUN never becomes
			// ready, wedging automatic transport establishment. Same wedge class
			// as the AR/service-discovery paths.)
			sudphUsable := func() (bool, error) {
				stunReady := false
				select {
				case <-v.stun.ready:
					stunReady = true
				case <-time.After(20 * time.Second):
					log.Warn("STUN not ready within 20s; skipping SUDPH for this peer")
				case <-ctx.Done():
					return false, ctx.Err()
				}
				// Without a ready STUN client we can't determine NAT type;
				// skip SUDPH rather than nil-deref v.stun.client.
				if !stunReady || v.stun.client == nil {
					return false, nil
				}
				switch v.stun.client.NATType {
				case stun.NATSymmetric, stun.NATSymmetricUDPFirewall,
					stun.NATError, stun.NATUnknown, stun.NATBlocked:
					return false, nil
				}
				return true, nil
			}

			// Try each type in the configured preference order
			// (routing.transport_preference), stopping at the first one that
			// establishes: the operator's primary transport, then the others.
			// Unset preference = the built-in default = stcpr, then sudph, then
			// dmsg — the order this was hardcoded to. A phone that pins dmsg
			// first skips the STCPR/SUDPH dialing a NATed mobile link rarely
			// wins, and the ~20 s STUN wait with it.
			var lastErr error
			for _, nType := range types.PreferredOrder(types.STCPR, types.SUDPH, types.DMSG) {
				switch nType {
				case types.STCPR:
					if !advertised[types.STCPR] {
						continue
					}
				case types.SUDPH:
					if !advertised[types.SUDPH] {
						continue
					}
					usable, err := sudphUsable()
					if err != nil {
						return err
					}
					if !usable {
						continue
					}
				}
				err := retrier.Do(ctx, func() error {
					_, err := tm.SaveTransport(ctx, rPK, nType, transport.LabelAutomatic)
					return err
				})
				if err == nil {
					return nil
				}
				log.Debugf("Establishing automatic %s transport failed.", nType)
				lastErr = err
			}

			return lastErr
		},
	}
}

func initRouter(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Routing

	// Resolve route finder URL: prefer HTTP, fall back to dmsghttp.
	rfURL := conf.RouteFinder
	if rfURL == "" && conf.RouteFinderDmsg != "" {
		rfURL = conf.RouteFinderDmsg
	}
	httpC, err := getHTTPClient(ctx, v, rfURL)
	if err != nil {
		return err
	}

	rfClient := rfclient.NewHTTP(rfURL, time.Duration(conf.RouteFinderTimeout), httpC, v.MasterLogger())
	logger := v.MasterLogger().PackageLogger("router")

	// Set up route group dialer with all available capabilities:
	// embedded RSN, transport relay, and DMSG fallback.
	var embeddedRSN router.EmbeddedSetupNode
	if v.embeddedRouteSetup != nil {
		log.WithField("route_setup_pk", v.embeddedRouteSetup.PK()).Info("Using embedded route setup-node for routing")
		embeddedRSN = v.embeddedRouteSetup
	}

	relayCache := router.NewRSNRelayCache(logger)
	// Legacy route setup (RSN dials each hop directly over dmsg) is the default
	// for now; the source-driven cascade is opt-in until its multihop data-plane
	// bug is fixed. forceLegacy is therefore the inverse of the opt-in flag.
	forceLegacy := !v.conf.Routing.EnableCascadeRouteSetup
	if forceLegacy {
		log.Info("Route setup using legacy direct-dial path (source-driven cascade disabled; set routing.enable_cascade_route_setup=true to opt in)")
	}
	rgDialer := router.NewSetupNodeDialerFull(embeddedRSN, relayCache, v.tpM, forceLegacy)

	if order := types.ParsePreferenceOrder(v.conf.Routing.TransportPreference); len(order) > 0 {
		types.SetPreferenceOrder(order)
		log.WithField("order", v.conf.Routing.TransportPreference).Info("Applied configured transport preference order")
	}

	// Tag rg-scoped log entries with the originating app's name so
	// 'cli proxy start --verbose' can scope to a session. Resolved lazily via
	// the proc manager — the manager owns the port↔app mapping.
	appLookup := func(p routing.Port) (string, bool) {
		if v.procM == nil {
			return "", false
		}
		return v.procM.AppByPort(p)
	}
	// dialHook is set by the routing-policy block below (nil = no policy = a
	// zero-cost dial path). Threaded into visorcore.BuildRouter as Config.DialHook.
	var dialHook router.DialHook

	// Operator-programmable routing policy (RFC #2882).
	// When conf.Routing.PolicyPerDial is set, build a Loader,
	// start its file-watcher if the path is file-backed, and
	// plug it into the router as a DialHook. Nil hook = no
	// integration cost when no policy is configured.
	// Operator-programmable routing policy. Two scopes:
	//   - visor-wide:  conf.Routing.PolicyPerDial
	//   - per-app:     AppConfig.RoutingPolicy (overrides the
	//                  visor-wide default for dials from that app)
	// A Hook is installed if either scope has a configured source.
	// Real Provider backed by the running visor's transport manager,
	// embedded geoip db, hypervisor list, and dmsgpty whitelist —
	// without this the script's geo.*, transports.*, and peers.*
	// stdlib calls would return zero values from policy.NopProvider.
	{
		policyLogger := func(format string, args ...interface{}) {
			log.Infof(format, args...)
		}
		var provider *visorPolicyProvider
		makeProvider := func() *visorPolicyProvider {
			if provider == nil {
				provider = newVisorPolicyProvider(v)
			}
			return provider
		}

		// Always install a Hook even when no policy is
		// configured at startup. The Hook with a nil default
		// engine and empty byApp map is zero-cost on the dial
		// path (loaderFor returns nil → BeforeDial / SelectRoute
		// / OnLegChange all early-return), and an installed
		// Hook is what makes runtime swaps via
		// SetAppRoutingPolicy possible without restart.
		hook := policy.NewHook(nil, policy.WithHookProvider(makeProvider()), policy.WithHookLogger(policyLogger))

		if pe, perr := loadPolicyEngine(v.conf.Routing.PolicyPerDial, makeProvider(), policyLogger); perr != nil {
			log.WithError(perr).Warn("Routing policy failed to load; visor will run without visor-wide policy.")
		} else if pe != nil {
			if werr := pe.watch(policyLogger); werr != nil {
				log.WithError(werr).Warn("Routing policy hot-reload watcher failed to start; policy still active but won't auto-reload.")
			}
			hook.SetDefault(pe.engine)
			v.pushCloseStack(pe.tag, pe.engine.Close)
			log.WithField("source", pe.engine.Source()).Info("Routing policy active.")
		}

		if v.conf.Launcher != nil {
			for _, app := range v.conf.Launcher.Apps {
				if app.RoutingPolicy == "" {
					continue
				}
				pe, perr := loadPolicyEngine(app.RoutingPolicy, makeProvider(), policyLogger)
				if perr != nil {
					log.WithError(perr).WithField("app", app.Name).Warn("Per-app routing policy failed to load; app will fall back to default policy.")
					continue
				}
				if pe == nil {
					continue
				}
				if werr := pe.watch(policyLogger); werr != nil {
					log.WithError(werr).WithField("app", app.Name).Warn("Per-app routing policy hot-reload watcher failed to start; policy still active but won't auto-reload.")
				}
				hook.RegisterApp(app.Name, pe.engine)
				appName := app.Name
				v.pushCloseStack(pe.tag+".app:"+appName, pe.engine.Close)
				log.WithField("app", appName).WithField("source", pe.engine.Source()).Info("Per-app routing policy active.")
			}
		}

		dialHook = hook
	}

	routeSetupHooks := getRouteSetupHooks(ctx, v, log)

	// Assemble + serve the router through the shared visorcore.BuildRouter so the
	// native visor and the wasm edge can't drift on the router.Config field mapping
	// or the Serve pattern. Native-only inputs (DialHook, AppLookup, SetupHooks) are
	// threaded through; the edge passes their zero values.
	serveCtx, cancel := context.WithCancel(context.Background())
	r, err := visorcore.BuildRouter(serveCtx, visorcore.RouterDeps{
		DmsgC:              v.dmsgC,
		PubKey:             v.conf.PK,
		SecKey:             v.conf.SK,
		TransportManager:   v.tpM,
		RouteFinder:        rfClient,
		RouteGroupDialer:   rgDialer,
		SetupNodes:         v.conf.EffectiveRouteSetupNodes(),
		MinHops:            v.conf.Routing.MinHops,
		AwaitSetupListener: v.awaitSetupListener,
		Logger:             logger,
		MasterLogger:       v.MasterLogger(),
		AppLookup:          appLookup,
		DialHook:           dialHook,
		RulesGCInterval:    0, // 0 = DefaultRulesGCInterval (10s)
		SetupHooks:         routeSetupHooks,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create router: %w", err)
	}

	v.pushCloseStack("router.serve", func() error {
		cancel()
		return r.Close()
	})

	v.initLock.Lock()
	v.rfClient = rfClient
	v.router = r
	v.initLock.Unlock()

	// Set up route checker so that transport re-creation is blocked when the
	// existing transport is actively referenced by routing rules. This prevents
	// a remote visor (or TPS) from breaking an in-flight route by forcing a
	// transport teardown/recreation under our feet.
	v.tpM.SetRouteChecker(func(tpID uuid.UUID) bool {
		return r.Rules() != nil && routeTableHasTransport(r, tpID)
	})

	return nil
}

// routeTableHasTransport returns true if the router's routing table has any
// active (non-timed-out) Forward or Intermediary rule referencing tpID.
func routeTableHasTransport(r router.Router, tpID uuid.UUID) bool {
	for _, rule := range r.Rules() {
		t := rule.Type()
		if t == routing.RuleForward || t == routing.RuleIntermediary {
			if rule.NextTransportID() == tpID {
				return true
			}
		}
	}
	return false
}

func initEmbeddedRouteSetup(ctx context.Context, v *Visor, log *logging.Logger) error {
	routeSetupSK := v.conf.Routing.RouteSetupSK
	if routeSetupSK == nil || *routeSetupSK == (cipher.SecKey{}) {
		log.Debug("No embedded route setup-node configured (route_setup_sk empty), skipping")
		return nil
	}

	routeSetupPK, err := routeSetupSK.PubKey()
	if err != nil {
		return fmt.Errorf("invalid route_setup_sk: %w", err)
	}
	log.WithField("route_setup_pk", routeSetupPK).Info("Starting embedded Route Setup Node")

	// Create a separate dmsg client with the route setup-node identity.
	// Reuses the visor's dmsg discovery URL but with route setup-node keys.
	dmsgConf := &dmsg.Config{
		MinSessions: 0, // Connect to all servers for better connectivity
		Protocol:    v.conf.Dmsg.Protocol,
	}
	dmsgConf.ClientType = "route_setup"

	// Resolve discovery URL: prefer HTTP, fall back to dmsghttp.
	discURL := v.conf.Dmsg.Discovery
	if discURL == "" && v.conf.Dmsg.DiscoveryDmsg != "" {
		discURL = v.conf.Dmsg.DiscoveryDmsg
	}
	httpC, err := getHTTPClient(ctx, v, discURL)
	if err != nil {
		return fmt.Errorf("embedded route setup-node: %w", err)
	}
	routeSetupDisc := dmsgdisc.NewHTTP(discURL, httpC, v.MasterLogger().PackageLogger("embedded_route_setup:disc"))
	routeSetupDmsgC := dmsg.NewClient(routeSetupPK, *routeSetupSK, routeSetupDisc, dmsgConf)
	routeSetupDmsgC.SetLogger(v.MasterLogger().PackageLogger("embedded_route_setup:dmsg"))

	go routeSetupDmsgC.Serve(ctx)

	select {
	case <-routeSetupDmsgC.Ready():
		log.Info("Embedded route setup-node dmsg client connected")
	case <-ctx.Done():
		return fmt.Errorf("context canceled waiting for route setup-node dmsg client")
	}
	// Seed deployment service PKs into routeSetupDmsgC's entry cache
	// BEFORE swapping in dmsgfirst — same reason as the main dmsgC
	// path in initDmsg. Without the seed, dmsgfirst's primary
	// DialStream(dmsgdiscPK) cache-misses, recurses through the
	// disc client, and overflows the goroutine stack.
	v.seedDmsgServiceEntries(routeSetupDmsgC, log)
	// Same dmsgfirst upgrade as the main dmsgC: discovery refresh
	// prefers DMSG and only falls back to plain HTTP per-call. The
	// dmsg-primary path uses v.dmsgDC (direct-client-backed) so the
	// DialStream to dmsg-disc resolves locally — see the equivalent
	// wiring in initDmsg's upgrade for the full rationale.
	v.initLock.Lock()
	primary := v.dmsgDC
	v.initLock.Unlock()
	upgradeDmsgDiscToDmsgOnly(routeSetupDmsgC, primary, v.conf.Dmsg, log)

	v.initLock.Lock()
	v.embeddedRouteSetup = &EmbeddedRouteSetup{
		dmsgC: routeSetupDmsgC,
		pk:    routeSetupPK,
		log:   log,
		stats: setupmetrics.NewCollector(setupmetrics.CollectorConfig{}),
	}
	v.initLock.Unlock()

	// Start the route setup-node listener to accept incoming requests from other visors
	go func() {
		if err := v.embeddedRouteSetup.Serve(ctx); err != nil {
			log.WithError(err).Error("Embedded route setup-node listener failed")
		}
	}()

	v.pushCloseStack("embedded_route_setup", func() error {
		return routeSetupDmsgC.Close()
	})
	return nil
}

// initNodeHealth initializes the node health tracker for TPS and RSN nodes.
func initNodeHealth(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		log.Warn("Dmsg client not available, skipping node health tracking")
		return nil
	}

	// Get configured TPS and RSN nodes (deployment + user merged)
	tpsNodes := v.conf.EffectiveTransportSetupPKs()
	rsnNodes := v.conf.EffectiveRouteSetupNodes()

	if len(tpsNodes) == 0 && len(rsnNodes) == 0 {
		log.Info("No TPS or RSN nodes configured, skipping node health tracking")
		return nil
	}

	log.WithField("tps_count", len(tpsNodes)).
		WithField("rsn_count", len(rsnNodes)).
		Info("Initializing node health tracker")

	v.nodeHealthTracker = NewNodeHealthTracker(v.dmsgC, log)
	v.nodeHealthTracker.Start(ctx, tpsNodes, rsnNodes)

	return nil
}
