// Package router pkg/router/node.go
package router

import (
	"context"
	"fmt"
	"net/http"
	"net/rpc"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

var log = logging.MustGetLogger("setup_node")

// Node performs routes setup operations over messaging channel.
type Node struct {
	dmsgC   *dmsg.Client
	pool    *ClientPool     // reusable RPC connections to remote visors
	cascade *CascadeBuilder // nil = cascade disabled (DMSG-only mode)
}

// DmsgClient returns the setup node's DMSG client.
// This allows other components (e.g. DMSG HTTP health) to share the same
// client instead of creating a second one with the same PK, which causes
// "error 306 - no associated listener" when streams route to the wrong client.
func (sn *Node) DmsgClient() *dmsg.Client {
	return sn.dmsgC
}

// NewNode constructs a new SetupNode.
func NewNode(conf *SetupConfig) (*Node, error) {
	if lvl, err := logging.LevelFromString(conf.LogLevel); err == nil {
		logging.SetLevel(lvl)
	}
	masterLogger := logging.NewMasterLogger()
	packageLogger := masterLogger.PackageLogger("node:disc")

	type setupNodeKey struct{}
	ctx := context.WithValue(context.Background(), setupNodeKey{}, true)

	// Pick the dmsg-discovery URL and the HTTP client used to query it.
	// Mirrors the visor bootstrap (pkg/visor/init_dmsg.go) so the RSN
	// can talk to dmsg-discovery over DMSG when configured to.
	//
	//   - If conf.Dmsg.DiscoveryDmsg is set AND we have static seed
	//     servers (conf.Dmsg.Servers), bring up a direct dmsg client
	//     against the seeds, wrap it as a dmsghttp transport, and use
	//     that as the http.Client for the real disc.NewHTTP. The seed
	//     servers break the chicken-and-egg of "need DMSG to query
	//     dmsg-discovery via DMSG."
	//   - Otherwise: plain HTTP, same as before.
	discURL := conf.Dmsg.Discovery
	httpC := &http.Client{}
	if conf.Dmsg.DiscoveryDmsg != "" && len(conf.Dmsg.Servers) > 0 {
		seedKeys := append(cipher.PubKeys{conf.PK}, dmsgServicePKsFromConf(conf)...)
		entries := direct.GetAllEntries(seedKeys, conf.Dmsg.Servers)
		dClient := direct.NewClient(entries, masterLogger.PackageLogger("rsn:dmsg_http:direct_client"))

		dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx,
			masterLogger.PackageLogger("rsn:dmsg_http:dmsgDC"),
			conf.PK, conf.SK, dClient, dmsg.DefaultConfig())
		if err != nil {
			log.WithError(err).Warn("DMSG-HTTP bootstrap failed, falling back to plain HTTP for dmsg-discovery")
		} else {
			httpC = &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}
			discURL = conf.Dmsg.DiscoveryDmsg
			log.Info("Using DMSG-HTTP for dmsg-discovery")
			// closeDmsgDC will run when the setup node shuts down via
			// its context cancellation; we don't track this in a close
			// stack here because Node.Stop is not exposed and the
			// process exits when ctx is cancelled.
			_ = closeDmsgDC
		}
	} else if discURL == "" && conf.Dmsg.DiscoveryDmsg != "" {
		// DMSG URL set but no seed servers — try plain HTTP against
		// the dmsg URL anyway. dmsg URLs don't work over plain HTTP,
		// so this will fail at first request, but that's the same
		// failure mode as before this change.
		discURL = conf.Dmsg.DiscoveryDmsg
		log.Warn("DiscoveryDmsg set but no seed servers in conf.Dmsg.Servers; cannot bootstrap dmsg-http")
	}

	dmsgDisc := disc.NewHTTP(discURL, httpC, packageLogger)
	dmsgConf := &dmsg.Config{MinSessions: conf.Dmsg.SessionsCount}
	dmsgC := dmsg.NewClient(conf.PK, conf.SK, dmsgDisc, dmsgConf)
	go dmsgC.Serve(ctx)

	log.WithField("local_pk", conf.PK).WithField("dmsg_conf", conf.Dmsg).
		WithField("disc_url", discURL).
		Info("Connecting to the dmsg network.")
	<-dmsgC.Ready()
	log.Info("Connected!")

	dialer := WrapDmsgClient(dmsgC)
	node := &Node{
		dmsgC: dmsgC,
		pool:  NewClientPool(dialer, DefaultPoolTTL),
	}

	// Initialize cascade builder if cascade config is present.
	// The transport manager for the RSN is initialized separately
	// by the caller (cmd/setup-node) since it requires network
	// factory configuration that depends on the deployment environment.
	if conf.Cascade != nil {
		conf.Cascade.SetCascadeDefaults()
		log.Info("Cascade route setup enabled")
	}

	return node, nil
}

