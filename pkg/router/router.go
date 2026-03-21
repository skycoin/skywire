// Package router implements package router for skywire visor.
package router

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/noise"

	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

//go:generate mockery --name Router --case underscore --inpackage

const (
	// DefaultRouteKeepAlive is the default expiration interval for routes
	DefaultRouteKeepAlive = 2 * time.Minute
	// DefaultRulesGCInterval is the default duration for garbage collection of routing rules.
	DefaultRulesGCInterval = 10 * time.Second
	acceptSize             = 1024

	handshakeAwaitTimeout = 30 * time.Second

	maxHops       = 1000
	retryDuration = 2 * time.Second
	retryInterval = 500 * time.Millisecond
)

var (
	// ErrUnknownPacketType is returned when packet type is unknown.
	ErrUnknownPacketType = errors.New("unknown packet type")

	// ErrRemoteEmptyPK occurs when the specified remote public key is empty.
	ErrRemoteEmptyPK = errors.New("empty remote public key")

	// ErrNoTransportFound is returned when not even one transport is found.
	ErrNoTransportFound = errors.New("no transport found")
)

// RouteSetupHook is an alias for a function that takes remote public key
// and a reference to transport manager in order to setup i.e:
// 1. If the remote is either available stcpr or sudph, establish the transport to the remote and then continue with the route creation process.
// 2. If neither of these direct transports is available, check if automatic transports are currently active. If they are continue with route creation.
// 3. If none of the first two checks was successful, establish a dmsg transport and then continue with route creation.
type RouteSetupHook func(cipher.PubKey, *transport.Manager) error

// Config configures Router.
type Config struct {
	Logger           *logging.Logger
	MasterLogger     *logging.MasterLogger
	PubKey           cipher.PubKey
	SecKey           cipher.SecKey
	TransportManager *transport.Manager
	RouteFinder      rfclient.Client
	RouteGroupDialer RouteGroupDialer
	SetupNodes       []cipher.PubKey
	RulesGCInterval  time.Duration
	MinHops          uint16
	MaxHops          uint16
}

// SetDefaults sets default values for certain empty values.
func (c *Config) SetDefaults() {
	if c.Logger == nil {
		c.Logger = logging.MustGetLogger("router")
	}

	if c.RouteGroupDialer == nil {
		c.RouteGroupDialer = NewSetupNodeDialer()
	}

	if c.RulesGCInterval <= 0 {
		c.RulesGCInterval = DefaultRulesGCInterval
	}

	if c.MaxHops == 0 {
		c.MaxHops = maxHops
	}
}

// DialOptions describes dial options.
type DialOptions struct {
	MinForwardRts       int
	MaxForwardRts       int
	MinConsumeRts       int
	MaxConsumeRts       int
	Retries             int
	UseExistingTpOnly   bool          // If true, only use routes through existing transports, don't create new ones
	TransportID         uuid.UUID     // If set, use this specific transport (skips route calculation for direct transports)
	ForwardHops         []routing.Hop // If set, use these hops for forward path (skips route calculation)
	ReverseHops         []routing.Hop // If set, use these hops for reverse path (skips route calculation)
	MuxRoutes           int           // Number of parallel routes to establish (0 or 1 = single route, >1 = mux)
	ExcludeTransportIDs []uuid.UUID   // Transport IDs to exclude from route calculation (for mux)
	ExcludeDMSG         bool          // Exclude DMSG transports (for mux — DMSG is a relay, not suitable for multiplexing)
}

// DefaultDialOptions returns default dial options.
// Used by default if nil is passed as options.
func DefaultDialOptions() *DialOptions {
	return &DialOptions{
		MinForwardRts: 1,
		MaxForwardRts: 1,
		MinConsumeRts: 1,
		MaxConsumeRts: 1,
		Retries:       3,
	}
}

// Router is responsible for creating and keeping track of routes.
// Internally, it uses the routing table, route finder client and setup client.
type Router interface {
	io.Closer

	// DialRoutes dials to a given visor of 'rPK'.
	// 'lPort'/'rPort' specifies the local/remote ports respectively.
	// A nil 'opts' input results in a value of '1' for all DialOptions fields.
	// A single call to DialRoutes should perform the following:
	// - Find routes via RouteFinder (in one call).
	// - Setup routes via SetupNode (in one call).
	// - Save to routing.Table and internal RouteGroup map.
	// - Return RouteGroup if successful.
	DialRoutes(ctx context.Context, rPK cipher.PubKey, lPort, rPort routing.Port, opts *DialOptions) (net.Conn, error)
	PingRoute(ctx context.Context, rPK cipher.PubKey, lPort, rPort routing.Port, opts *DialOptions) (net.Conn, error)

	// AcceptRoutes should block until we receive an AddRules packet from SetupNode
	// that contains ConsumeRule(s) or ForwardRule(s).
	// Then the following should happen:
	// - Save to routing.Table and internal RouteGroup map.
	// - Return the RoutingGroup.
	AcceptRoutes(context.Context) (net.Conn, error)
	SaveRoutingRules(rules ...routing.Rule) error
	ReserveKeys(n int) ([]routing.RouteID, error)
	IntroduceRules(rules routing.EdgeRules) error
	Serve(context.Context) error
	SetupIsTrusted(cipher.PubKey) bool
	SetMinHop(uint16)
	SetExistingTPOnly(bool)
	SetForceLocalRoutes(bool)
	GetLastRouteCalcTime() time.Duration

	// Routing table related methods
	RoutesCount() int
	Rules() []routing.Rule
	Rule(routing.RouteID) (routing.Rule, error)
	SaveRule(routing.Rule) error
	DelRules([]routing.RouteID)

	// MeasureTransportLatency measures the latency of a specific transport by
	// creating a temporary direct route over that transport, sending a ping,
	// and measuring the round-trip time. Returns latency in milliseconds.
	MeasureTransportLatency(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) (float64, error)
}

// Router implements visor.PacketRouter. It manages routing table by
// communicating with setup nodes, forward packets according to local
// rules and manages route groups for apps.
type router struct {
	mx                 sync.Mutex
	conf               *Config
	logger             *logging.Logger
	mLogger            *logging.MasterLogger
	sl                 *dmsg.Listener
	dmsgC              *dmsg.Client
	trustedVisors      map[cipher.PubKey]struct{}
	tm                 *transport.Manager
	rt                 routing.Table
	rgsNs              map[routing.RouteDescriptor]*NoiseRouteGroup // Noise-wrapped route groups to push incoming reads from transports.
	rgsRaw             map[routing.RouteDescriptor]*RouteGroup      // Not-yet-noise-wrapped route groups. when one of these gets wrapped, it gets removed from here
	rpcSrv             *rpc.Server
	accept             chan routing.EdgeRules
	done               chan struct{}
	once               sync.Once
	routeSetupHookMu   sync.Mutex
	routeSetupHooks    []RouteSetupHook // see RouteSetupHook description
	existingTpOnly     bool             // when true, don't create new transports for routing
	existingTpOnlyMu   sync.Mutex       // protects existingTpOnly
	forceLocalRoutes   bool             // when true, skip route finder and use local route calculation
	forceLocalRoutesMu sync.Mutex       // protects forceLocalRoutes
	lastRouteCalcTime  time.Duration    // last route calculation time (for local routes)
	lastRouteCalcMu    sync.Mutex       // protects lastRouteCalcTime
}

