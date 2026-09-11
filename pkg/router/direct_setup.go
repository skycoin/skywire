//go:build !tinygo || (js && wasm)

// Package router pkg/router/direct_setup.go c2-net-routing
//
// Route setup for a 1-hop route over a transport the two visors already
// share, with no setup node, no dmsg and no address resolver involved.
//
// A --direct dial over an existing transport already skips the route finder
// (#4552): the route it would return IS the transport being held. Setup was
// still routed through the RSN, which reserved the destination's route IDs by
// dialing it at dmsg port 136 — so a direct transport was never self-
// sufficient. If the destination was not reachable over dmsg the dial failed
// even though a perfectly good transport to it was open, and the resulting
// "cannot connect to delegated server" read as a problem with the delegated
// servers rather than with dmsg reachability of the peer.
//
// For one hop there are no intermediaries, so the RSN has nothing to do that
// the two endpoints cannot do between themselves: the source reserves its own
// IDs locally, asks the peer for its IDs, and hands the peer its edge rules —
// all over a SetupRPC virtual stream carried on the shared transport itself
// (route ID 0 packets, the same mechanism the RSN relay and the DHT use).
//
// AUTHORIZATION. The dmsg setup listener accepts rules only from a trusted
// setup node (Router.SetupIsTrusted). This path has no setup node, so the
// peer on the other end of the transport is the authority — and it is only
// allowed to ask for what it could already have. directSetupGateway enforces:
//
//   - only ReserveIDs and AddEdgeRules exist; AddIntermediaryRules does not,
//     so a peer can never make this visor a transit hop for a third party;
//   - the descriptor must name exactly this visor and that peer, so a peer
//     cannot install rules for a route this visor is not an endpoint of;
//   - the forward rule's next transport must be the transport the request
//     arrived on, so a peer cannot aim traffic down anyone else's transport.
//
// Within those bounds the peer gains nothing it did not already have: it holds
// a transport to this visor and can send it packets regardless. no_transit is
// unaffected — it refuses intermediary rules, which this path never carries.
package router

import (
	"context"
	"fmt"

	rpc "github.com/0magnet/gobrpc"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// DirectSetupRPCName is the RPC service name served to transport peers on an
// accepted SetupRPC virtual stream. It deliberately differs from RPCName (the
// full setup gateway reached over dmsg from a trusted setup node) so the two
// authorization domains can never be confused for one another.
const DirectSetupRPCName = "DirectSetupGateway"

// setupRPCMuxProvider is implemented by the route-group dialer that owns the
// visor's single SetupRPC VStreamMux. Only one handler can be registered on
// the transport manager for a packet type, so the inbound accept loop shares
// the mux the dialer already created rather than making a second one.
type setupRPCMuxProvider interface {
	SetupRPCMux() *transport.VStreamMux
}

// directSetupGateway is the restricted RPC gateway served to a transport peer.
// See the package comment for what it will and will not accept, and why that
// grants the peer nothing new.
type directSetupGateway struct {
	logger *logging.Logger
	router Router
	local  cipher.PubKey
	peer   cipher.PubKey
	tpID   uuid.UUID
}

// ReserveIDs reserves route IDs for the peer's 1-hop route. Bounded to the
// four a bidirectional 1-hop route can possibly need, so a peer cannot drain
// this visor's route-ID space by asking for a large count.
func (g *directSetupGateway) ReserveIDs(n uint8, routeIDs *[]routing.RouteID) error {
	const maxDirectIDs = 4
	if n == 0 || n > maxDirectIDs {
		return routing.Failure{
			Code: routing.FailureReserveRtIDs,
			Msg:  fmt.Sprintf("direct setup: refusing to reserve %d ids (max %d)", n, maxDirectIDs),
		}
	}
	ids, err := g.router.ReserveKeys(int(n))
	if err != nil {
		g.logger.WithError(err).Warn("direct setup: ReserveIDs failed")
		return routing.Failure{Code: routing.FailureReserveRtIDs, Msg: err.Error()}
	}
	*routeIDs = ids
	return nil
}

// AddEdgeRules installs the peer's edge rules after checking they describe a
// route between exactly these two visors over exactly this transport.
func (g *directSetupGateway) AddEdgeRules(rules routing.EdgeRules, ok *bool) error {
	if err := g.validate(rules); err != nil {
		*ok = false
		g.logger.WithError(err).WithField("peer", g.peer).
			Warn("direct setup: refusing edge rules")
		return routing.Failure{Code: routing.FailureAddRules, Msg: err.Error()}
	}
	if err := g.router.IntroduceRules(rules); err != nil {
		*ok = false
		g.logger.WithError(err).Warn("direct setup: IntroduceRules failed")
		return routing.Failure{Code: routing.FailureAddRules, Msg: err.Error()}
	}
	*ok = true
	return nil
}

// validate is the whole authorization model; see the package comment.
func (g *directSetupGateway) validate(rules routing.EdgeRules) error {
	// The route must run between this visor and the peer that asked, in
	// either direction — never a route this visor is not an endpoint of.
	src, dst := rules.Desc.SrcPK(), rules.Desc.DstPK()
	if !((src == g.local && dst == g.peer) || (src == g.peer && dst == g.local)) {
		return fmt.Errorf("descriptor %s does not name this visor and the requesting peer", rules.Desc.String())
	}
	if rules.Forward == nil || rules.Reverse == nil {
		return fmt.Errorf("edge rules must carry both a forward and a reverse rule")
	}
	if t := rules.Forward.Type(); t != routing.RuleForward {
		return fmt.Errorf("forward rule is %v, want %v", t, routing.RuleForward)
	}
	if t := rules.Reverse.Type(); t != routing.RuleReverse {
		return fmt.Errorf("reverse rule is %v, want %v", t, routing.RuleReverse)
	}
	// The one that matters: traffic may only be sent back down the transport
	// the request arrived on. Without this a peer could point a forward rule
	// at a transport to a third party and use this visor as a transit hop
	// without ever installing an intermediary rule.
	if next := rules.Forward.NextTransportID(); next != g.tpID {
		return fmt.Errorf("forward rule's next transport %s is not the transport this request arrived on (%s)", next, g.tpID)
	}
	return nil
}

// serveDirectSetup accepts SetupRPC virtual streams from transport peers and
// serves the restricted gateway on each. It is the destination half of
// setupDirectRoute.
func (r *router) serveDirectSetup(ctx context.Context, mux *transport.VStreamMux) {
	log := r.logger
	for {
		stream, err := mux.Accept()
		if err != nil {
			if ctx.Err() != nil || r.routerDone() {
				return
			}
			log.WithError(err).Debug("direct setup: vstream accept stopped")
			return
		}
		go r.handleDirectSetup(stream)
	}
}

func (r *router) handleDirectSetup(stream *transport.VStream) {
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Errorf("Panic in direct setup handler: %v", rec)
		}
		stream.Close() //nolint:errcheck,gosec
	}()

	tpID, ok := r.transportWith(stream.RemotePK())
	if !ok {
		r.logger.WithField("peer", stream.RemotePK()).
			Debug("direct setup: no live transport with the requesting peer; refusing")
		return
	}

	gw := &directSetupGateway{
		logger: r.logger,
		router: r,
		local:  r.conf.PubKey,
		peer:   stream.RemotePK(),
		tpID:   tpID,
	}
	srv := rpc.NewServer()
	if err := srv.RegisterName(DirectSetupRPCName, gw); err != nil {
		r.logger.WithError(err).Error("direct setup: failed to register gateway")
		return
	}
	srv.ServeConn(stream)
}

