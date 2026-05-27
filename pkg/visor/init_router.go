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
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
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

			dmsgFallback := func() error {
				return retrier.Do(ctx, func() error {
					_, err := tm.SaveTransport(ctx, rPK, types.DMSG, transport.LabelAutomatic)
					if err != nil {
						log.Debugf("Establishing automatic DMSG transport failed.")
					}
					return err
				})
			}
			// check visor's AR transport
			if allTransports == nil && !v.conf.Transport.PublicAutoconnect {
				// skips if there's no AR transports
				log.Warn("empty AR transports")
				return dmsgFallback()
			}
			transports, ok := allTransports[rPK]
			if !ok {
				log.WithField("pk", rPK.String()).Warn("pk not found in the transports")
				// check if automatic transport is available, if it does,
				// continue with route creation
				return dmsgFallback()
			}
			// try to establish direct connection to rPK (single hop) using SUDPH or STCPR
			trySTCPR := false
			trySUDPH := false

			for _, trans := range transports {
				nType := types.Type(trans)
				if nType == types.STCPR {
					trySTCPR = true
					continue
				}

				// Wait until stun client is ready
				<-v.stun.ready

				// skip SUDPH if NAT type prevents it (symmetric NAT, firewall, or STUN failure)
				if nType == types.SUDPH {
					switch v.stun.client.NATType {
					case stun.NATSymmetric, stun.NATSymmetricUDPFirewall,
						stun.NATError, stun.NATUnknown, stun.NATBlocked:
						continue
					}
				}
				trySUDPH = true
			}

			// trying to establish direct connection to rPK using STCPR
			if trySTCPR {
				err := retrier.Do(ctx, func() error {
					_, err := tm.SaveTransport(ctx, rPK, types.STCPR, transport.LabelAutomatic)
					return err
				})
				if err == nil {
					return nil
				}
				log.Debugf("Establishing automatic STCPR transport failed.")
			}
			// trying to establish direct connection to rPK using SUDPH
			if trySUDPH {
				err := retrier.Do(ctx, func() error {
					_, err := tm.SaveTransport(ctx, rPK, types.SUDPH, transport.LabelAutomatic)
					return err
				})
				if err == nil {
					return nil
				}
				log.Debugf("Establishing automatic SUDPH transport failed.")
			}

			return dmsgFallback()
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
	rgDialer := router.NewSetupNodeDialerFull(embeddedRSN, relayCache, v.tpM)

	if order := types.ParsePreferenceOrder(v.conf.Routing.TransportPreference); len(order) > 0 {
		types.SetPreferenceOrder(order)
		log.WithField("order", v.conf.Routing.TransportPreference).Info("Applied configured transport preference order")
	}

	rConf := router.Config{
		Logger:             logger,
		MasterLogger:       v.MasterLogger(),
		PubKey:             v.conf.PK,
		SecKey:             v.conf.SK,
		TransportManager:   v.tpM,
		RouteFinder:        rfClient,
		RouteGroupDialer:   rgDialer,
		SetupNodes:         v.conf.EffectiveRouteSetupNodes(),
		RulesGCInterval:    0, // 0 = DefaultRulesGCInterval (10s)
		MinHops:            v.conf.Routing.MinHops,
		AwaitSetupListener: v.awaitSetupListener,
		// Tag rg-scoped log entries with the originating app's name so
		// 'cli proxy start --verbose' can scope to a session. Resolved
		// lazily via the proc manager — the manager owns the port↔app
		// mapping and is the only authoritative source.
		AppLookup: func(p routing.Port) (string, bool) {
			if v.procM == nil {
				return "", false
			}
			return v.procM.AppByPort(p)
		},
	}

	routeSetupHooks := getRouteSetupHooks(ctx, v, log)

	r, err := router.New(v.dmsgC, &rConf, routeSetupHooks)
	if err != nil {
		err := fmt.Errorf("failed to create router: %w", err)
		return err
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	if err := r.Serve(serveCtx); err != nil {
		cancel()
		return err
	}

	v.pushCloseStack("router.serve", func() error {
		cancel()
		return r.Close()
	})

	v.initLock.Lock()
	v.rfClient = rfClient
	v.router = r
	v.initLock.Unlock()

	// Latency is primarily measured at the transport level via transport-level
	// ping/pong frames (no RSN needed). For backward compatibility with old
	// visors that don't support transport ping, fall back to RSN-based measurement.
	v.tpM.SetLatencyFallback(func(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) float64 {
		latencyMs, err := r.MeasureTransportLatency(ctx, remote, tpID)
		if err != nil {
			logger.WithError(err).Debugf("RSN latency fallback failed for transport %s", tpID)
			return 0
		}
		logger.Debugf("Transport %s latency (RSN fallback): %.2f ms", tpID, latencyMs)
		return latencyMs
	})

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
	upgradeDmsgDiscToDmsgfirst(routeSetupDmsgC, primary, v.conf.Dmsg, log)

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