// New constructs a new Router.
func New(dmsgC *dmsg.Client, config *Config, routeSetupHooks []RouteSetupHook) (Router, error) {
	config.SetDefaults()

	sl, err := dmsgC.Listen(skyenv.DmsgAwaitSetupPort)
	if err != nil {
		return nil, err
	}

	trustedVisors := make(map[cipher.PubKey]struct{})
	for _, node := range config.SetupNodes {
		trustedVisors[node] = struct{}{}
	}

	if routeSetupHooks == nil {
		routeSetupHooks = []RouteSetupHook{}
	}

	r := &router{
		conf:            config,
		logger:          config.Logger,
		mLogger:         config.MasterLogger,
		tm:              config.TransportManager,
		rt:              routing.NewTable(config.Logger),
		sl:              sl,
		dmsgC:           dmsgC,
		rgsNs:           make(map[routing.RouteDescriptor]*NoiseRouteGroup),
		rgsRaw:          make(map[routing.RouteDescriptor]*RouteGroup),
		rpcSrv:          rpc.NewServer(),
		accept:          make(chan routing.EdgeRules, acceptSize),
		done:            make(chan struct{}),
		trustedVisors:   trustedVisors,
		routeSetupHooks: routeSetupHooks,
	}

	go r.rulesGCLoop()

	if err := r.rpcSrv.Register(NewRPCGateway(r, config.MasterLogger)); err != nil {
		return nil, fmt.Errorf("failed to register RPC server")
	}

	return r, nil
}

// RegisterSetupHooks takes variadic RouteSetupHook to add to router's setup functions
// currently not in use
func (r *router) RegisterSetupHooks(rshooks ...RouteSetupHook) {
	r.routeSetupHookMu.Lock()
	r.routeSetupHooks = append(r.routeSetupHooks, rshooks...)
	r.routeSetupHookMu.Unlock()
}

// DialRoutes dials to a given visor of 'rPK'.
// 'lPort'/'rPort' specifies the local/remote ports respectively.
// A nil 'opts' input results in a value of '1' for all DialOptions fields.
// A single call to DialRoutes should perform the following:
// - Find routes via RouteFinder (in one call).
// - Setup routes via SetupNode (in one call).
// - Save to routing.Table and internal RouteGroup map.
// - Return RouteGroup if successful.
func (r *router) DialRoutes(
	ctx context.Context,
	rPK cipher.PubKey,
	lPort, rPort routing.Port,
	opts *DialOptions,
) (net.Conn, error) {

	if rPK.Null() {
		err := ErrRemoteEmptyPK
		r.logger.WithError(err).Error("Failed to dial routes.")
		return nil, fmt.Errorf("failed to dial routes: %w", err)
	}

	if r.conf.MinHops == 0 {
		r.logger.Error("Routing disabled. (minhop=0)")
		return nil, fmt.Errorf("Routing disabled. (minhop=0)")
	}

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, lPort, rPort)

	// check if transport exist, then skip minhop value and consider it equal 0
	defaultMinHops := r.conf.MinHops
	if r.isTpdExist(rPK) {
		r.conf.MinHops = 1
	}

	// Check if existing transport only mode is set on the router
	r.existingTpOnlyMu.Lock()
	routerExistingTpOnly := r.existingTpOnly
	r.existingTpOnlyMu.Unlock()

	// Only run route setup hooks (which may create new transports) if UseExistingTpOnly is false
	// on both the router level and the dial options level
	useExistingOnly := routerExistingTpOnly || (opts != nil && opts.UseExistingTpOnly)
	if r.conf.MinHops == 1 && !useExistingOnly {
		r.routeSetupHookMu.Lock()
		if len(r.routeSetupHooks) != 0 {
			for _, rsf := range r.routeSetupHooks {
				if err := rsf(rPK, r.tm); err != nil {
					r.routeSetupHookMu.Unlock()
					return nil, err
				}
			}
		}
		r.routeSetupHookMu.Unlock()
	} else if useExistingOnly {
		r.logger.Debug("UseExistingTpOnly is set, skipping transport creation hooks")
	}

	// Retry route setup with fresh routes if it fails due to stale TPD data.
	// Route-finder may return routes with non-existent transports (TPD sync issues),
	// so we query for fresh routes on each retry instead of retrying the same bad route.
	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		forwardPath, reversePath, err := r.fetchBestRoutes(ctx, lPK, rPK, opts)
		if err != nil {
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Route finder failed (attempt %d/%d), retrying with fresh query...", attempt, maxRetries)
				continue
			}
			return nil, fmt.Errorf("route finder: %w", err)
		}

		req := routing.BidirectionalRoute{
			Desc:      forwardDesc,
			KeepAlive: DefaultRouteKeepAlive,
			Forward:   forwardPath,
			Reverse:   reversePath,
		}

		rules, connectedNode, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, req)
		if err != nil {
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Route setup failed (attempt %d/%d), retrying with fresh route...", attempt, maxRetries)
				continue
			}
			r.logger.WithError(err).Error("Error dialing route group")
			return nil, err
		}

		// Reorder setup nodes to prioritize the one that worked
		if !connectedNode.Null() {
			r.conf.SetupNodes = ReorderSetupNodes(r.conf.SetupNodes, connectedNode)
		}

		if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Saving routing rules failed (attempt %d/%d), retrying with fresh route...", attempt, maxRetries)
				continue
			}
			r.logger.WithError(err).Error("Error saving routing rules")
			return nil, err
		}

		nsConf := noise.Config{
			LocalPK:   r.conf.PubKey,
			LocalSK:   r.conf.SecKey,
			RemotePK:  rPK,
			Initiator: true,
		}

		nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf)
		if err != nil {
			// Clean up saved rules on failure
			r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
			// Check if context was canceled
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Check if this is a "no suitable transport" error (stale TPD data)
			if strings.Contains(err.Error(), "no suitable transport") || strings.Contains(err.Error(), "transport") {
				if attempt < maxRetries {
					r.logger.WithError(err).Warnf("Route handshake failed due to transport issue (attempt %d/%d), querying route-finder for fresh route...", attempt, maxRetries)
					continue
				}
			}
			return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
		}

		// Store the complete forward route hops for later retrieval
		nrg.SetForwardHops(forwardPath)

		nrg.rg.startOffServiceLoops()

		r.logger.Debugf("Created new routes to %s on port %d", rPK, lPort)

		// Attempt additional mux routes if requested and mux was negotiated
		muxCount := 1
		if opts != nil && opts.MuxRoutes > 1 {
			muxCount = opts.MuxRoutes
		}
		if muxCount > 1 && nrg.rg.mux != nil {
			// Collect transport IDs already in use
			excludeIDs := []uuid.UUID{rules.Forward.NextTransportID()}

			for i := 1; i < muxCount; i++ {
				muxOpts := &DialOptions{
					MinForwardRts:       1,
					MaxForwardRts:       1,
					MinConsumeRts:       1,
					MaxConsumeRts:       1,
					Retries:             1,
					ExcludeTransportIDs: excludeIDs,
					ExcludeDMSG:         true, // DMSG is a relay, not suitable for multiplexing
				}

				muxFwd, muxRev, err := r.fetchBestRoutes(ctx, lPK, rPK, muxOpts)
				if err != nil {
					r.logger.Debugf("Mux route %d/%d: no additional route found: %v", i+1, muxCount, err)
					break
				}

				muxReq := routing.BidirectionalRoute{
					Desc:      forwardDesc,
					KeepAlive: DefaultRouteKeepAlive,
					Forward:   muxFwd,
					Reverse:   muxRev,
				}

				muxRules, _, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, muxReq)
				if err != nil {
					r.logger.Debugf("Mux route %d/%d: setup failed: %v", i+1, muxCount, err)
					break
				}

				if err := r.appendRouteToGroup(nrg, muxRules); err != nil {
					r.logger.Debugf("Mux route %d/%d: append failed: %v", i+1, muxCount, err)
					break
				}

				excludeIDs = append(excludeIDs, muxRules.Forward.NextTransportID())
				r.logger.Infof("Mux route %d/%d established via transport %s", i+1, muxCount, muxRules.Forward.NextTransportID())
			}
		}

		// reset MinHops default value if changed before
		if defaultMinHops != 1 {
			r.conf.MinHops = defaultMinHops
		}

		return nrg, nil
	}

	// Should never reach here, but handle it gracefully
	return nil, fmt.Errorf("failed to establish route after %d attempts", maxRetries)
}