// dmsgServicePKsFromConf extracts the public keys embedded in the
// setup-node's dmsg:// service URLs. These are added to the static
// seed entries built from conf.Dmsg.Servers so the bootstrap dmsg
// client can resolve them without HTTP discovery — same trick the
// visor uses in dmsgServicePKs() (pkg/visor/init_dmsg.go).
func dmsgServicePKsFromConf(conf *SetupConfig) cipher.PubKeys {
	candidates := []string{conf.Dmsg.DiscoveryDmsg}
	// TPD URL: dmsg-aware via scheme; AR URL: same. The setup-node's
	// other discovery URLs are HTTP-only today, but adding their dmsg
	// counterparts here is harmless and forward-compatible.
	if t := conf.TransportDiscovery; strings.HasPrefix(t, "dmsg://") {
		candidates = append(candidates, t)
	}
	if conf.Transport != nil {
		if a := conf.Transport.AddressResolver; strings.HasPrefix(a, "dmsg://") {
			candidates = append(candidates, a)
		}
	}

	var pks cipher.PubKeys
	for _, raw := range candidates {
		if !strings.HasPrefix(raw, "dmsg://") {
			continue
		}
		var addr dmsg.Addr
		if err := addr.Set(raw[len("dmsg://"):]); err != nil {
			continue
		}
		pks = append(pks, addr.PK)
	}
	return pks
}

// InitCascade initializes the cascade builder with a transport manager.
// Must be called after the RSN's transport manager is set up by the caller.
// This separation exists because the transport manager requires deployment-
// specific configuration (STCPR listen address, AR client, etc.) that the
// RSN's core doesn't control.
func (sn *Node) InitCascade(conf *SetupConfig, tm *transport.Manager) {
	if conf.Cascade == nil || tm == nil {
		return
	}
	cb := NewCascadeBuilder(log, conf.PK, conf.SK, tm)
	cb.SetTimeouts(conf.Cascade.ReserveTimeout, conf.Cascade.InstallTimeout)

	// Register the cascade ACK handler on the transport manager so
	// the builder can receive ACKs from cascade messages it sends.
	tm.SetCascadeHandler(func(p routing.Packet, mt *transport.ManagedTransport) {
		if p.Type() == routing.CascadeAckPacket {
			cb.HandleAck(p, mt)
		}
		// CascadeSetupPacket on RSN transports would be unexpected
		// (RSN doesn't process setup messages from others), but log
		// for debugging.
		if p.Type() == routing.CascadeSetupPacket {
			log.Warn("Received unexpected CascadeSetupPacket on RSN transport")
		}
	})

	sn.cascade = cb
	log.WithField("reserve_timeout", conf.Cascade.ReserveTimeout).
		WithField("install_timeout", conf.Cascade.InstallTimeout).
		Info("Cascade builder initialized")
}

// Pool returns the setup node's connection pool.
func (sn *Node) Pool() *ClientPool {
	return sn.pool
}

// Close closes the connection pool and underlying dmsg client.
func (sn *Node) Close() error {
	if sn == nil {
		return nil
	}
	if sn.pool != nil {
		sn.pool.Close()
	}
	return sn.dmsgC.Close()
}

