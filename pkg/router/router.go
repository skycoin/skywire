// Package router implements package router for skywire visor.
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"sync"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/setup/setupclient"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/snet"
	"github.com/skycoin/skywire/pkg/transport"
)

//go:generate mockery -name Router -case underscore -inpkg

const (
	// DefaultRouteKeepAlive is the default expiration interval for routes
	DefaultRouteKeepAlive = 30 * time.Second
	// DefaultRulesGCInterval is the default duration for garbage collection of routing rules.
	DefaultRulesGCInterval = 5 * time.Second
	acceptSize             = 1024

	minHops = 0
	maxHops = 50
)

var (
	// ErrUnknownPacketType is returned when packet type is unknown.
	ErrUnknownPacketType = errors.New("unknown packet type")

	// ErrRemoteEmptyPK occurs when the specified remote public key is empty.
	ErrRemoteEmptyPK = errors.New("empty remote public key")
)

// Config configures Router.
type Config struct {
	Logger           *logging.Logger
	PubKey           cipher.PubKey
	SecKey           cipher.SecKey
	TransportManager *transport.Manager
	RouteFinder      rfclient.Client
	RouteGroupDialer setupclient.RouteGroupDialer
	SetupNodes       []cipher.PubKey
	RulesGCInterval  time.Duration
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
	DialRoutes(ctx context.Context, rPK cipher.PubKey, lPort, rPort routing.Port, opts *DialOptions) (*RouteGroup, error)

	// AcceptRoutes should block until we receive an AddRules packet from SetupNode
	// that contains ConsumeRule(s) or ForwardRule(s).
	// Then the following should happen:
	// - Save to routing.Table and internal RouteGroup map.
	// - Return the RoutingGroup.
	AcceptRoutes(context.Context) (*RouteGroup, error)
	SaveRoutingRules(rules ...routing.Rule) error
	ReserveKeys(n int) ([]routing.RouteID, error)
	IntroduceRules(rules routing.EdgeRules) error
	Serve(context.Context) error
	SetupIsTrusted(cipher.PubKey) bool

	// routing table related methods
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
	mx            sync.Mutex
	conf          *Config
	logger        *logging.Logger
	n             *snet.Network
	sl            *snet.Listener
	trustedVisors map[cipher.PubKey]struct{}
	tm            *transport.Manager
	rt            routing.Table
	rfc           rfclient.Client                         // route finder client
	rgs           map[routing.RouteDescriptor]*RouteGroup // route groups to push incoming reads from transports.
	rpcSrv        *rpc.Server
	accept        chan routing.EdgeRules
	done          chan struct{}
	wg            sync.WaitGroup
	once          sync.Once
}

// New constructs a new Router.
func New(n *snet.Network, config *Config) (Router, error) {
	config.SetDefaults()

	sl, err := n.Listen(snet.DmsgType, skyenv.DmsgAwaitSetupPort)
	if err != nil {
		return nil, err
	}

	trustedVisors := make(map[cipher.PubKey]struct{})
	for _, node := range config.SetupNodes {
		trustedVisors[node] = struct{}{}
	}

	r := &router{
		conf:          config,
		logger:        config.Logger,
		n:             n,
		tm:            config.TransportManager,
		rt:            routing.NewTable(),
		sl:            sl,
		rfc:           config.RouteFinder,
		rgs:           make(map[routing.RouteDescriptor]*RouteGroup),
		rpcSrv:        rpc.NewServer(),
		accept:        make(chan routing.EdgeRules, acceptSize),
		done:          make(chan struct{}),
		trustedVisors: trustedVisors,
	}

	go r.rulesGCLoop()

	if err := r.rpcSrv.Register(NewRPCGateway(r)); err != nil {
		return nil, fmt.Errorf("failed to register RPC server")
	}

	return r, nil
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
) (*RouteGroup, error) {

	if rPK.Null() {
		err := ErrRemoteEmptyPK
		r.logger.WithError(err).Error("Failed to dial routes.")
		return nil, fmt.Errorf("failed to dial routes: %v", err)
	}

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, lPort, rPort)

	forwardPath, reversePath, err := r.fetchBestRoutes(lPK, rPK, opts)
	if err != nil {
		return nil, fmt.Errorf("route finder: %s", err)
	}

	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: DefaultRouteKeepAlive,
		Forward:   forwardPath,
		Reverse:   reversePath,
	}

	rules, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.n, r.conf.SetupNodes, req)
	if err != nil {
		r.logger.WithError(err).Error("Error dialing route group")
		return nil, err
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		r.logger.WithError(err).Error("Error saving routing rules")
		return nil, err
	}

	rg := r.saveRouteGroupRules(rules)

	r.logger.Infof("Created new routes to %s on port %d", rPK, lPort)

	return rg, nil
}

// AcceptsRoutes should block until we receive an AddRules packet from SetupNode
// that contains ConsumeRule(s) or ForwardRule(s).
// Then the following should happen:
// - Save to routing.Table and internal RouteGroup map.
// - Return the RoutingGroup.
func (r *router) AcceptRoutes(ctx context.Context) (*RouteGroup, error) {
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
		return nil, err
	}

	rg := r.saveRouteGroupRules(rules)

	return rg, nil
}

