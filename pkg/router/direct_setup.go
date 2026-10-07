// Package router pkg/router/direct_setup.go c2-net-routing
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	rpc "github.com/0magnet/gobrpc"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
)

// A route between two visors with a transport between them needs no setup
// node. The source reserves its own route IDs, asks the destination for its
// two over the destination's setup port, and installs the destination's edge
// rules there itself. The destination accepts that only from a peer it has a
// transport with, only for a route from that peer to itself over such a
// transport, and nothing that would make it a transit hop.

// maxDirectSetupIDs bounds the route IDs one direct setup may reserve; a
// one-hop route needs two on each side.
const maxDirectSetupIDs = 4

// directSetupUnsupportedFor is how long a peer that refused a direct setup
// is sent to the setup node instead, until it runs a version that accepts.
const directSetupUnsupportedFor = 30 * time.Minute

var (
	errDirectSetupUsed     = errors.New("direct setup: one reservation and one install per connection")
	errDirectSetupTooMany  = errors.New("direct setup: too many route IDs")
	errDirectSetupNotOurs  = errors.New("direct setup: route is not between the caller and this visor")
	errDirectSetupNoDirect = errors.New("direct setup: rule does not leave over a transport to the caller")
)

// directSetupGateway is the setup RPC a direct peer gets: reserve IDs and
// install the edge rules of a one-hop route between it and this visor.
type directSetupGateway struct {
	r      *router
	caller cipher.PubKey
	log    *logging.Logger

	mu        sync.Mutex
	reserved  bool
	installed bool
}

// ReserveIDs reserves at most maxDirectSetupIDs route IDs, once.
func (g *directSetupGateway) ReserveIDs(n uint8, routeIDs *[]routing.RouteID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reserved {
		return routing.Failure{Code: routing.FailureReserveRtIDs, Msg: errDirectSetupUsed.Error()}
	}
	if n == 0 || n > maxDirectSetupIDs {
		return routing.Failure{Code: routing.FailureReserveRtIDs, Msg: errDirectSetupTooMany.Error()}
	}
	g.reserved = true
	ids, err := g.r.ReserveKeys(int(n))
	if err != nil {
		return routing.Failure{Code: routing.FailureReserveRtIDs, Msg: err.Error()}
	}
	*routeIDs = ids
	return nil
}

// AddEdgeRules installs this visor's edge of a one-hop route from the caller.
func (g *directSetupGateway) AddEdgeRules(rules routing.EdgeRules, ok *bool) error {
	*ok = false
	g.mu.Lock()
	used := g.installed
	g.installed = true
	g.mu.Unlock()
	if used || !g.reserved {
		return routing.Failure{Code: routing.FailureAddRules, Msg: errDirectSetupUsed.Error()}
	}
	if err := g.r.checkDirectEdgeRules(g.caller, rules); err != nil {
		g.log.WithError(err).WithField("caller", g.caller).Debug("Refused direct route setup")
		return routing.Failure{Code: routing.FailureAddRules, Msg: err.Error()}
	}
	if err := g.r.IntroduceRules(rules); err != nil {
		return routing.Failure{Code: routing.FailureAddRules, Msg: err.Error()}
	}
	*ok = true
	return nil
}

// checkDirectEdgeRules accepts the responding edge of a route from caller to
// this visor whose forward rule leaves over a live transport to caller.
func (r *router) checkDirectEdgeRules(caller cipher.PubKey, rules routing.EdgeRules) error {
	if err := rules.Validate(); err != nil {
		return err
	}
	if rules.Desc.SrcPK() != caller || rules.Desc.DstPK() != r.conf.PubKey {
		return errDirectSetupNotOurs
	}
	if rules.Forward.Type() != routing.RuleForward || rules.Reverse.Type() != routing.RuleReverse {
		return errDirectSetupNotOurs
	}
	if !r.transportTo(rules.Forward.NextTransportID(), caller) {
		return errDirectSetupNoDirect
	}
	return nil
}

// directSetupPeer reports whether pk may set up one-hop routes to this visor
// without a setup node: it has a live transport to this visor.
func (r *router) directSetupPeer(pk cipher.PubKey) bool {
	if r.tm == nil {
		return false
	}
	found := false
	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp != nil && !tp.IsClosed() && tp.Entry.RemoteEdge(r.conf.PubKey) == pk {
			found = true
			return false
		}
		return true
	})
	return found
}

// transportTo reports whether id is a live transport between this visor and pk.
func (r *router) transportTo(id uuid.UUID, pk cipher.PubKey) bool {
	if r.tm == nil {
		return false
	}
	tp := r.tm.Transport(id)
	return tp != nil && !tp.IsClosed() && tp.Entry.RemoteEdge(r.conf.PubKey) == pk
}

// serveDirectSetup serves one direct peer's setup connection.
func (r *router) serveDirectSetup(conn *dmsg.Stream, caller cipher.PubKey) {
	defer func() {
		if rec := recover(); rec != nil {
			logging.LogRecovered(r.logger, "direct setup handler", rec)
		}
		conn.Close() //nolint:errcheck,gosec
	}()
	conn.SetDeadline(time.Now().Add(time.Minute)) //nolint:errcheck,gosec
	srv := rpc.NewServer()
	gw := &directSetupGateway{r: r, caller: caller, log: r.logger}
	if err := registerDirectSetupRPC(srv, gw); err != nil {
		r.logger.WithError(err).Warn("Could not serve direct route setup")
		return
	}
	srv.ServeConn(conn)
}