// setupPingRoute sets up a ping route with the given forward and reverse paths.
// This is the common setup logic used by PingRoute for both calculated and direct transport routes.
func (r *router) setupPingRoute(
	ctx context.Context,
	forwardDesc routing.RouteDescriptor,
	forwardPath, reversePath []routing.Hop,
	rPK cipher.PubKey,
	_ *DialOptions,
) (net.Conn, error) {
	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: DefaultRouteKeepAlive,
		Forward:   forwardPath,
		Reverse:   reversePath,
	}

	// Debug: log route details before sending to setup node
	r.logger.Debugf("setupPingRoute: Desc.SrcPK=%s, Desc.DstPK=%s", forwardDesc.SrcPK(), forwardDesc.DstPK())
	// Log all forward hops with their transport IDs
	for i, hop := range forwardPath {
		r.logger.Debugf("setupPingRoute: Forward[%d] TpID=%s From=%s To=%s", i, hop.TpID, hop.From, hop.To)
	}
	// Log all reverse hops with their transport IDs
	for i, hop := range reversePath {
		r.logger.Debugf("setupPingRoute: Reverse[%d] TpID=%s From=%s To=%s", i, hop.TpID, hop.From, hop.To)
	}

	rules, connectedNode, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, req)
	if err != nil {
		r.logger.WithError(err).Error("Error dialing ping route group")
		return nil, err
	}

	// Reorder setup nodes to prioritize the one that worked
	if !connectedNode.Null() {
		r.conf.SetupNodes = ReorderSetupNodes(r.conf.SetupNodes, connectedNode)
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		r.logger.WithError(err).Error("Error saving ping routing rules")
		return nil, err
	}

	nsConf := noise.Config{
		LocalPK:   r.conf.PubKey,
		LocalSK:   r.conf.SecKey,
		RemotePK:  rPK,
		Initiator: true,
	}

	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf)
	if err != nil {
		// Clean up saved rules if route group setup fails
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
		return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
	}

	// Store the complete forward route hops for later retrieval
	nrg.SetForwardHops(forwardPath)

	nrg.rg.startOffServiceLoops()

	lPort := forwardDesc.SrcPort()
	r.logger.Debugf("Created new ping route to %s on port %d", rPK, lPort)

	return nrg, nil
}

// PingRoute dials to a given visor of 'rPK' to establish a ping route.
// Uses the same route-finding and setup machinery as DialRoutes but
// without route setup hooks (transport creation). This tests the routing
// infrastructure directly.
// If opts.TransportID is set, uses that specific transport directly (skips route calculation).
func (r *router) PingRoute(
	ctx context.Context,
	rPK cipher.PubKey,
	lPort, rPort routing.Port,
	opts *DialOptions,
) (net.Conn, error) {

	if rPK.Null() {
		err := ErrRemoteEmptyPK
		r.logger.WithError(err).Error("Failed to dial ping route.")
		return nil, fmt.Errorf("failed to dial ping route: %w", err)
	}

	if opts == nil {
		opts = DefaultDialOptions()
	}

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, lPort, rPort)

	// Debug: log what options we received
	r.logger.Debugf("PingRoute opts: TransportID=%s, ForwardHops=%d, ReverseHops=%d", opts.TransportID, len(opts.ForwardHops), len(opts.ReverseHops))

	// If full route is specified, use it directly without route calculation
	if len(opts.ForwardHops) > 0 && len(opts.ReverseHops) > 0 {
		r.logger.Debugf("Using specified %d-hop route to %s", len(opts.ForwardHops), rPK)
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = 0 // No calculation needed
		r.lastRouteCalcMu.Unlock()
		return r.setupPingRoute(ctx, forwardDesc, opts.ForwardHops, opts.ReverseHops, rPK, opts)
	}

	// If TransportID is specified, use it directly without route calculation (single hop)
	if opts.TransportID != (uuid.UUID{}) {
		r.logger.Debugf("Using specified transport %s for direct route to %s", opts.TransportID, rPK)
		forwardPath := []routing.Hop{{TpID: opts.TransportID, From: lPK, To: rPK}}
		reversePath := []routing.Hop{{TpID: opts.TransportID, From: rPK, To: lPK}}
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = 0 // No calculation needed
		r.lastRouteCalcMu.Unlock()
		return r.setupPingRoute(ctx, forwardDesc, forwardPath, reversePath, rPK, opts)
	}

	const maxRetries = 3
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		forwardPath, reversePath, err := r.fetchBestRoutes(ctx, lPK, rPK, opts)
		if err != nil {
			lastErr = fmt.Errorf("route finder: %w", err)
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Ping route finder failed (attempt %d/%d), retrying...", attempt, maxRetries)
				continue
			}
			return nil, lastErr
		}

		conn, err := r.setupPingRoute(ctx, forwardDesc, forwardPath, reversePath, rPK, opts)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Ping route setup failed (attempt %d/%d), retrying...", attempt, maxRetries)
				continue
			}
			return nil, lastErr
		}

		return conn, nil
	}

	return nil, fmt.Errorf("failed to establish ping route after %d attempts: %w", maxRetries, lastErr)
}

// MeasureTransportLatency measures the latency of a specific transport by creating
// a temporary direct route over that transport, performing multiple ping/pong measurements,
// and returning latency statistics (min/max/avg). The route is closed after measurement.
func (r *router) MeasureTransportLatency(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) (float64, error) {
	r.logger.Debugf("Measuring latency for transport %s to %s", tpID, remote)

	// Create ping route using the specific transport (skips route finder)
	opts := &DialOptions{
		TransportID: tpID,
	}

	// Use ephemeral ports for the ping route
	lPort := routing.Port(skyenv.LatencyProbePort)
	rPort := routing.Port(skyenv.LatencyProbePort)

	conn, err := r.PingRoute(ctx, remote, lPort, rPort, opts)
	if err != nil {
		r.logger.WithError(err).Debugf("Failed to establish ping route for latency measurement on transport %s", tpID)
		return 0, fmt.Errorf("failed to establish ping route: %w", err)
	}

	// Ensure we close the route when done
	defer func() {
		if err := conn.Close(); err != nil {
			r.logger.WithError(err).Debug("Failed to close ping route after latency measurement")
		}
	}()

	// Get the RouteGroup to perform actual ping/pong measurements
	rg, ok := conn.(*RouteGroup)
	if !ok {
		// Try NoiseRouteGroup
		if nrg, ok := conn.(*NoiseRouteGroup); ok {
			rg = nrg.rg
		} else {
			return 0, errors.New("unexpected connection type, cannot measure latency")
		}
	}

	// Perform multiple ping measurements (5 pings for good statistics)
	const pingCount = 5
	min, max, avg, err := rg.MeasureLatency(ctx, pingCount)
	if err != nil {
		r.logger.WithError(err).Debugf("Failed to measure latency for transport %s", tpID)
		return 0, fmt.Errorf("failed to measure latency: %w", err)
	}

	r.logger.Debugf("Transport %s latency: min=%.2f ms, max=%.2f ms, avg=%.2f ms", tpID, min, max, avg)

	// Set full stats on the transport if accessible
	r.mx.Lock()
	if tp := r.tm.Transport(tpID); tp != nil {
		tp.SetLatencyStats(transport.LatencyStats{
			Min: min,
			Max: max,
			Avg: avg,
		})
	}
	r.mx.Unlock()

	// Return average for backwards compatibility
	return avg, nil
}