// maxConcurrentHandlers bounds how many route setup requests the
// setup-node processes simultaneously. Each handler dials ~2-6 remote
// visors, and each dial reserves one ephemeral porter port (16 384
// ports total). Without a cap, a burst of requests (e.g. after a
// network restart when all visors reconnect at once) can exhaust the
// ephemeral port space, causing a death spiral: new dials fail
// immediately with "ephemeral port space exhausted", but in-flight
// handlers hold their ports for up to handlerTimeout (70 s), so the
// port space stays full. 512 concurrent handlers × ~6 ports ≈ 3 072
// ports at peak, leaving ample headroom.
const maxConcurrentHandlers = 512

// Serve starts transport listening loop.
func (sn *Node) Serve(ctx context.Context, m setupmetrics.Metrics) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	const dmsgPort = skyenv.DmsgSetupPort
	const timeout = 30 * time.Second

	log.WithField("dmsg_port", dmsgPort).Info("Starting listener.")
	lis, err := sn.dmsgC.Listen(skyenv.DmsgSetupPort)
	if err != nil {
		return fmt.Errorf("failed to listen on dmsg port %d: %v", skyenv.DmsgSetupPort, lis)
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			log.WithError(err).Warn("Dmsg listener closed with non-nil error.")
		}
	}()

	// Porter watchdog: periodically sweep stale ephemeral port reservations
	// and log usage. The embedded RSN (pkg/visor/embedded_route_setup.go) runs
	// the same loop; without it here, the standalone setup-node container
	// leaks reservations until the 16K ephemeral range saturates and every
	// new dial fails with "ephemeral port space exhausted" (observed: ~13
	// ports/min in prod, full exhaustion in ~20h). Sweep age matches the
	// pool TTL — anything older than that should have been freed already.
	const porterMaxAge = 5 * time.Minute
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		var lastCount int
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				swept := sn.dmsgC.SweepStalePorterEntries(porterMaxAge)
				diag := sn.dmsgC.PorterDiag()
				delta := diag.Ephemeral - lastCount
				if diag.Ephemeral > 100 || delta > 0 || swept > 0 {
					log.WithField("ephemeral", diag.Ephemeral).
						WithField("delta_60s", delta).
						WithField("swept", swept).
						WithField("pool_size", sn.pool.Size()).
						Warn("Setup-node porter watchdog")
				}
				lastCount = diag.Ephemeral
			}
		}
	}()

	// handlerTimeout is the hard deadline for an entire RPC handler goroutine.
	// This covers the full lifecycle: dial to remote visors, reserve IDs,
	// broadcast rules, and return response. It must be longer than the
	// per-request timeout used in DialRouteGroup (which is `timeout`=30s)
	// to allow for orderly context cancellation before the connection is
	// forcibly closed.
	const handlerTimeout = 2*timeout + 10*time.Second // 70s

	// Semaphore to limit concurrent handler goroutines.
	sem := make(chan struct{}, maxConcurrentHandlers)

	// Start transport-level RPC accept loop if cascade is enabled.
	// Visors that have a direct "setup" transport (or relay through a neighbor)
	// send SetupRPCPacket virtual streams instead of DMSG.
	if sn.cascade != nil && sn.cascade.tm != nil {
		setupMux := transport.NewVStreamMux(sn.cascade.tm, routing.SetupRPCPacket, log)
		sn.cascade.tm.SetSetupRPCHandler(setupMux.HandlePacket)

		go func() {
			defer setupMux.Close() //nolint:errcheck,gosec
			for {
				stream, err := setupMux.Accept()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.WithError(err).Warn("SetupRPC vstream accept failed")
					return
				}

				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					stream.Close() //nolint:errcheck,gosec
					return
				}

				handlerCtx, handlerCancel := context.WithTimeout(ctx, handlerTimeout)
				gw := &SetupRPCGateway{
					Metrics: m,
					Ctx:     handlerCtx,
					Conn:    nil, // no raw conn for vstream handlers
					ReqPK:   stream.RemotePK(),
					Dialer:  WrapDmsgClient(sn.dmsgC),
					Pool:    sn.pool,
					Cascade: sn.cascade,
					Timeout: timeout,
				}
				rpcS := rpc.NewServer()
				if err := rpcS.Register(gw); err != nil {
					log.WithError(err).Error("Failed to register vstream RPC gateway")
					stream.Close()  //nolint:errcheck,gosec
					handlerCancel() //nolint:gosec
					<-sem
					continue
				}
				go func() {
					defer func() {
						handlerCancel()
						stream.Close() //nolint:errcheck,gosec
						<-sem
					}()
					rpcS.ServeConn(stream)
				}()
			}
		}()
		log.Info("SetupRPC virtual stream accept loop started (transport-level)")
	}

	log.WithField("dmsg_port", dmsgPort).
		WithField("max_concurrent", maxConcurrentHandlers).
		Info("Accepting dmsg streams.")
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.WithError(err).Warn("Failed to accept dmsg stream, continuing...")
			continue
		}

		// Acquire a handler slot. If all slots are busy, block until one
		// frees up or the context is canceled. This back-pressures the
		// accept loop, which is safe — the DMSG listener buffers pending
		// streams, and visors retry on timeout.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			conn.Close() //nolint:errcheck,gosec,gosec
			return nil
		}

		// Derive a per-handler context so that if the handler takes too long,
		// all downstream operations (MakeMap dials, RPC calls) are canceled.
		handlerCtx, handlerCancel := context.WithTimeout(ctx, handlerTimeout)

		gw := &SetupRPCGateway{
			Metrics: m,
			Ctx:     handlerCtx,
			Conn:    conn,
			ReqPK:   conn.RemoteAddr().(dmsg.Addr).PK,
			Dialer:  WrapDmsgClient(sn.dmsgC),
			Pool:    sn.pool,
			Cascade: sn.cascade,
			Timeout: timeout,
		}
		rpcS := rpc.NewServer()
		if err := rpcS.Register(gw); err != nil {
			log.WithError(err).Error("Failed to register RPC gateway")
			conn.Close()    //nolint:errcheck,gosec,gosec
			handlerCancel() //nolint:gosec
			<-sem           // release the slot
			continue
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Errorf("Panic in setup RPC handler: %v", r)
				}
				handlerCancel()
				conn.Close() //nolint:errcheck,gosec,gosec
				<-sem        // release the handler slot
			}()
			// Set a hard deadline on the connection itself as a backstop.
			// If context cancellation fails to propagate (e.g., ServeConn blocks
			// on a read), the OS will close the connection after this deadline.
			conn.SetDeadline(time.Now().Add(handlerTimeout)) //nolint:errcheck,gosec,gosec
			rpcS.ServeConn(conn)
		}()
	}
}