// transportWith returns the id of a live transport to peer, if this visor has
// one. The stream arrived over a transport, so this is really a lookup of
// which one — it also rejects a peer whose transport went away mid-request.
func (r *router) transportWith(peer cipher.PubKey) (uuid.UUID, bool) {
	var found uuid.UUID
	var ok bool
	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp.Remote() == peer && !tp.IsClosed() {
			found, ok = tp.Entry.ID, true
			return false
		}
		return true
	})
	return found, ok
}

// staticIDReserver hands GenerateRules the IDs already reserved from each
// endpoint. Reusing GenerateRules rather than hand-rolling the two rules is
// deliberate: the rules a direct 1-hop route installs are then identical to
// the ones the setup node would have produced for the same route.
type staticIDReserver struct {
	ids map[cipher.PubKey][]routing.RouteID
}

func (s *staticIDReserver) PopID(pk cipher.PubKey) (routing.RouteID, bool) {
	stack := s.ids[pk]
	if len(stack) == 0 {
		return 0, false
	}
	s.ids[pk] = stack[1:]
	return stack[0], true
}

// setupDirectRoute installs a 1-hop route between this visor and the peer at
// the far end of the transport it runs over, without a setup node. It returns
// the initiating edge's rules exactly as RouteGroupDialer.Dial would.
//
// Any failure returns an error so the caller falls back to the setup node —
// this is an optimization of a working path, never a replacement that can
// strand a dial.
func (r *router) setupDirectRoute(ctx context.Context, log *logging.Logger, biRt routing.BidirectionalRoute) (routing.EdgeRules, error) {
	mux, tp, err := r.directSetupLeg(biRt)
	if err != nil {
		return routing.EdgeRules{}, err
	}

	srcPK, dstPK := biRt.Desc.SrcPK(), biRt.Desc.DstPK()

	// Our own IDs never leave this process.
	localIDs, err := r.ReserveKeys(2)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: reserve local ids: %w", err)
	}

	stream, err := mux.DialOnTransport(tp)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: vstream dial: %w", err)
	}
	defer stream.Close() //nolint:errcheck

	rpcC := rpc.NewClient(stream)
	defer rpcC.Close() //nolint:errcheck

	var remoteIDs []routing.RouteID
	if err := directCall(ctx, rpcC, DirectSetupRPCName+".ReserveIDs", uint8(2), &remoteIDs); err != nil {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: reserve peer ids: %w", err)
	}
	if len(remoteIDs) < 2 {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: peer reserved %d ids, want 2", len(remoteIDs))
	}

	fwdRt, revRt := biRt.ForwardAndReverse()
	idR := &staticIDReserver{ids: map[cipher.PubKey][]routing.RouteID{
		srcPK: localIDs,
		dstPK: remoteIDs,
	}}
	fwdRules, revRules, interRules, err := GenerateRules(idR, []routing.Route{fwdRt, revRt})
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: generate rules: %w", err)
	}
	// A 1-hop route has no intermediaries by construction. If any appeared,
	// the route is not what this path is for — bail to the setup node rather
	// than silently dropping rules the route needs.
	if len(interRules) != 0 {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: route unexpectedly has %d intermediary hop(s)", len(interRules))
	}
	if len(fwdRules[srcPK.String()]) == 0 || len(revRules[srcPK.String()]) == 0 ||
		len(fwdRules[dstPK.String()]) == 0 || len(revRules[dstPK.String()]) == 0 {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: generated rules are missing an edge")
	}

	initEdge := routing.EdgeRules{Desc: revRt.Desc, Forward: fwdRules[srcPK.String()][0], Reverse: revRules[srcPK.String()][0]}
	respEdge := routing.EdgeRules{Desc: fwdRt.Desc, Forward: fwdRules[dstPK.String()][0], Reverse: revRules[dstPK.String()][0]}

	var ok bool
	if err := directCall(ctx, rpcC, DirectSetupRPCName+".AddEdgeRules", respEdge, &ok); err != nil {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: install peer rules: %w", err)
	}
	if !ok {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: peer refused the edge rules")
	}

	log.WithField("remote", dstPK).WithField("transport", tp.Entry.ID).
		Debug("1-hop route set up over the transport itself; no setup node, no dmsg")

	return initEdge, nil
}