// AcceptRoutes should block until we receive an AddRules packet from SetupNode
// that contains ConsumeRule(s) or ForwardRule(s).
// Then the following should happen:
// - Save to routing.Table and internal RouteGroup map.
// - Return the RoutingGroup.
func (r *router) AcceptRoutes(ctx context.Context) (net.Conn, error) {
	var (
		rules routing.EdgeRules
		ok    bool
	)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case rules, ok = <-r.accept:
	}

	if !ok {
		err := &net.OpError{
			Op:     "accept",
			Net:    "skynet",
			Source: nil,
			Err:    errors.New("use of closed network connection"),
		}

		return nil, err
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		return nil, fmt.Errorf("SaveRoutingRules: %w", err)
	}

	nsConf := noise.Config{
		LocalPK:   r.conf.PubKey,
		LocalSK:   r.conf.SecKey,
		RemotePK:  rules.Desc.SrcPK(),
		Initiator: false,
	}

	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf)
	if err != nil {
		// Clean up saved rules if route group setup fails
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
		return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
	}

	nrg.rg.startOffServiceLoops()

	return nrg, nil
}

// Serve starts transport listening loop.
func (r *router) Serve(ctx context.Context) error {
	r.logger.Debug("Starting router")

	go r.serveTransportManager(ctx)

	go r.serveSetup()

	return nil
}

func (r *router) serveTransportManager(ctx context.Context) {
	for {
		packet, err := r.tm.ReadPacket()
		if err != nil {
			if err == transport.ErrNotServing {
				r.logger.WithError(err).Debug("Stopped reading packets")
				return
			}

			r.logger.WithError(err).Error("Stopped reading packets due to unexpected error.")
			return
		}

		if err := r.handleTransportPacket(ctx, packet); err != nil {
			if err == transport.ErrNotServing {
				r.logger.WithError(err).Warnf("Stopped serving Transport.")
				return
			}

			r.logger.Warnf("Failed to handle transport frame: %v", err)
		}
	}
}

func (r *router) serveSetup() {
	for {
		conn, err := r.sl.AcceptStream()
		if err != nil {
			log := r.logger.WithError(err)
			if errors.Is(err, dmsg.ErrEntityClosed) {
				log.Debug("Setup client stopped serving.")
				return
			}
			log.Error("Setup client stopped serving due to unexpected error.")
			return
		}

		remotePK := conn.RawRemoteAddr().PK
		if !r.SetupIsTrusted(remotePK) {
			r.logger.Warnf("closing conn from untrusted setup node: %v", conn.Close())
			continue
		}

		r.logger.Debugf("handling setup request: setupPK(%s)", remotePK)

		go r.rpcSrv.ServeConn(conn)
	}
}

// TODO: fix gocyclo error.
//
//gocyclo:ignore
func (r *router) saveRouteGroupRules(ctx context.Context, rules routing.EdgeRules, nsConf noise.Config) (*NoiseRouteGroup, error) {
	// Check context before starting
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.logger.Debugf("Saving route group rules with desc: %s", &rules.Desc)

	// When route group is wrapped with noise, it's put into `nrgs`. but before that,
	// in the process of wrapping we still need to use this route group to handle
	// handshake packets. so we keep these not-yet wrapped rgs in the `rgsRaw`
	// until they get wrapped with noise

	r.mx.Lock()

	// first ensure that this rg is not being wrapped with noise right now
	if _, ok := r.rgsRaw[rules.Desc]; ok {
		r.mx.Unlock()
		r.logger.Warnf("Desc %s already reserved, skipping...", rules.Desc)
		return nil, fmt.Errorf("noise route group with desc %s already being initialized", &rules.Desc)
	}

	// we need to close currently existing wrapped rg if there's one
	nrg, ok := r.rgsNs[rules.Desc]

	// Look up the transport for the first hop - fail early if not available
	nextTpID := rules.Forward.NextTransportID()
	tp := r.tm.Transport(nextTpID)
	if tp == nil {
		r.mx.Unlock()
		return nil, fmt.Errorf("transport %s not found locally", nextTpID)
	}

	rg := NewRouteGroup(DefaultRouteGroupConfig(), r.rt, rules.Desc, r.mLogger)
	rg.appendRules(rules.Forward, rules.Reverse, tp)
	// we put raw rg so it can be accessible to the router when handshake packets come in
	r.rgsRaw[rules.Desc] = rg
	r.mx.Unlock()

	if nsConf.Initiator {
		if err := rg.sendHandshake(true); err != nil {
			r.logger.WithError(err).Errorf("Failed to send handshake from route group (%s): %v, closing...",
				&rules.Desc, err)
			if err := rg.Close(); err != nil {
				r.logger.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
			}
			// Clean up rgsRaw on failure to prevent blocking future connections
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()

			return nil, fmt.Errorf("sendHandshake (%s): %w", &rules.Desc, err)
		}
	}

	// Use parent context for overall timeout, with handshake-specific timeout as fallback
	hsCtx, hsCancel := context.WithTimeout(ctx, handshakeAwaitTimeout)
	defer hsCancel()

	select {
	case <-rg.handshakeProcessed:
	case <-hsCtx.Done():
		// Check if parent context was canceled (e.g., setup timeout)
		if ctx.Err() != nil {
			r.logger.Debugf("Route group setup canceled for %s: %v", &rules.Desc, ctx.Err())
			// Clean up
			if err := rg.Close(); err != nil {
				r.logger.WithError(err).Warnf("Failed to close route group on context cancellation")
			}
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()
			return nil, ctx.Err()
		}
		// remote should send handshake packet during initialization,
		// if no packet received during timeout interval, we're dealing
		// with the old visor
		rg.handshakeProcessedOnce.Do(func() {
			rg.encrypt = false
			close(rg.handshakeProcessed)
		})
	}

	if !nsConf.Initiator {
		if err := rg.sendHandshake(true); err != nil {
			r.logger.WithError(err).Errorf("Failed to send handshake from route group (%s): %v, closing...",
				&rules.Desc, err)
			if err := rg.Close(); err != nil {
				r.logger.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
			}
			// Clean up rgsRaw on failure to prevent blocking future connections
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()

			return nil, fmt.Errorf("sendHandshake (%s): %w", &rules.Desc, err)
		}
	}

	if ok && nrg != nil {
		// if already functioning wrapped rg exists, we safely close it here
		r.logger.Debugf("Noise route group with desc %s already exists, closing the old one and replacing...", &rules.Desc)

		if err := nrg.Close(); err != nil {
			r.logger.Errorf("Error closing already existing noise route group: %v", err)
		}

		r.logger.Debugf("Successfully closed old noise route group")
	}

	if rg.encrypt {
		// wrapping rg with noise
		wrappedRG, err := network.EncryptConn(nsConf, rg)
		if err != nil {
			r.logger.WithError(err).Errorf("Failed to wrap route group (%s): %v, closing...", &rules.Desc, err)
			if err := rg.Close(); err != nil {
				r.logger.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
			}
			// Clean up rgsRaw on failure to prevent blocking future connections
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()

			return nil, fmt.Errorf("WrapConn (%s): %w", &rules.Desc, err)
		}

		nrg = &NoiseRouteGroup{
			rg:   rg,
			Conn: wrappedRG,
		}
	} else {
		nrg = &NoiseRouteGroup{
			rg:   rg,
			Conn: rg,
		}
	}

	r.mx.Lock()
	// put ready nrg and remove raw rg, we won't need it anymore
	r.rgsNs[rules.Desc] = nrg
	delete(r.rgsRaw, rules.Desc)
	r.mx.Unlock()

	return nrg, nil
}