// ErrCircuitOpen is returned by CreateRouteGroup when the per-
// destination circuit breaker has short-circuited a setup attempt. It
// is distinct from a dial failure so callers and stats can tell the
// difference between "this destination is known bad, skipping" and
// "we tried and failed again".
var ErrCircuitOpen = fmt.Errorf("route setup: destination circuit breaker open")

// CreateRouteGroup creates a route group by communicating with routers used within the bidirectional route.
// The following steps are taken:
// * Check the validity of bi route input.
// * Consult the per-destination circuit breaker; fast-fail if open.
// * Route IDs are reserved from the routers.
// * Intermediary rules are broadcasted to the intermediary routers.
// * Edge rules are broadcasted to the responding router.
// * Edge rules is returned (to the initiating router).
func CreateRouteGroup(ctx context.Context, dialer network.Dialer, pool *ClientPool, cascade *CascadeBuilder, biRt routing.BidirectionalRoute, metrics setupmetrics.Metrics) (resp routing.EdgeRules, err error) {
	log := logging.MustGetLogger(fmt.Sprintf("request:%s->%s", biRt.Desc.SrcPK(), biRt.Desc.DstPK()))
	log.Info("Processing request.")
	// If the metrics implementation is a Collector, use the richer
	// RecordRouteContext so the resulting StatsSnapshot includes src /
	// dst / hop-count. Fall back to the legacy RecordRoute for the
	// Victoria Metrics / Empty implementations which don't track per-
	// request context.
	hopCount := len(biRt.Forward)
	collector, haveCollector := metrics.(*setupmetrics.Collector)
	if haveCollector {
		defer collector.RecordRouteContext(ctx, biRt.Desc.SrcPK(), biRt.Desc.DstPK(), hopCount)(&err)
	} else {
		defer metrics.RecordRoute()(&err)
	}

	// Consult the per-destination circuit breaker. If the breaker is
	// open we short-circuit the entire setup path — no dial work, no
	// session usage, no concurrent-worker slot held for ~10s. This
	// matters for destinations that have accumulated thousands of
	// consecutive id_reservation failures (a dead visor still in
	// discovery) because one breaker decision saves ~10s of work per
	// attempt.
	if haveCollector {
		if ok, reason := collector.AllowDestination(biRt.Desc.DstPK()); !ok {
			log.Debugf("circuit breaker: %s", reason)
			return routing.EdgeRules{}, fmt.Errorf("%w: %s", ErrCircuitOpen, reason)
		}
	}

	// Ensure bi routes input is valid.
	if err = biRt.Check(); err != nil {
		return routing.EdgeRules{}, err
	}

	// Try cascade path if a CascadeBuilder is available.
	if cascade != nil {
		cascadeResp, cascadeErr := createRouteGroupCascade(ctx, log, cascade, biRt)
		if cascadeErr == nil {
			return cascadeResp, nil
		}
		log.WithError(cascadeErr).Warn("Cascade route setup failed, falling back to DMSG")
	}

	// DMSG-based path (existing protocol).
	// Reserve route IDs from remote routers.
	rtIDR, err := ReserveRouteIDs(ctx, log, dialer, pool, biRt)
	if err != nil {
		return routing.EdgeRules{}, err
	}
	defer func() {
		if err != nil {
			// On failure, discard connections (they may be broken).
			log.Debug("Discarding route id reserver connections (error path).")
			rtIDR.Close() //nolint:errcheck,gosec,gosec
		} else if pool != nil {
			// On success, return connections to pool for reuse.
			log.Debug("Returning route id reserver connections to pool.")
			rtIDR.ReturnToPool(pool)
		} else {
			log.WithError(rtIDR.Close()).Debug("Closing route id reserver.")
		}
	}()

	// Generate forward and reverse routes.
	fwdRt, revRt := biRt.ForwardAndReverse()
	srcPK := biRt.Desc.Src()
	dstPK := biRt.Desc.Dst()

	// Generate routing rules (for edge and intermediary routers) that are to be sent.
	// Rules are grouped by rule type [FWD, REV, INTER].
	fwdRules, revRules, interRules, err := GenerateRules(rtIDR, []routing.Route{fwdRt, revRt})
	if err != nil {
		return routing.EdgeRules{}, err
	}
	initEdge := routing.EdgeRules{Desc: revRt.Desc, Forward: fwdRules[srcPK.String()][0], Reverse: revRules[srcPK.String()][0]}
	respEdge := routing.EdgeRules{Desc: fwdRt.Desc, Forward: fwdRules[dstPK.String()][0], Reverse: revRules[dstPK.String()][0]}

	log.Infof("Generated routing rules:\nInitiating edge: %v\nResponding edge: %v\nIntermediaries: %v",
		initEdge.String(), respEdge.String(), interRules.String())

	// Broadcast intermediary rules to intermediary routers.
	if err := BroadcastIntermediaryRules(ctx, log, rtIDR, interRules); err != nil {
		return routing.EdgeRules{}, err
	}

	// Broadcast rules to responding router.
	log.Debug("Broadcasting responding rules...")
	ok, err := rtIDR.Client(biRt.Desc.DstPK()).AddEdgeRules(ctx, respEdge)
	if err != nil || !ok {
		return routing.EdgeRules{}, fmt.Errorf("failed to broadcast rules to destination router: %v", err)
	}

	// Return rules to initiating router.
	return initEdge, nil
}

