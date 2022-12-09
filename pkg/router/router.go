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
	"sync"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/noise"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/setup/setupclient"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

//go:generate mockery --name Router --case underscore --inpackage

const (
	// DefaultRouteKeepAlive is the default expiration interval for routes
	DefaultRouteKeepAlive = 30 * time.Second
	// DefaultRulesGCInterval is the default duration for garbage collection of routing rules.
	DefaultRulesGCInterval = 5 * time.Second
	acceptSize             = 1024

	handshakeAwaitTimeout = 2 * time.Second

	maxHops       = 50
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
	RouteGroupDialer setupclient.RouteGroupDialer
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
		c.RouteGroupDialer = setupclient.NewSetupNodeDialer()
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
	MinForwardRts int
	MaxForwardRts int
	MinConsumeRts int
	MaxConsumeRts int
}

// DefaultDialOptions returns default dial options.
// Used by default if nil is passed as options.
func DefaultDialOptions() *DialOptions {
	return &DialOptions{
		MinForwardRts: 1,
		MaxForwardRts: 1,
		MinConsumeRts: 1,
		MaxConsumeRts: 1,
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

	// Routing table related methods
	RoutesCount() int
	Rules() []routing.Rule
	Rule(routing.RouteID) (routing.Rule, error)
	SaveRule(routing.Rule) error
	DelRules([]routing.RouteID)
}

// Router implements visor.PacketRouter. It manages routing table by
// communicating with setup nodes, forward packets according to local
// rules and manages route groups for apps.
type router struct {
	mx               sync.Mutex
	conf             *Config
	logger           *logging.Logger
	mLogger          *logging.MasterLogger
	sl               *dmsg.Listener
	dmsgC            *dmsg.Client
	trustedVisors    map[cipher.PubKey]struct{}
	tm               *transport.Manager
	rt               routing.Table
	rgsNs            map[routing.RouteDescriptor]*NoiseRouteGroup // Noise-wrapped route groups to push incoming reads from transports.
	rgsRaw           map[routing.RouteDescriptor]*RouteGroup      // Not-yet-noise-wrapped route groups. when one of these gets wrapped, it gets removed from here
	rpcSrv           *rpc.Server
	accept           chan routing.EdgeRules
	done             chan struct{}
	once             sync.Once
	routeSetupHookMu sync.Mutex
	routeSetupHooks  []RouteSetupHook // see RouteSetupHook description
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

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, lPort, rPort)

	r.routeSetupHookMu.Lock()
	defer r.routeSetupHookMu.Unlock()
	if len(r.routeSetupHooks) != 0 {
		for _, rsf := range r.routeSetupHooks {
			if err := rsf(rPK, r.tm); err != nil {
				return nil, err
			}
		}
	}

	// check if transports are available
	ok := r.checkIfTransportAvailable()
	if !ok {
		return nil, ErrNoTransportFound
	}

	forwardPath, reversePath, err := r.fetchBestRoutes(lPK, rPK, opts)
	if err != nil {
		return nil, fmt.Errorf("route finder: %w", err)
	}

	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: DefaultRouteKeepAlive,
		Forward:   forwardPath,
		Reverse:   reversePath,
	}

	rules, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, req)
	if err != nil {
		r.logger.WithError(err).Error("Error dialing route group")
		return nil, err
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		r.logger.WithError(err).Error("Error saving routing rules")
		return nil, err
	}

	nsConf := noise.Config{
		LocalPK:   r.conf.PubKey,
		LocalSK:   r.conf.SecKey,
		RemotePK:  rPK,
		Initiator: true,
	}

	nrg, err := r.saveRouteGroupRules(rules, nsConf)
	if err != nil {
		return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
	}

	nrg.rg.startOffServiceLoops()

	r.logger.Debugf("Created new routes to %s on port %d", rPK, lPort)

	return nrg, nil
}

// PingRoute dials to a given visor of 'rPK'.
// 'lPort'/'rPort' specifies the local/remote ports respectively.
// A nil 'opts' input results in a value of '1' for all DialOptions fields.
// A single call to DialRoutes should perform the following:
// - Find routes via RouteFinder (in one call).
// - Setup routes via SetupNode (in one call).
// - Save to routing.Table and internal RouteGroup map.
// - Return RouteGroup if successful.
func (r *router) PingRoute(
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

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, lPK, lPort, rPort)

	r.routeSetupHookMu.Lock()
	defer r.routeSetupHookMu.Unlock()
	if len(r.routeSetupHooks) != 0 {
		for _, rsf := range r.routeSetupHooks {
			if err := rsf(rPK, r.tm); err != nil {
				return nil, err
			}
		}
	}

	// check if transports are available
	ok := r.checkIfTransportAvailable()
	if !ok {
		return nil, ErrNoTransportFound
	}
	forwardPath, reversePath, err := r.fetchPingRoute(lPK, rPK, opts)
	if err != nil {
		return nil, fmt.Errorf("route finder: %w", err)
	}

	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: DefaultRouteKeepAlive,
		Forward:   forwardPath,
		Reverse:   reversePath,
	}

	rules, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, req)
	if err != nil {
		r.logger.WithError(err).Error("Error dialing route group")
		return nil, err
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		r.logger.WithError(err).Error("Error saving routing rules")
		return nil, err
	}

	nsConf := noise.Config{
		LocalPK:   r.conf.PubKey,
		LocalSK:   r.conf.SecKey,
		RemotePK:  r.conf.PubKey,
		Initiator: true,
	}

	nrg, err := r.saveRouteGroupRules(rules, nsConf)
	if err != nil {
		return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
	}

	nrg.rg.startOffServiceLoops()

	r.logger.Debugf("Created new routes to %s on port %d", lPK, lPort)

	return nrg, nil
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

	nrg, err := r.saveRouteGroupRules(rules, nsConf)
	if err != nil {
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

func (r *router) saveRouteGroupRules(rules routing.EdgeRules, nsConf noise.Config) (*NoiseRouteGroup, error) {
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

	rg := NewRouteGroup(DefaultRouteGroupConfig(), r.rt, rules.Desc, r.mLogger)
	rg.appendRules(rules.Forward, rules.Reverse, r.tm.Transport(rules.Forward.NextTransportID()))
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

			return nil, fmt.Errorf("sendHandshake (%s): %w", &rules.Desc, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), handshakeAwaitTimeout)
	defer cancel()

	select {
	case <-rg.handshakeProcessed:
	case <-ctx.Done():
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
		b := int(packet[routing.PacketPayloadOffset])
		supportEncryptionVal := true
		if b == 0 {
			supportEncryptionVal = false
		}
		p = routing.MakeHandshakePacket(rule.NextRouteID(), supportEncryptionVal)
	case routing.KeepAlivePacket:
		p = routing.MakeKeepAlivePacket(rule.NextRouteID())
	case routing.ClosePacket:
		p = routing.MakeClosePacket(rule.NextRouteID(), routing.CloseCode(packet.Payload()[0]))
	case routing.PingPacket:
		timestamp := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset:]))
		throughput := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset+8:]))
		p = routing.MakePingPacket(rule.NextRouteID(), timestamp, throughput)
	case routing.PongPacket:
		timestamp := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset:]))
		p = routing.MakePongPacket(rule.NextRouteID(), timestamp)
	case routing.ErrorPacket:
		var err error

		p, err = routing.MakeErrorPacket(rule.NextRouteID(), packet.Payload())
		if err != nil {
			return err
		}
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