// appendRouteToGroup adds an additional transport/rule pair to an existing
// mux-enabled NoiseRouteGroup. Used for establishing additional parallel routes.
func (r *router) appendRouteToGroup(nrg *NoiseRouteGroup, rules routing.EdgeRules) error {
	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		return fmt.Errorf("SaveRoutingRules: %w", err)
	}

	nextTpID := rules.Forward.NextTransportID()
	tp := r.tm.Transport(nextTpID)
	if tp == nil {
		return fmt.Errorf("transport %s not found for additional mux route", nextTpID)
	}

	nrg.rg.appendRules(rules.Forward, rules.Reverse, tp)

	// Send handshake on the new transport to inform the remote side
	rg := nrg.rg
	rg.mu.Lock()
	lastIdx := len(rg.tps) - 1
	lastTp := rg.tps[lastIdx]
	lastRule := rg.fwd[lastIdx]
	rg.mu.Unlock()

	packet := routing.MakeHandshakePacket(lastRule.NextRouteID(), rg.encrypt, routing.CapMux|routing.CapSACK)
	if err := rg.writePacket(context.Background(), lastTp, packet, lastRule.KeyRouteID()); err != nil {
		r.logger.WithError(err).Warn("Failed to send handshake on additional mux transport")
	}

	r.logger.Debugf("Appended mux route via transport %s to RouteGroup %s", nextTpID, &rules.Desc)
	return nil
}

func (r *router) handleTransportPacket(ctx context.Context, packet routing.Packet) error {
	switch packet.Type() {
	case routing.DataPacket, routing.HandshakePacket:
		return r.handleDataHandshakePacket(ctx, packet)
	case routing.ClosePacket:
		return r.handleClosePacket(ctx, packet)
	case routing.KeepAlivePacket:
		return r.handleKeepAlivePacket(ctx, packet)
	case routing.PingPacket:
		return r.handlePingPacket(ctx, packet)
	case routing.PongPacket:
		return r.handlePongPacket(ctx, packet)
	case routing.ErrorPacket:
		return r.handleErrorPacket(ctx, packet)
	case routing.SACKPacket:
		return r.handleSACKRouterPacket(ctx, packet)
	default:
		return ErrUnknownPacketType
	}
}

func (r *router) handleDataHandshakePacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}
	log := r.logger.WithField("func", "router.handleDataHandshakePacket")
	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling packet with descriptor %s", &desc)

	if ok {
		if nrg == nil {
			return errors.New("noiseRouteGroup is nil")
		}

		// in this case we have already initialized nrg and may use it straightforward
		log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
			len(packet.Payload()), packet.RouteID(), rule)

		return nrg.handlePacket(packet)
	}

	// we don't have nrg for this packet. it's either handshake message or
	// we don't have route for this one completely

	rg, ok := r.initializingRouteGroup(desc)
	if !ok {
		// no route, just return error
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("initializing RouteGroup is nil")
	}

	// handshake packet, handling with the raw rg
	log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
}

func (r *router) handleClosePacket(ctx context.Context, packet routing.Packet) error {
	routeID := packet.RouteID()

	log := r.logger.WithField("func", "router.handleClosePacket")
	log.Tracef("Received close packet for route ID %v", routeID)

	rule, err := r.GetRule(routeID)
	if err != nil {
		return err
	}

	if rule.Type() == routing.RuleReverse {
		log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())
	} else {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
	}

	defer func() {
		routeIDs := []routing.RouteID{routeID}
		r.rt.DelRules(routeIDs)
	}()

	if t := rule.Type(); t == routing.RuleIntermediary {
		log.Traceln("Handling intermediary close packet")
		return r.forwardPacket(ctx, packet, rule)
	}

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling close packet with descriptor %s", &desc)

	if !ok {
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	defer r.removeNoiseRouteGroup(desc)

	if nrg == nil {
		return errors.New("noiseRouteGroup is nil")
	}

	log.Tracef("Got new remote close packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	closeCode := routing.CloseCode(packet.Payload()[0])

	if nrg.isClosed() {
		return io.ErrClosedPipe
	}

	if err := nrg.handlePacket(packet); err != nil {
		return fmt.Errorf("error handling close packet with code %d by noise route group with descriptor %s: %v",
			closeCode, &desc, err)
	}

	return nil
}

func (r *router) handleKeepAlivePacket(ctx context.Context, packet routing.Packet) error {
	routeID := packet.RouteID()

	log := r.logger.WithField("func", "router.handleKeepAlivePacket")
	log.Tracef("Received keepalive packet for route ID %v", routeID)

	rule, err := r.GetRule(routeID)
	if err != nil {
		return err
	}

	if rule.Type() == routing.RuleReverse {
		log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())
	} else {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
	}

	// propagate packet only for intermediary rule. forward rule workflow doesn't get here,
	// consume rules should be omitted, activity is already updated
	if t := rule.Type(); t == routing.RuleIntermediary {
		log.Traceln("Handling intermediary keep-alive packet")
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Route ID %v found, updated activity", routeID)

	return nil
}

func (r *router) handlePingPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}
	log := r.logger.WithField("func", "router.handlePingPacket")

	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling packet with descriptor %s", &desc)

	if ok {
		if nrg == nil {
			return errors.New("noiseRouteGroup is nil")
		}

		// in this case we have already initialized nrg and may use it straightforward
		log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
			len(packet.Payload()), packet.RouteID(), rule)

		return nrg.handlePacket(packet)
	}

	// we don't have nrg for this packet. it's either handshake message or
	// we don't have route for this one completely

	rg, ok := r.initializingRouteGroup(desc)
	if !ok {
		// no route, just return error
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("initializing RouteGroup is nil")
	}

	// handshake packet, handling with the raw rg
	log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
}

func (r *router) handlePongPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}
	log := r.logger.WithField("func", "router.handlePongPacket")

	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling packet with descriptor %s", &desc)

	if ok {
		if nrg == nil {
			return errors.New("noiseRouteGroup is nil")
		}

		// in this case we have already initialized nrg and may use it straightforward
		log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
			len(packet.Payload()), packet.RouteID(), rule)

		return nrg.handlePacket(packet)
	}

	// we don't have nrg for this packet. it's either handshake message or
	// we don't have route for this one completely

	rg, ok := r.initializingRouteGroup(desc)
	if !ok {
		// no route, just return error
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("initializing RouteGroup is nil")
	}

	// handshake packet, handling with the raw rg
	log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
}

func (r *router) handleErrorPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}
	log := r.logger.WithField("func", "router.handleErrorPacket")
	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling packet with descriptor %s", &desc)

	if ok {
		if nrg == nil {
			return errors.New("noiseRouteGroup is nil")
		}

		// in this case we have already initialized nrg and may use it straightforward
		log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
			len(packet.Payload()), packet.RouteID(), rule)

		return nrg.handlePacket(packet)
	}

	// we don't have nrg for this packet and we don't have route for this one completely

	rg, ok := r.initializingRouteGroup(desc)
	if !ok {
		// no route, just return error
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("initializing RouteGroup is nil")
	}

	// handshake packet, handling with the raw rg
	log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
}

