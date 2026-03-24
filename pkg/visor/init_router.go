// init_router.go contains router initialization logic.
package visor

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/google/uuid"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// getRouteSetupHooks aka autotransport
// TODO: fix gocyclo error.
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
				<-v.stunReady

				// skip SUDPH if NAT type prevents it (symmetric NAT, firewall, or STUN failure)
				if nType == types.SUDPH {
					switch v.stunClient.NATType {
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

	httpC, err := getHTTPClient(ctx, v, conf.RouteFinder)
	if err != nil {
		return err
	}

	rfClient := rfclient.NewHTTP(conf.RouteFinder, time.Duration(conf.RouteFinderTimeout), httpC, v.MasterLogger())
	logger := v.MasterLogger().PackageLogger("router")

	// Use embedded route setup-node if available, otherwise use remote setup-nodes
	var rgDialer router.RouteGroupDialer
	if v.embeddedRouteSetup != nil {
		log.WithField("route_setup_pk", v.embeddedRouteSetup.PK()).Info("Using embedded route setup-node for routing")
		rgDialer = router.NewSetupNodeDialerWithEmbedded(v.embeddedRouteSetup)
	} else {
		rgDialer = router.NewSetupNodeDialer()
	}

	rConf := router.Config{
		Logger:           logger,
		MasterLogger:     v.MasterLogger(),
		PubKey:           v.conf.PK,
		SecKey:           v.conf.SK,
		TransportManager: v.tpM,
		RouteFinder:      rfClient,
		RouteGroupDialer: rgDialer,
		SetupNodes:       conf.RouteSetupNodes,
		RulesGCInterval:  0, // TODO
		MinHops:          v.conf.Routing.MinHops,
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

	// Set up transport latency measurement callback
	// When a transport is created, measure its latency via a direct route ping
	v.tpM.SetOnTransportCreated(func(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) float64 {
		latencyMs, err := r.MeasureTransportLatency(ctx, remote, tpID)
		if err != nil {
			logger.WithError(err).Debugf("Failed to measure latency for transport %s", tpID)
			return 0
		}
		logger.Debugf("Measured latency for transport %s: %.2f ms", tpID, latencyMs)
		return latencyMs
	})

	return nil
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
	httpC := &http.Client{}
	routeSetupDisc := dmsgdisc.NewHTTP(v.conf.Dmsg.Discovery, httpC, v.MasterLogger().PackageLogger("embedded_route_setup:disc"))
	routeSetupDmsgC := dmsg.NewClient(routeSetupPK, *routeSetupSK, routeSetupDisc, dmsgConf)
	routeSetupDmsgC.SetLogger(v.MasterLogger().PackageLogger("embedded_route_setup:dmsg"))

	go routeSetupDmsgC.Serve(ctx)

	select {
	case <-routeSetupDmsgC.Ready():
		log.Info("Embedded route setup-node dmsg client connected")
	case <-ctx.Done():
		return fmt.Errorf("context canceled waiting for route setup-node dmsg client")
	}

	v.initLock.Lock()
	v.embeddedRouteSetup = &EmbeddedRouteSetup{
		dmsgC: routeSetupDmsgC,
		pk:    routeSetupPK,
		log:   log,
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

	// Get configured TPS and RSN nodes
	tpsNodes := v.conf.Transport.TransportSetupPKs
	rsnNodes := v.conf.Routing.RouteSetupNodes

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