// ReserveRouteIDs dials to all routers and reserves required route IDs from them.
// The number of route IDs to be reserved per router, is extrapolated from the 'route' input.
func ReserveRouteIDs(ctx context.Context, log logrus.FieldLogger, dialer network.Dialer, pool *ClientPool, route routing.BidirectionalRoute) (idR IDReserver, err error) {
	log.Debug("Reserving route IDs...")
	defer func() {
		if err != nil {
			log.WithError(err).Warn("Failed to reserve route IDs.")
		}
	}()

	idR, err = NewIDReserver(ctx, dialer, pool, [][]routing.Hop{route.Forward, route.Reverse})
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate route id reserver: %w", err)
	}
	defer func() {
		if err != nil {
			log.WithError(idR.Close()).Warn("Closing router clients due to error.")
		}
	}()

	if err = idR.ReserveIDs(ctx); err != nil {
		return idR, fmt.Errorf("failed to reserve route ids: %w", err)
	}
	return idR, nil
}

// GenerateRules generates rules for given forward and reverse routes.
// The outputs are as follows:
// - maps that relate slices of forward, consume and intermediary routing rules to a given visor's public key.
// - an error (if any).
func GenerateRules(idR IDReserver, routes []routing.Route) (fwdRules, revRules, interRules RulesMap, err error) {
	fwdRules = make(RulesMap)
	revRules = make(RulesMap)
	interRules = make(RulesMap)

	for _, route := range routes {
		// 'firstRID' is the first visor's key routeID
		firstRID, ok := idR.PopID(route.Hops[0].From)
		if !ok {
			return nil, nil, nil, ErrNoKey
		}

		desc := route.Desc
		srcAddr := desc.Src()
		dstAddr := desc.Dst()
		srcPort := desc.SrcPort()
		dstPort := desc.DstPort()

		var rID = firstRID

		for i, hop := range route.Hops {
			nxtRID, ok := idR.PopID(hop.To)
			if !ok {
				return nil, nil, nil, ErrNoKey
			}

			var port routing.Port
			if desc.DstPK() == hop.From {
				port = dstPort
			}
			if desc.SrcPK() == hop.From {
				port = srcPort
			}
			addr := routing.Addr{
				PubKey: hop.From,
				Port:   port,
			}
			if i == 0 {
				rule := routing.ForwardRule(route.KeepAlive, rID, nxtRID, hop.TpID, srcAddr.PubKey, dstAddr.PubKey, srcPort, dstPort)
				fwdRules[addr.String()] = append(fwdRules[addr.String()], rule)
			} else {
				rule := routing.IntermediaryForwardRule(route.KeepAlive, rID, nxtRID, hop.TpID)
				interRules[addr.String()] = append(interRules[addr.String()], rule)
			}

			rID = nxtRID
		}

		rule := routing.ConsumeRule(route.KeepAlive, rID, srcAddr.PubKey, dstAddr.PubKey, srcPort, dstPort)
		revRules[dstAddr.String()] = append(revRules[dstAddr.String()], rule)
	}

	return fwdRules, revRules, interRules, nil
}

// BroadcastIntermediaryRules broadcasts routing rules to the intermediary routers.
func BroadcastIntermediaryRules(ctx context.Context, log logrus.FieldLogger, rtIDR IDReserver, interRules RulesMap) (err error) {
	log.WithField("intermediary_routers", len(interRules)).Debug("Broadcasting intermediary rules...")
	defer func() {
		if err != nil {
			log.WithError(err).Warn("Failed to broadcast intermediary rules.")
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(interRules))
	defer close(errCh)

	for addr, rules := range interRules {
		var pk cipher.PubKey
		stringPK := strings.Split(addr, ":")
		err = pk.Set(stringPK[0])
		if err != nil {
			return err
		}
		go func(pk cipher.PubKey, rules []routing.Rule) {
			_, err := rtIDR.Client(pk).AddIntermediaryRules(ctx, rules)
			if err != nil {
				cancel()
			}
			errCh <- err
		}(pk, rules)
	}

	return firstError(len(interRules), errCh)
}