// Serve starts transport listening loop.
func (r *router) Serve(ctx context.Context) error {
	r.logger.Info("Starting router")

	go r.serveTransportManager(ctx)

	r.wg.Add(1)

	go func() {
		defer r.wg.Done()
		r.serveSetup()
	}()

	r.tm.Serve(ctx)

	return nil
}

func (r *router) serveTransportManager(ctx context.Context) {
	for {
		packet, err := r.tm.ReadPacket()
		if err != nil {
			if err == transport.ErrNotServing {
				r.logger.WithError(err).Info("Stopped reading packets")
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
		conn, err := r.sl.AcceptConn()
		if err != nil {
			log := r.logger.WithError(err)
			if err == dmsg.ErrEntityClosed {
				log.Info("Setup client stopped serving.")
			} else {
				log.Error("Setup client stopped serving due to unexpected error.")
			}
			return
		}

		if !r.SetupIsTrusted(conn.RemotePK()) {
			r.logger.Warnf("closing conn from untrusted setup node: %v", conn.Close())
			continue
		}

		r.logger.Infof("handling setup request: setupPK(%s)", conn.RemotePK())

		go r.rpcSrv.ServeConn(conn)
	}
}

func (r *router) saveRouteGroupRules(rules routing.EdgeRules) *RouteGroup {
	r.logger.Infof("Saving route group rules with desc: %s", &rules.Desc)
	r.mx.Lock()
	defer r.mx.Unlock()

	rg, ok := r.rgs[rules.Desc]
	if ok && rg != nil {
		r.logger.Infof("Route group with desc %s already exists, closing the old one and replacing...", &rules.Desc)

		if err := rg.Close(); err != nil {
			r.logger.Errorf("Error closing already existing route group: %v", err)
		}

		r.logger.Infoln("Successfully closed old route group")
	}

	r.logger.Infof("Creating new route group rule with desc: %s", &rules.Desc)

	rg = NewRouteGroup(DefaultRouteGroupConfig(), r.rt, rules.Desc)
	r.rgs[rules.Desc] = rg

	rg.fwd = append(rg.fwd, rules.Forward)
	rg.rvs = append(rg.rvs, rules.Reverse)

	tp := r.tm.Transport(rules.Forward.NextTransportID())
	rg.tps = append(rg.tps, tp)

	return rg
}

func (r *router) handleTransportPacket(ctx context.Context, packet routing.Packet) error {
	switch packet.Type() {
	case routing.DataPacket:
		return r.handleDataPacket(ctx, packet)
	case routing.ClosePacket:
		return r.handleClosePacket(ctx, packet)
	case routing.KeepAlivePacket:
		return r.handleKeepAlivePacket(ctx, packet)
	default:
		return ErrUnknownPacketType
	}
}

func (r *router) handleDataPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}

	if rule.Type() == routing.RuleConsume {
		r.logger.Debugf("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())
	} else {
		r.logger.Debugf("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
	}

	switch rule.Type() {
	case routing.RuleForward, routing.RuleIntermediaryForward:
		r.logger.Infoln("Handling intermediary data packet")
		return r.forwardPacket(ctx, packet, rule)
	}

	desc := rule.RouteDescriptor()
	rg, ok := r.routeGroup(desc)

	r.logger.Infof("Handling packet with descriptor %s", &desc)

	if !ok {
		r.logger.Infof("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("RouteGroup is nil")
	}

	r.logger.Infof("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
}

func (r *router) handleClosePacket(ctx context.Context, packet routing.Packet) error {
	routeID := packet.RouteID()

	r.logger.Infof("Received close packet for route ID %v", routeID)

	rule, err := r.GetRule(routeID)
	if err != nil {
		return err
	}

	if rule.Type() == routing.RuleConsume {
		r.logger.Debugf("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())
	} else {
		r.logger.Debugf("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
	}

	defer func() {
		routeIDs := []routing.RouteID{routeID}
		r.rt.DelRules(routeIDs)
	}()

	if t := rule.Type(); t == routing.RuleIntermediaryForward {
		r.logger.Infoln("Handling intermediary close packet")
		return r.forwardPacket(ctx, packet, rule)
	}

	desc := rule.RouteDescriptor()
	rg, ok := r.routeGroup(desc)

	r.logger.Infof("Handling close packet with descriptor %s", &desc)

	if !ok {
		r.logger.Infof("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	defer r.removeRouteGroup(desc)

	if rg == nil {
		return errors.New("RouteGroup is nil")
	}

	r.logger.Infof("Got new remote close packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	closeCode := routing.CloseCode(packet.Payload()[0])

	if rg.isClosed() {
		return io.ErrClosedPipe
	}

	if err := rg.handlePacket(packet); err != nil {
		return fmt.Errorf("error handling close packet with code %d by route group with descriptor %s: %v",
			closeCode, &desc, err)
	}

	return nil
}

func (r *router) handleKeepAlivePacket(ctx context.Context, packet routing.Packet) error {
	routeID := packet.RouteID()

	r.logger.Infof("Received keepalive packet for route ID %v", routeID)

	rule, err := r.GetRule(routeID)
	if err != nil {
		return err
	}

	if rule.Type() == routing.RuleConsume {
		r.logger.Debugf("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())
	} else {
		r.logger.Debugf("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
	}

	// propagate packet only for intermediary rule. forward rule workflow doesn't get here,
	// consume rules should be omitted, activity is already updated
	if t := rule.Type(); t == routing.RuleIntermediaryForward {
		r.logger.Infoln("Handling intermediary keep-alive packet")
		return r.forwardPacket(ctx, packet, rule)
	}

	r.logger.Infof("Route ID %v found, updated activity", routeID)

	return nil
}

// GetRule gets routing rule.
func (r *router) GetRule(routeID routing.RouteID) (routing.Rule, error) {
	rule, err := r.rt.Rule(routeID)
	if err != nil {
		return nil, fmt.Errorf("routing table: %s", err)
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
		return fmt.Errorf("error updating activity for route ID %d: %v", routeID, err)
	}

	return nil
}

// Close safely stops Router.
func (r *router) Close() error {
	if r == nil {
		return nil
	}

	r.logger.Info("Closing all App connections and RouteGroups")

	r.once.Do(func() {
		close(r.done)

		r.mx.Lock()
		close(r.accept)
		r.mx.Unlock()
	})

	if err := r.sl.Close(); err != nil {
		r.logger.WithError(err).Warnf("closing route_manager returned error")
	}

	r.wg.Wait()

	return r.tm.Close()
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
	case routing.KeepAlivePacket:
		p = routing.MakeKeepAlivePacket(rule.NextRouteID())
	case routing.ClosePacket:
		p = routing.MakeClosePacket(rule.NextRouteID(), routing.CloseCode(packet.Payload()[0]))
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

	r.logger.Infof("Forwarded packet via Transport %s using rule %d", rule.NextTransportID(), rule.KeyRouteID())

	return nil
}

// RemoveRouteDescriptor removes route group rule.
func (r *router) RemoveRouteDescriptor(desc routing.RouteDescriptor) {
	rules := r.rt.AllRules()
	for _, rule := range rules {
		if rule.Type() != routing.RuleConsume {
			continue
		}

		rd := rule.RouteDescriptor()
		if rd.DstPK() == desc.DstPK() && rd.DstPort() == desc.DstPort() && rd.SrcPort() == desc.SrcPort() {
			r.rt.DelRules([]routing.RouteID{rule.KeyRouteID()})
			return
		}
	}
}

func (r *router) fetchBestRoutes(src, dst cipher.PubKey, opts *DialOptions) (fwd, rev routing.Path, err error) {
	// TODO(nkryuchkov): use opts
	if opts == nil {
		opts = DefaultDialOptions() // nolint
	}

	r.logger.Infof("Requesting new routes from %s to %s", src, dst)

	timer := time.NewTimer(time.Second * 10)
	defer timer.Stop()

	forward := [2]cipher.PubKey{src, dst}
	backward := [2]cipher.PubKey{dst, src}

fetchRoutesAgain:
	ctx := context.Background()

	paths, err := r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward, backward},
		&rfclient.RouteOptions{MinHops: minHops, MaxHops: maxHops})

	if err != nil {
		select {
		case <-timer.C:
			return nil, nil, err
		default:
			goto fetchRoutesAgain
		}
	}

	r.logger.Infof("Found routes Forward: %s. Reverse %s", paths[forward], paths[backward])

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
			return fmt.Errorf("routing table: %s", err)
		}

		r.logger.Infof("Save new Routing Rule with ID %d %s", rule.KeyRouteID(), rule)
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

func (r *router) popRouteGroup(desc routing.RouteDescriptor) (*RouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	rg, ok := r.rgs[desc]
	if !ok {
		return nil, false
	}

	delete(r.rgs, desc)

	return rg, true
}

func (r *router) routeGroup(desc routing.RouteDescriptor) (*RouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	rg, ok := r.rgs[desc]

	return rg, ok
}

func (r *router) removeRouteGroup(desc routing.RouteDescriptor) {
	r.mx.Lock()
	defer r.mx.Unlock()

	delete(r.rgs, desc)
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
	log.WithField("rules_count", len(removedRules)).
		Debug("Removed rules.")

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
	if rule.Type() != routing.RuleConsume {
		log.
			WithField("func", "removeRouteGroupOfRule").
			WithField("rule", rule.Type().String()).
			Debug("Nothing to be done")

		return
	}

	rDesc := rule.RouteDescriptor()
	log.WithField("rt_desc", rDesc.String()).
		Debug("Closing route group associated with rule...")

	rg, ok := r.popRouteGroup(rDesc)
	if !ok {
		log.Debug("No route group associated with expired rule. Nothing to be done.")
		return
	}
	if rg.isClosed() {
		log.Debug("Route group already closed. Nothing to be done.")
		return
	}
	if err := rg.Close(); err != nil {
		log.WithError(err).Error("Failed to close route group.")
		return
	}
	log.Debug("Route group closed.")
}