// directSetupLeg checks that biRt really is a single hop over one live
// transport to the destination, and returns what the setup needs.
func (r *router) directSetupLeg(biRt routing.BidirectionalRoute) (*transport.VStreamMux, *transport.ManagedTransport, error) {
	if len(biRt.Forward) != 1 || len(biRt.Reverse) != 1 {
		return nil, nil, fmt.Errorf("direct setup: route is %d/%d hops, not 1", len(biRt.Forward), len(biRt.Reverse))
	}
	fwd, rev := biRt.Forward[0], biRt.Reverse[0]
	if fwd.TpID != rev.TpID {
		return nil, nil, fmt.Errorf("direct setup: forward and reverse use different transports")
	}
	if fwd.From != biRt.Desc.SrcPK() || fwd.To != biRt.Desc.DstPK() {
		return nil, nil, fmt.Errorf("direct setup: hop does not match the route descriptor")
	}
	if r.tm == nil {
		return nil, nil, fmt.Errorf("direct setup: no transport manager")
	}
	provider, hasMux := r.conf.RouteGroupDialer.(setupRPCMuxProvider)
	if !hasMux {
		return nil, nil, fmt.Errorf("direct setup: route-group dialer has no SetupRPC mux")
	}
	mux := provider.SetupRPCMux()
	if mux == nil {
		return nil, nil, fmt.Errorf("direct setup: SetupRPC mux not initialized")
	}
	tp := r.tm.Transport(fwd.TpID)
	if tp == nil || tp.IsClosed() {
		return nil, nil, fmt.Errorf("direct setup: transport %s is not up", fwd.TpID)
	}
	if tp.Remote() != biRt.Desc.DstPK() {
		return nil, nil, fmt.Errorf("direct setup: transport %s does not lead to the destination", fwd.TpID)
	}
	return mux, tp, nil
}

// directCall issues one RPC bounded by ctx. gobrpc's synchronous Call has no
// context, and a peer that accepts the stream but never answers would
// otherwise hold the dial open until the transport itself fails.
func directCall(ctx context.Context, c *rpc.Client, method string, args, reply interface{}) error {
	call := c.Go(method, args, reply, nil)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-call.Done:
		return call.Error
	}
}

// routerDone reports whether the router has shut down.
func (r *router) routerDone() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// The rest of IDReserver is not meaningful for a static, already-reserved set
// of ids: GenerateRules only ever calls PopID. These satisfy the interface so
// the shared rule generator can be reused unchanged.
func (s *staticIDReserver) Close() error                       { return nil }
func (s *staticIDReserver) String() string                     { return fmt.Sprintf("staticIDReserver%v", s.ids) }
func (s *staticIDReserver) ReserveIDs(_ context.Context) error { return nil }
func (s *staticIDReserver) Client(_ cipher.PubKey) *Client     { return nil }
func (s *staticIDReserver) ReturnToPool(_ *ClientPool)         {}

func (s *staticIDReserver) TotalIDs() int {
	total := 0
	for _, stack := range s.ids {
		total += len(stack)
	}
	return total
}