func (r *router) handleSACKRouterPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}

	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		return r.forwardPacket(ctx, packet, rule)
	}

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)
	if !ok || nrg == nil {
		return nil // SACK for unknown route, ignore silently
	}

	return nrg.handlePacket(packet)
}

// GetRule gets routing rule.
func (r *router) GetRule(routeID routing.RouteID) (routing.Rule, error) {
	rule, err := r.rt.Rule(routeID)
	if err != nil {
		return nil, fmt.Errorf("routing table: %w", err)
	}

	if rule == nil {
		return nil, errors.New("unknown RouteID")
	}

	// TODO(evanlinjin): This is a workaround for ensuring the read-in rule is of the correct size.
	// Sometimes it is not, causing a segfault later down the line.
	if len(rule) < routing.RuleHeaderSize {
		return nil, errors.New("corrupted rule")
	}

	return rule, nil
}

// UpdateRuleActivity updates routing rule activity
func (r *router) UpdateRuleActivity(routeID routing.RouteID) error {
	err := r.rt.UpdateActivity(routeID)
	if err != nil {
		return fmt.Errorf("error updating activity for route ID %d: %w", routeID, err)
	}

	return nil
}

// Close safely stops Router.
func (r *router) Close() error {
	if r == nil {
		return nil
	}

	r.logger.Debug("Closing all App connections and RouteGroups")
	r.once.Do(func() {
		close(r.done)
		r.mx.Lock()
		close(r.accept)
		r.mx.Unlock()
	})
	if err := r.sl.Close(); err != nil {
		r.logger.WithError(err).Warnf("closing route_manager returned error")
		return err
	}

	return nil
}

func (r *router) forwardPacket(ctx context.Context, packet routing.Packet, rule routing.Rule) error {
	tp := r.tm.Transport(rule.NextTransportID())
	if tp == nil {
		return errors.New("unknown transport")
	}

	var p routing.Packet

	switch packet.Type() {
	case routing.DataPacket:
		var err error

		p, err = routing.MakeDataPacket(rule.NextRouteID(), packet.Payload())
		if err != nil {
			return err
		}
	case routing.HandshakePacket:
		// Forward the full handshake payload (preserves capability bitmap for extended handshakes)
		p = routing.MakeHandshakePacketRaw(rule.NextRouteID(), packet.Payload())
	case routing.KeepAlivePacket:
		p = routing.MakeKeepAlivePacket(rule.NextRouteID())
	case routing.ClosePacket:
		p = routing.MakeClosePacket(rule.NextRouteID(), routing.CloseCode(packet.Payload()[0]))
	case routing.PingPacket:
		timestamp := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset:]))    //nolint: gosec
		throughput := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset+8:])) //nolint: gosec
		p = routing.MakePingPacket(rule.NextRouteID(), timestamp, throughput)
	case routing.PongPacket:
		timestamp := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset:])) //nolint: gosec
		p = routing.MakePongPacket(rule.NextRouteID(), timestamp)
	case routing.ErrorPacket:
		var err error

		p, err = routing.MakeErrorPacket(rule.NextRouteID(), packet.Payload())
		if err != nil {
			return err
		}
	case routing.SACKPacket:
		p = routing.MakeSACKPacket(rule.NextRouteID(), packet.SACKLastContiguousSeq(), packet.SACKBitmap())
	default:
		return fmt.Errorf("packet of type %s can't be forwarded", packet.Type())
	}

	if err := tp.WritePacket(ctx, p); err != nil {
		return err
	}

	// successfully forwarded packet, may update the rule activity now
	if err := r.UpdateRuleActivity(rule.KeyRouteID()); err != nil {
		r.logger.Errorf("Failed to update activity for rule with route ID %d: %v", rule.KeyRouteID(), err)
	}

	r.logger.Debugf("Forwarded packet via Transport %s using rule %d", rule.NextTransportID(), rule.KeyRouteID())

	return nil
}

// RemoveRouteDescriptor removes route group rule.
func (r *router) RemoveRouteDescriptor(desc routing.RouteDescriptor) {
	rules := r.rt.AllRules()
	for _, rule := range rules {
		if rule.Type() != routing.RuleReverse {
			continue
		}

		rd := rule.RouteDescriptor()
		if rd.DstPK() == desc.DstPK() && rd.DstPort() == desc.DstPort() && rd.SrcPort() == desc.SrcPort() {
			r.rt.DelRules([]routing.RouteID{rule.KeyRouteID()})
			return
		}
	}
}

func (r *router) fetchBestRoutes(ctx context.Context, src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	// TODO: use opts
	if opts == nil {
		opts = DefaultDialOptions() // nolint
	}

	// Check if force local routes is enabled
	r.forceLocalRoutesMu.Lock()
	forceLocal := r.forceLocalRoutes
	r.forceLocalRoutesMu.Unlock()

	if forceLocal {
		r.logger.Info("Calculating route locally (--local-route enabled)")
		calcStart := time.Now()
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, src, dst, opts)
		calcTime := time.Since(calcStart)
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = calcTime
		r.lastRouteCalcMu.Unlock()
		if localErr == nil {
			r.logger.Infof("Local route calculated in %v: Forward=%v, Reverse=%v", calcTime, localFwd, localRev)
		}
		return localFwd, localRev, localErr
	}

	retries := opts.Retries

	r.logger.Debugf("Requesting new routes from %s to %s", src, dst)

	timer := time.NewTimer(retryDuration)
	defer timer.Stop()

	forward := [2]cipher.PubKey{src, dst}
	backward := [2]cipher.PubKey{dst, src}

fetchRoutesAgain:
	// Check context before making network calls
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context canceled before route fetch: %w", err)
	}

	paths, err := r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward, backward},
		&rfclient.RouteOptions{MinHops: r.conf.MinHops, MaxHops: r.conf.MaxHops})

	if err == rfclient.ErrTransportNotFound {
		// Try local route calculation - may find a local transport that's not yet in TPD
		r.logger.Info("Route finder returned transport not found, attempting local route calculation...")
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, src, dst, opts)
		if localErr == nil {
			r.logger.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
			return localFwd, localRev, nil
		}
		r.logger.WithError(localErr).Debug("Local route calculation also failed")
		return nil, nil, err
	}
	// simple retries condition
	if retries == 0 {
		// Try local route calculation as fallback before giving up
		r.logger.Info("Route finder exhausted retries, attempting local route calculation...")
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, src, dst, opts)
		if localErr == nil {
			r.logger.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
			return localFwd, localRev, nil
		}
		r.logger.WithError(localErr).Warn("Local route calculation also failed")
		r.logger.Errorf(ErrNoRouteFound.Error())
		return nil, nil, ErrNoRouteFound
	}
	if retries > 0 {
		retries--
	}

	if err != nil {
		select {
		case <-timer.C:
			// Try local route calculation as fallback
			r.logger.Info("Route finder timed out, attempting local route calculation...")
			localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, src, dst, opts)
			if localErr == nil {
				r.logger.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
				return localFwd, localRev, nil
			}
			r.logger.WithError(localErr).Warn("Local route calculation also failed")
			return nil, nil, err
		default:
			time.Sleep(retryInterval)
			goto fetchRoutesAgain
		}
	}

	r.logger.Debugf("Found routes Forward: %s. Reverse %s", paths[forward], paths[backward])

	return paths[forward][0], paths[backward][0], nil
}