// oneHopRoute reports whether req is a route between two visors over one
// transport each way.
func oneHopRoute(req routing.BidirectionalRoute) bool {
	return len(req.Forward) == 1 && len(req.Reverse) == 1
}

// directSetupState remembers peers that refused a direct setup.
type directSetupState struct {
	mu          sync.Mutex
	unsupported map[cipher.PubKey]time.Time
}

func (d *directSetupState) supported(pk cipher.PubKey) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	until, ok := d.unsupported[pk]
	if ok && time.Now().After(until) {
		delete(d.unsupported, pk)
		return true
	}
	return !ok
}

func (d *directSetupState) markUnsupported(pk cipher.PubKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.unsupported == nil {
		d.unsupported = map[cipher.PubKey]time.Time{}
	}
	d.unsupported[pk] = time.Now().Add(directSetupUnsupportedFor)
}

// refusedDirectSetup reports whether err is a peer closing the setup
// connection unanswered, which is what a version without direct setup does.
func refusedDirectSetup(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "connection is shut down")
}

// dialRouteGroup sets up a route group: a one-hop route with its destination
// directly, anything longer, or a peer that cannot, through a setup node.
func (r *router) dialRouteGroup(ctx context.Context, log *logging.Logger, req routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
	dst := req.Desc.DstPK()
	if r.dmsgC != nil && oneHopRoute(req) && r.directSetup.supported(dst) {
		rules, err := r.setupDirect(ctx, r.dmsgC, req)
		if err == nil {
			r.routeSource.directSetups.Add(1)
			return rules, r.conf.PubKey, nil
		}
		if ctx.Err() != nil {
			return routing.EdgeRules{}, cipher.PubKey{}, err
		}
		r.routeSource.directSetupFallbacks.Add(1)
		if refusedDirectSetup(err) {
			r.directSetup.markUnsupported(dst)
		}
		log.WithError(err).WithField("remote", dst).Debug("Direct route setup failed; using a setup node")
	}
	return r.conf.RouteGroupDialer.Dial(ctx, log, r.dmsgC, r.conf.SetupNodes, req)
}

// setupDirect builds a one-hop route with its destination, as a setup node
// would: two route IDs from each side, the rules from GenerateRules, and the
// destination's edge installed on it. It returns this visor's edge.
func (r *router) setupDirect(ctx context.Context, dmsgC *dmsg.Client, req routing.BidirectionalRoute) (routing.EdgeRules, error) {
	if err := req.Check(); err != nil {
		return routing.EdgeRules{}, err
	}
	src, dst := req.Desc.SrcPK(), req.Desc.DstPK()
	if src != r.conf.PubKey {
		return routing.EdgeRules{}, errDirectSetupNotOurs
	}
	dialCtx, cancel := context.WithTimeout(ctx, DialTimeout)
	str, err := dmsgC.DialStream(dialCtx, dmsg.Addr{PK: dst, Port: skyenv.DmsgAwaitSetupPort})
	cancel()
	if err != nil {
		return routing.EdgeRules{}, &DialError{PK: dst, Err: err}
	}
	c := NewClientFromRaw(str, dst)
	defer c.Close() //nolint:errcheck
	return r.setupDirectWith(ctx, c, req)
}

// setupDirectWith runs a direct setup over an open setup connection to the
// route's destination.
func (r *router) setupDirectWith(ctx context.Context, c *Client, req routing.BidirectionalRoute) (routing.EdgeRules, error) {
	src, dst := req.Desc.SrcPK(), req.Desc.DstPK()
	remote, err := c.ReserveIDs(ctx, 2)
	if err != nil {
		return routing.EdgeRules{}, &ReserveError{PK: dst, Err: err}
	}
	local, err := r.ReserveKeys(2)
	if err != nil {
		return routing.EdgeRules{}, err
	}
	ids := &fixedIDs{ids: map[cipher.PubKey][]routing.RouteID{src: local, dst: remote}}
	fwdRt, revRt := req.ForwardAndReverse()
	fwdRules, revRules, _, err := GenerateRules(ids, []routing.Route{fwdRt, revRt})
	if err != nil {
		return routing.EdgeRules{}, err
	}
	initEdge := routing.EdgeRules{Desc: revRt.Desc, Forward: fwdRules[req.Desc.Src().String()][0], Reverse: revRules[req.Desc.Src().String()][0]}
	respEdge := routing.EdgeRules{Desc: fwdRt.Desc, Forward: fwdRules[req.Desc.Dst().String()][0], Reverse: revRules[req.Desc.Dst().String()][0]}
	ok, err := c.AddEdgeRules(ctx, respEdge)
	if err != nil || !ok {
		return routing.EdgeRules{}, fmt.Errorf("direct setup: destination refused its edge rules: %v", err)
	}
	return initEdge, nil
}

// fixedIDs is an IDReserver over route IDs already reserved.
type fixedIDs struct {
	ids map[cipher.PubKey][]routing.RouteID
}

func (f *fixedIDs) Close() error                     { return nil }
func (f *fixedIDs) String() string                   { return "direct setup ids" }
func (f *fixedIDs) ReserveIDs(context.Context) error { return nil }
func (f *fixedIDs) Client(cipher.PubKey) *Client     { return nil }
func (f *fixedIDs) ReturnToPool(*ClientPool)         {}
func (f *fixedIDs) TotalIDs() int                    { return len(f.ids) }
func (f *fixedIDs) PopID(pk cipher.PubKey) (routing.RouteID, bool) {
	ids := f.ids[pk]
	if len(ids) == 0 {
		return 0, false
	}
	f.ids[pk] = ids[1:]
	return ids[0], true
}