func (r *router) fetchBestRoutes(src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	// TODO: use opts
	if opts == nil {
		opts = DefaultDialOptions() // nolint
	}

	r.logger.Debugf("Requesting new routes from %s to %s", src, dst)

	timer := time.NewTimer(retryDuration)
	defer timer.Stop()

	forward := [2]cipher.PubKey{src, dst}
	backward := [2]cipher.PubKey{dst, src}

fetchRoutesAgain:
	ctx := context.Background()

	paths, err := r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward, backward},
		&rfclient.RouteOptions{MinHops: r.conf.MinHops, MaxHops: r.conf.MaxHops})

	if err == rfclient.ErrTransportNotFound {
		return nil, nil, err
	}

	if err != nil {
		select {
		case <-timer.C:
			return nil, nil, err
		default:
			time.Sleep(retryInterval)
			goto fetchRoutesAgain
		}
	}

	r.logger.Debugf("Found routes Forward: %s. Reverse %s", paths[forward], paths[backward])

	return paths[forward][0], paths[backward][0], nil
}

func (r *router) fetchPingRoute(src, pingKey cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	// TODO: use opts
	if opts == nil {
		opts = DefaultDialOptions() // nolint
	}

	r.logger.Debugf("Requesting new routes from %s to %s", src, src)

	timer := time.NewTimer(retryDuration)
	defer timer.Stop()

	forward := [2]cipher.PubKey{src, src}
	backward := [2]cipher.PubKey{src, src}

fetchRoutesAgain:
	ctx := context.Background()

	paths, err := r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward, backward},
		&rfclient.RouteOptions{MinHops: 0, MaxHops: 2})
	if err == rfclient.ErrTransportNotFound {
		return nil, nil, err
	}

	if err != nil {
		select {
		case <-timer.C:
			return nil, nil, err
		default:
			time.Sleep(retryInterval)
			goto fetchRoutesAgain
		}
	}

	var hopTo, hopFrom bool
	// check if the remote pk is present in both the hops
	// [
	// 	{
	// 		"TpID":"<tp-id>",
	// 		"From":"<local-pk>",
	// 		"To":"<remote-pk>"
	// 	},
	// 	{
	// 		"TpID":"<tp-id>",
	// 		"From":"<remote-pk>",
	// 		"To":"<local-pk>"
	// 	}
	// ]
	for _, hop := range paths[forward][0] {
		if hop.To == pingKey {
			hopTo = true
		}
		if hop.From == pingKey {
			hopFrom = true
		}
	}
	if !hopTo && hopFrom {
		return nil, nil, fmt.Errorf("Unable to fetch route with a hop from %v", pingKey)
	}

	r.logger.Debugf("Found routes Forward: %s. Reverse %s", paths[forward], paths[backward])

	return paths[forward][0], paths[backward][0], nil
}

// SetupIsTrusted checks if setup node is trusted.
func (r *router) SetupIsTrusted(sPK cipher.PubKey) bool {
	_, ok := r.trustedVisors[sPK]
	return ok
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

func (r *router) removeNoiseRouteGroup(desc routing.RouteDescriptor) {
	r.mx.Lock()
	defer r.mx.Unlock()

	delete(r.rgsNs, desc)
}

func (r *router) IntroduceRules(rules routing.EdgeRules) error {
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
		Debug("Closing noise route group associated with rule...")

	nrg, ok := r.popNoiseRouteGroup(rDesc)
	if !ok {
		log.Debug("No noise route group associated with expired rule. Nothing to be done.")
		return
	}
	if nrg.isClosed() {
		log.Debug("Noise route group already closed. Nothing to be done.")
		return
	}
	if err := nrg.Close(); err != nil {
		log.WithError(err).Error("Failed to close noise route group.")
		return
	}
	log.Debug("Noise route group closed.")
}

func (r *router) checkIfTransportAvailable() (ok bool) {
	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		ok = true
		return ok
	})
	return ok
}