// calculateLocalRoutes attempts to calculate routes locally using the transport manager
// and transport discovery data, without relying on the route finder service.
// It supports 1-hop (direct), 2-hop routes, and self-ping (src == dst).
func (r *router) calculateLocalRoutes(ctx context.Context, src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	dialOpts := opts
	if r.tm == nil {
		return nil, nil, errors.New("transport manager not available")
	}

	dc := r.tm.Conf.DiscoveryClient
	if dc == nil {
		return nil, nil, errors.New("discovery client not available")
	}

	isSelfPing := src == dst
	r.logger.Debugf("Calculating route locally from %s to %s (self-ping=%v)", src, dst, isSelfPing)

	// Collect local transports
	type localTp struct {
		id       uuid.UUID
		remotePK cipher.PubKey
		tpType   string
	}
	var localTps []localTp

	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil {
			return true
		}
		localTps = append(localTps, localTp{
			id:       tp.Entry.ID,
			remotePK: tp.Entry.RemoteEdge(src),
			tpType:   string(tp.Entry.Type),
		})
		return true
	})

	if len(localTps) == 0 {
		return nil, nil, errors.New("no local transports available")
	}

	r.logger.Debugf("Found %d local transports", len(localTps))

	// Check for direct (1-hop) route first
	for _, tp := range localTps {
		if tp.remotePK == dst {
			// Skip DMSG transports for mux (DMSG is a relay, not suitable for multiplexing)
			if dialOpts != nil && dialOpts.ExcludeDMSG && tp.tpType == "dmsg" {
				r.logger.Debugf("Skipping DMSG transport %s (excluded for mux)", tp.id)
				continue
			}
			// Skip excluded transport IDs (used by mux to get different transports)
			excluded := false
			if dialOpts != nil {
				for _, exID := range dialOpts.ExcludeTransportIDs {
					if tp.id == exID {
						excluded = true
						break
					}
				}
			}
			if excluded {
				r.logger.Debugf("Skipping excluded transport %s to destination", tp.id)
				continue
			}
			r.logger.Debugf("Found direct transport to destination: %s (type=%s)", tp.id, tp.tpType)
			fwdHop := routing.Hop{TpID: tp.id, From: src, To: dst}
			revHop := routing.Hop{TpID: tp.id, From: dst, To: src}
			return []routing.Hop{fwdHop}, []routing.Hop{revHop}, nil
		}
	}

	// Build transport cache from single GetAllTransports() call
	// This replaces N individual GetTransportsByEdge API calls with one bulk fetch
	allEntries, err := dc.GetAllTransports(ctx)
	if err != nil {
		r.logger.WithError(err).Warn("Failed to fetch all transports for route calculation")
		return nil, nil, fmt.Errorf("failed to fetch transport discovery data: %w", err)
	}

	// Build lookup map: pubkey -> transports involving that pubkey
	transportsByEdge := make(map[cipher.PubKey][]*transport.Entry)
	for _, entry := range allEntries {
		if entry == nil {
			continue
		}
		for _, edge := range entry.Edges {
			transportsByEdge[edge] = append(transportsByEdge[edge], entry)
		}
	}
	r.logger.Debugf("Built transport cache with %d entries covering %d visors", len(allEntries), len(transportsByEdge))

	// For self-ping, try 2-hop route through any available transport partner
	// This allows testing the full route setup even without a direct self-transport
	if isSelfPing {
		r.logger.Debug("Self-ping: looking for 2-hop loopback route through transport partner")
		for _, tp := range localTps {
			intermediatePK := tp.remotePK
			if intermediatePK == src {
				// Skip actual self-transports (already checked above)
				continue
			}

			// For self-ping via 2-hop: src -> intermediate -> src
			// We need the intermediate to have a transport back to us
			intermediateEntries := transportsByEdge[intermediatePK]
			if len(intermediateEntries) == 0 {
				continue
			}

			for _, entry := range intermediateEntries {
				if entry == nil {
					continue
				}
				remotePK := entry.RemoteEdge(intermediatePK)
				if remotePK == src {
					r.logger.Debugf("Found 2-hop self-ping route via %s (tp1=%s, tp2=%s)", intermediatePK, tp.id, entry.ID)

					// Build loopback route: src -> intermediate -> src
					fwdHop1 := routing.Hop{TpID: tp.id, From: src, To: intermediatePK}
					fwdHop2 := routing.Hop{TpID: entry.ID, From: intermediatePK, To: src}

					// Reverse is the same for self-ping
					revHop1 := routing.Hop{TpID: entry.ID, From: src, To: intermediatePK}
					revHop2 := routing.Hop{TpID: tp.id, From: intermediatePK, To: src}

					return []routing.Hop{fwdHop1, fwdHop2}, []routing.Hop{revHop1, revHop2}, nil
				}
			}
		}
		return nil, nil, errors.New("self-ping: no 2-hop loopback route found through transport partners")
	}

	// Try 2-hop routes through intermediate visors
	for _, tp := range localTps {
		intermediatePK := tp.remotePK

		// Look up transports from cache (built from single GetAllTransports call)
		intermediateEntries := transportsByEdge[intermediatePK]
		if len(intermediateEntries) == 0 {
			continue
		}

		// Check if any of the intermediate visor's transports connect to our destination
		for _, entry := range intermediateEntries {
			if entry == nil {
				continue
			}
			// Check if this transport connects to our destination
			remotePK := entry.RemoteEdge(intermediatePK)
			if remotePK == dst {
				r.logger.Debugf("Found 2-hop route via %s (tp1=%s, tp2=%s)", intermediatePK, tp.id, entry.ID)

				// Build forward route: src -> intermediate -> dst
				fwdHop1 := routing.Hop{TpID: tp.id, From: src, To: intermediatePK}
				fwdHop2 := routing.Hop{TpID: entry.ID, From: intermediatePK, To: dst}

				// Build reverse route: dst -> intermediate -> src
				revHop1 := routing.Hop{TpID: entry.ID, From: dst, To: intermediatePK}
				revHop2 := routing.Hop{TpID: tp.id, From: intermediatePK, To: src}

				return []routing.Hop{fwdHop1, fwdHop2}, []routing.Hop{revHop1, revHop2}, nil
			}
		}
	}

	return nil, nil, errors.New("no route found through local transports")
}

// SetupIsTrusted checks if setup node is trusted.
func (r *router) SetupIsTrusted(sPK cipher.PubKey) bool {
	_, ok := r.trustedVisors[sPK]
	return ok
}

// SetMinHop set minhop when visor running
func (r *router) SetMinHop(minhop uint16) {
	r.conf.MinHops = minhop
}

// SetExistingTPOnly sets whether to only use existing transports for routing.
// When true, no new transports will be created when dialing routes.
func (r *router) SetExistingTPOnly(enabled bool) {
	r.existingTpOnlyMu.Lock()
	defer r.existingTpOnlyMu.Unlock()
	r.existingTpOnly = enabled
	r.logger.Infof("SetExistingTPOnly: %v", enabled)
}

// SetForceLocalRoutes sets whether to skip the route finder and use local route calculation.
// When true, routes are calculated locally using transport manager and TPD data.
func (r *router) SetForceLocalRoutes(enabled bool) {
	r.forceLocalRoutesMu.Lock()
	defer r.forceLocalRoutesMu.Unlock()
	r.forceLocalRoutes = enabled
	r.logger.Infof("SetForceLocalRoutes: %v", enabled)
}

// GetLastRouteCalcTime returns the time it took to calculate the last local route.
func (r *router) GetLastRouteCalcTime() time.Duration {
	r.lastRouteCalcMu.Lock()
	defer r.lastRouteCalcMu.Unlock()
	return r.lastRouteCalcTime
}

// Saves `rules` to the routing table.
func (r *router) SaveRoutingRules(rules ...routing.Rule) error {
	for _, rule := range rules {
		if err := r.rt.SaveRule(rule); err != nil {
			r.logger.WithError(err).Error("Error saving rule to routing table")
			return fmt.Errorf("routing table: %w", err)
		}

		r.logger.Debugf("Save new Routing Rule with ID %d %s", rule.KeyRouteID(), rule)
	}

	return nil
}

func (r *router) ReserveKeys(n int) ([]routing.RouteID, error) {
	ids, err := r.rt.ReserveKeys(n)
	if err != nil {
		r.logger.WithError(err).Error("Error reserving IDs")
	}

	return ids, err
}

func (r *router) popNoiseRouteGroup(desc routing.RouteDescriptor) (*NoiseRouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	nrg, ok := r.rgsNs[desc]
	if !ok {
		return nil, false
	}

	delete(r.rgsNs, desc)

	return nrg, true
}

func (r *router) noiseRouteGroup(desc routing.RouteDescriptor) (*NoiseRouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	nrg, ok := r.rgsNs[desc]

	return nrg, ok
}

func (r *router) initializingRouteGroup(desc routing.RouteDescriptor) (*RouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	rg, ok := r.rgsRaw[desc]

	return rg, ok
}

func (r *router) popRawRouteGroup(desc routing.RouteDescriptor) (*RouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	rg, ok := r.rgsRaw[desc]
	if !ok {
		return nil, false
	}

	delete(r.rgsRaw, desc)

	return rg, true
}

func (r *router) removeNoiseRouteGroup(desc routing.RouteDescriptor) {
	r.mx.Lock()
	defer r.mx.Unlock()

	delete(r.rgsNs, desc)
}

func (r *router) IntroduceRules(rules routing.EdgeRules) error {
	// Save rules immediately to avoid race with incoming transport packets
	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		return fmt.Errorf("SaveRoutingRules: %w", err)
	}

	// Check if we already have an active mux-enabled route group for this descriptor.
	// If so, append the additional route instead of creating a new connection.
	r.mx.Lock()
	if nrg, ok := r.rgsNs[rules.Desc]; ok && nrg != nil && nrg.rg.mux != nil {
		r.mx.Unlock()

		nextTpID := rules.Forward.NextTransportID()
		tp := r.tm.Transport(nextTpID)
		if tp == nil {
			return fmt.Errorf("transport %s not found for additional mux route", nextTpID)
		}
		nrg.rg.appendRules(rules.Forward, rules.Reverse, tp)
		r.logger.Debugf("Appended additional mux route to existing RouteGroup for %s", &rules.Desc)
		return nil
	}
	r.mx.Unlock()

	// Handle ping/latency probe routes (port 46) directly without going through
	// the accept channel. These are ephemeral routes from other visors measuring
	// transport latency — they don't need to be delivered to any application.
	// Processing them in-line prevents them from blocking application routes
	// (proxy, VPN) in the accept queue, where each route blocks for up to the
	// handshake timeout (30s).
	if rules.Desc.DstPort() == routing.Port(skyenv.LatencyProbePort) || rules.Desc.SrcPort() == routing.Port(skyenv.LatencyProbePort) {
		go func() {
			nsConf := noise.Config{
				LocalPK:   r.conf.PubKey,
				LocalSK:   r.conf.SecKey,
				RemotePK:  rules.Desc.SrcPK(),
				Initiator: false,
			}
			nrg, err := r.saveRouteGroupRules(context.Background(), rules, nsConf)
			if err != nil {
				r.logger.WithError(err).Debug("Failed to setup ping route group (non-blocking)")
				r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
				return
			}
			nrg.rg.startOffServiceLoops()
		}()
		return nil
	}

	select {
	case <-r.done:
		return io.ErrClosedPipe
	default:
		r.mx.Lock()
		defer r.mx.Unlock()

		select {
		case r.accept <- rules:
			return nil
		case <-r.done:
			return io.ErrClosedPipe
		}
	}
}

// RoutesCount returns count of the routes stored within the routing table.
func (r *router) RoutesCount() int {
	return r.rt.Count()
}

// Rules gets all the rules stored within the routing table.
func (r *router) Rules() []routing.Rule {
	return r.rt.AllRules()
}

// Rule fetches rule by the route `id`.
func (r *router) Rule(id routing.RouteID) (routing.Rule, error) {
	return r.rt.Rule(id)
}

// SaveRule stores the `rule` within the routing table.
func (r *router) SaveRule(rule routing.Rule) error {
	return r.rt.SaveRule(rule)
}

// DelRules removes rules associated with `ids` from the routing table.
func (r *router) DelRules(ids []routing.RouteID) {
	rules := make([]routing.Rule, 0, len(ids))
	for _, id := range ids {
		rule, err := r.rt.Rule(id)
		if err != nil {
			r.logger.WithError(err).Errorf("Failed to get rule with ID %d on rule removal", id)
			continue
		}

		rules = append(rules, rule)
	}

	r.rt.DelRules(ids)

	for _, rule := range rules {
		r.removeRouteGroupOfRule(rule)
	}
}

func (r *router) rulesGCLoop() {
	ticker := time.NewTicker(r.conf.RulesGCInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			r.rulesGC()
		}
	}
}

func (r *router) rulesGC() {
	log := r.logger.WithField("func", "router.rulesGC")

	removedRules := r.rt.CollectGarbage()
	if len(removedRules) > 0 {
		log.WithField("rules_count", len(removedRules)).
			Debug("Removed rules.")
	}

	for _, rule := range removedRules {
		r.removeRouteGroupOfRule(rule)
	}
}

func (r *router) removeRouteGroupOfRule(rule routing.Rule) {
	log := r.logger.
		WithField("func", "router.removeRouteGroupOfRule").
		WithField("rule_type", rule.Type().String()).
		WithField("rule_keyRtID", rule.KeyRouteID())

	// we need to process only consume rules, cause we don't
	// really care about the other ones, other rules removal
	// doesn't affect our work here
	if rule.Type() != routing.RuleReverse {
		log.Debug("Nothing to be done")
		return
	}

	rDesc := rule.RouteDescriptor()
	log.WithField("rt_desc", rDesc.String()).
		Debug("Closing route group associated with rule...")

	// First try noise-wrapped route groups (fully initialized)
	nrg, ok := r.popNoiseRouteGroup(rDesc)
	if ok {
		if nrg.isClosed() {
			log.Debug("Noise route group already closed. Nothing to be done.")
			return
		}
		if err := nrg.Close(); err != nil {
			log.WithError(err).Error("Failed to close noise route group.")
			return
		}
		log.Debug("Noise route group closed.")
		return
	}

	// Also check raw route groups (still being initialized, e.g., interrupted during handshake)
	rg, ok := r.popRawRouteGroup(rDesc)
	if ok {
		if rg.isClosed() {
			log.Debug("Raw route group already closed. Nothing to be done.")
			return
		}
		if err := rg.Close(); err != nil {
			log.WithError(err).Error("Failed to close raw route group.")
			return
		}
		log.Debug("Raw route group closed.")
		return
	}

	log.Debug("No route group associated with rule. Nothing to be done.")
}

func (r *router) isTpdExist(rPK cipher.PubKey) bool {
	// check stcpr transport if exist
	_, err := r.tm.GetTransport(rPK, types.STCPR)
	if err == nil {
		return true
	}
	// check sudph transport if exist
	_, err = r.tm.GetTransport(rPK, types.SUDPH)
	if err == nil {
		return true
	}
	// check dmsg transport if exist
	_, err = r.tm.GetTransport(rPK, types.DMSG)
	return err == nil
}
