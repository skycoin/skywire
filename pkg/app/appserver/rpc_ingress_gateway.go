// Package appserver pkg/app/appserver/rpc_ingress_gateway.go c2-vis-appsvc
package appserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// dialSetupCeiling is the overall deadline for a single app Dial RPC's route
// setup. The router's DialRoutes bounds each individual setup attempt
// (routeSetupDialTimeout) and its own retry budget, but the dial context was
// previously context.Background() with NO deadline: a route-setup path that
// blocked indefinitely (a setup-node / cascade RPC that accepted but never
// replied, a destination that dropped the setup conn) left the app's Dial RPC
// — and its retrier — blocked inside that one call, so a dropped route wedged
// the app permanently in "starting" even with --reconnect. This ceiling
// guarantees the Dial RPC returns so the app's reconnect loop can re-try from
// scratch. It is generous enough to cover a slow multi-hop setup with retries;
// legitimate setups complete in seconds.
const dialSetupCeiling = 90 * time.Second

// RPCIOErr is used to return an error coming from network stack.
//
// Since client is implemented as an RPC client, we need to correctly
// pass all kinds of network errors from gateway back to the client.
// `net.Error` is an interface, so we can't pass it directly, we have to
// disassemble error on the server side and reassemble it back on the
// client side.
type RPCIOErr struct {
	Text           string
	IsNetErr       bool
	IsTimeoutErr   bool
	IsTemporaryErr bool
}

// ToError converts `*RPCIOErr` to `error`.
func (e *RPCIOErr) ToError() error {
	if e == nil {
		return nil
	}

	if !e.IsNetErr {
		switch e.Text {
		case io.EOF.Error():
			return io.EOF
		case io.ErrClosedPipe.Error():
			return io.ErrClosedPipe
		case io.ErrUnexpectedEOF.Error():
			return io.ErrUnexpectedEOF
		default:
			return errors.New(e.Text)
		}
	}

	return &netErr{
		err:       errors.New(e.Text),
		timeout:   e.IsTimeoutErr,
		temporary: e.IsTemporaryErr,
	}
}

// RPCIngressGateway is a RPC interface for the app server.
type RPCIngressGateway struct {
	proc *Proc
	lm   *idmanager.Manager // contains listeners associated with their IDs
	cm   *idmanager.Manager // contains connections associated with their IDs
	log  *logging.Logger
	// dialedPorts is the set of local route-group ports this app process was
	// handed by its own dials. It is the ownership proof NoteMuxEvent checks:
	// a tunnel event names a port, and the router's lookup is by port alone.
	// Kept past the conn's death on purpose (the retire event arrives after the
	// app closed the tunnel) and bounded by the 16-bit port space.
	dialedPorts   map[routing.Port]struct{}
	dialedPortsMx sync.Mutex
}

// NewRPCGateway constructs new server RPC interface.
func NewRPCGateway(log *logging.Logger, proc *Proc) *RPCIngressGateway {
	if log == nil {
		log = logging.MustGetLogger("app_rpc_ingress_gateway")
	}
	return &RPCIngressGateway{
		proc:        proc,
		lm:          idmanager.New(),
		cm:          idmanager.New(),
		log:         log,
		dialedPorts: make(map[routing.Port]struct{}),
	}
}

// SetDetailedStatus sets detailed status of an app.
func (r *RPCIngressGateway) SetDetailedStatus(status *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetDetailedStatus", status)(nil, &err)

	r.proc.SetDetailedStatus(*status)

	return nil
}

// SetOTP sets the current one-time code of an app that gates its own UI.
//
// Unlike its sibling calls, the value is deliberately withheld from
// rpcutil.LogCall (nil input) — it is a live credential, and app logs are
// retrievable over the hypervisor API.
func (r *RPCIngressGateway) SetOTP(otp *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetOTP", nil)(nil, &err)

	r.proc.SetOTP(*otp)

	return nil
}

// Notify publishes a user-facing notification to the visor's notification hub,
// which routes it to whatever sink can actually reach the user.
//
// Like SetOTP the payload is withheld from rpcutil.LogCall (nil input): a body
// is routinely UNTRUSTED peer text (a chat message), and app logs are
// retrievable over the hypervisor API.
//
// The App field is overwritten from this proc's own identity rather than
// trusted: the RPC service is registered per-proc and served on that proc's
// private conn, so the visor already knows the caller for certain. Without the
// override any app could publish under skychat's name — which on Android maps
// to a per-app notification channel the user may have deliberately muted.
func (r *RPCIngressGateway) Notify(req *NotifyReq, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "Notify", nil)(nil, &err)

	if req == nil {
		return errors.New("nil notification request")
	}
	// r.proc is nil in unit tests that construct a bare gateway; dropping is
	// the correct best-effort behavior there.
	if r.proc == nil {
		return nil
	}
	r.proc.Notify(*req)

	return nil
}

// SetConnectionDuration sets the connection duration of an app (vpn-client in this instance)
func (r *RPCIngressGateway) SetConnectionDuration(dur int64, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetConnectionDuration", dur)(nil, &err)
	r.proc.SetConnectionDuration(dur)
	return nil
}

// ProxyStatus returns the visor-built rich read-only status snapshot for the
// calling app (per-leg mux telemetry, recent logs, route/transport events). It
// lets an app that serves its own reserved status host — skysocks-client's
// status.skysocks — render the same rich view the visor-side resolving proxies
// show, instead of only what the app process itself can see. Read-only; an empty
// snapshot (the visor has no data, or the manager has no builder wired as on the
// browser wasm-visor) is returned WITHOUT error so the app degrades gracefully
// to its own local view.
func (r *RPCIngressGateway) ProxyStatus(_ *struct{}, resp *proxystatus.Snapshot) (err error) {
	defer rpcutil.LogCall(r.log, "ProxyStatus", nil)(nil, &err)
	// r.proc is nil in unit tests that construct a bare gateway.
	if r.proc == nil || r.proc.m == nil {
		return nil
	}
	if snap, ok := r.proc.m.ProxyStatus(r.proc.appName); ok {
		*resp = snap
	}
	return nil
}

// SetError sets error of an app.
func (r *RPCIngressGateway) SetError(appErr *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetError", appErr)(nil, &err)
	r.proc.SetError(*appErr)
	return nil
}

// SetAppPort sets the connection port of an app (vpn-client in this instance)
func (r *RPCIngressGateway) SetAppPort(port routing.Port, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppPort", port)(nil, &err)
	r.proc.SetAppPort(port)
	return nil
}

// NoteMuxEventReq is an app reporting a TUNNEL event — one of its tunnels
// promoted out of standby, parked back into it, or retired dead — onto the
// router's mux-event ring, and re-labeling that tunnel's role.
//
// LocalPort names the tunnel: it is the port the app was handed when it dialed
// (DialResp.LocalPort), which is the route group's source port, and the only
// identifier the app has for a route group it knows nothing else about. Event
// is one of router.MuxEventTunnel*; Reason is what decided it, in the words of
// the code that did ("failover: active tunnel died"). Role, when set, is the
// tunnel's role AFTER the event ("active" / "standby").
type NoteMuxEventReq struct {
	LocalPort routing.Port
	Event     string
	Reason    string
	Role      string
}

// Tunnel event and role vocabulary accepted from an app. The events mirror
// router.MuxEventTunnel* and the roles skysocks.TunnelRole* — named here as
// literals because pkg/skysocks imports this package, not the other way round.
//
// Anything outside these sets is rejected rather than stored: the strings land
// in the router's shared 256-entry mux-event ring and in `visor state`, so an
// app that could write arbitrary ones could both invent event kinds and, by
// looping, evict every other group's leg and wedge history.
const (
	maxMuxEventReasonLen = 256
	tunnelRoleActive     = "active"
	tunnelRoleStandby    = "standby"
)

// truncateMuxEventReason bounds the app-supplied reason kept in the router's
// mux-event ring. The ring never shrinks and lives as long as the visor, so an
// app could otherwise park hundreds of MB of strings in router memory.
func truncateMuxEventReason(reason string) string {
	if len(reason) > maxMuxEventReasonLen {
		return reason[:maxMuxEventReasonLen]
	}
	return reason
}

// validTunnelEvent reports whether event is one of the tunnel-level mux events
// an app is allowed to record.
func validTunnelEvent(event string) bool {
	switch event {
	case router.MuxEventTunnelPromoted, router.MuxEventTunnelParked, router.MuxEventTunnelRetired:
		return true
	default:
		return false
	}
}

// ownsLocalPort reports whether localPort belongs to THIS app process: either a
// conn the gateway still holds for it, or a port one of its own dials was
// handed. The ledger is what makes the retire case work — the app closes the
// tunnel's conn before it reports the death, so the conn is already gone from
// r.cm by the time the event arrives.
func (r *RPCIngressGateway) ownsLocalPort(localPort routing.Port) bool {
	if localPort == 0 {
		return false
	}
	r.dialedPortsMx.Lock()
	_, ok := r.dialedPorts[localPort]
	r.dialedPortsMx.Unlock()
	if ok {
		return true
	}
	r.cm.DoRange(func(_ uint16, v interface{}) bool {
		conn, isConn := v.(net.Conn)
		if !isConn || conn == nil {
			return true
		}
		addr, isAddr := conn.LocalAddr().(appnet.Addr)
		if isAddr && addr.Port == localPort {
			ok = true
			return false
		}
		return true
	})
	return ok
}

// noteDialedPort remembers a local port this app process dialed, so a tunnel
// event naming it later can be proven to be its own. The set is bounded by the
// port space and lives as long as the proc's gateway.
func (r *RPCIngressGateway) noteDialedPort(localPort routing.Port) {
	if localPort == 0 {
		return
	}
	r.dialedPortsMx.Lock()
	if r.dialedPorts == nil {
		r.dialedPorts = make(map[routing.Port]struct{})
	}
	r.dialedPorts[localPort] = struct{}{}
	r.dialedPortsMx.Unlock()
}

// NoteMuxEvent records an app-decided tunnel event on the route group the app
// dialed from req.LocalPort, and re-stamps that group's tunnel role.
//
// Only the app knows which of its tunnels carries streams; only the router
// knows the route behind it and owns the event ring `visor state --select
// diag` reads. This is the one seam between them. Unknown ports are not an
// error — a tunnel whose route group has already been reaped is exactly the
// case a retire event reports, and failing the call would turn a race into a
// log line in the app.
//
// The request is validated first, because the router's lookup is by PORT
// ALONE: NoteTunnelEvent matches any route group whose source or destination
// port equals it, across every app on this visor. Without the ownership check
// any app process could re-stamp another app's tunnel role and write events
// attributed to the victim's app name. Reason is truncated because it is
// stored verbatim in a ring that never shrinks; Event and Role need no
// truncation once they are whitelisted against a fixed vocabulary.
func (r *RPCIngressGateway) NoteMuxEvent(req *NoteMuxEventReq, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "NoteMuxEvent", req)(nil, &err)
	if req == nil {
		return errors.New("NoteMuxEvent: nil request")
	}
	if !validTunnelEvent(req.Event) {
		return fmt.Errorf("NoteMuxEvent: unknown event %q", req.Event)
	}
	if req.Role != "" && req.Role != tunnelRoleActive && req.Role != tunnelRoleStandby {
		return fmt.Errorf("NoteMuxEvent: unknown role %q", req.Role)
	}
	if !r.ownsLocalPort(req.LocalPort) {
		return fmt.Errorf("NoteMuxEvent: local port %d is not this app's", req.LocalPort)
	}
	reason := truncateMuxEventReason(req.Reason)
	nw, nerr := appnet.ResolveNetworker(appnet.TypeSkynet)
	if nerr != nil {
		return nerr
	}
	sw, ok := nw.(*appnet.SkywireNetworker)
	if !ok {
		// A test-mock or alternative networker has no route groups to note
		// anything on; mirrors dialWithMuxRoutes's degrade-quietly contract.
		return nil
	}
	rt := sw.Router()
	if rt == nil {
		return nil
	}
	rt.NoteTunnelEvent(req.LocalPort, req.Event, reason, req.Role)
	return nil
}

// DialResp contains response parameters for `Dial`.
type DialResp struct {
	ConnID    uint16
	LocalPort routing.Port
}

// DialOptionsReq carries a dial request with extra per-call options.
// Used by DialWithOptions for app paths that need to request specific
// router behavior. Zero / unset fields preserve the single-route,
// router-picks-freely default — equivalent to plain Dial.
//
// Added for #1525-adjacent skywire-cli mux-route testing per the
// operator's 2026-05-19 ask. Generalizes the per-call route count
// already plumbed through VisorCat (pkg/visor/api_visor_cat.go) into
// the app-net surface so apps like skynet-client can request N routes
// at dial time without needing a parallel "cli skynet cat"-style RPC.
//
// MinHops mirrors the same field on router.DialOptions and is needed
// to actually exercise the mux>direct hypothesis end-to-end via real
// data-plane apps: without it, MuxRoutes > 1 still picks the existing
// direct transport for every route (N mux streams on one TCP socket),
// not N routes through N intermediates.
//
// ForwardMinHops / ReverseMinHops are per-direction overrides for
// bandwidth-asymmetric workloads (e.g. HTTP GET = small upstream + bulk
// downstream). When > 0 they take precedence over the symmetric
// MinHops for that direction only. Zero = inherit MinHops. Setting
// just ReverseMinHops=2 with MinHops=0 lets forward stay direct while
// reverse is forced multi-hop — the canonical asymmetric test shape.
//
// ForwardMuxRoutes / ReverseMuxRoutes are per-direction overrides for
// MuxRoutes. Setting ForwardMuxRoutes=1 + ReverseMuxRoutes=4 yields
// 1 forward leg + 4 reverse legs — the canonical asymmetric-count
// shape (download-heavy workload aggregating only the reverse
// direction).
type DialOptionsReq struct {
	Addr             appnet.Addr
	MuxRoutes        int
	MinHops          int
	ForwardMinHops   int
	ReverseMinHops   int
	ForwardMuxRoutes int
	ReverseMuxRoutes int
	// Direct, when true, forces a direct-transport-only dial: the router
	// creates a direct transport to the destination if none exists, then dials
	// 1-hop over it, bypassing the route-finder. The `--direct` skynet flag
	// sets this. In spirit mutually exclusive with MinHops >= 2.
	Direct bool
	// DiversifyTransports opts this dial into visor-side auto-diversification
	// for multi-tunnel bandwidth aggregation (docs/mux_aggregation_rfc.md
	// step 3). Set by the skysocks-client for its extra tunnels (2..N): the
	// router excludes the first-hop transports/intermediates already claimed by
	// this visor's live route groups to the same dst, so each tunnel leaves
	// over a different first-hop transport and their throughputs sum. Bounded
	// visor-side to the case where a sibling route group already exists, so a
	// lone dial is byte-identical to today. See router.DialOptions.
	DiversifyTransports bool
	// RequireDisjointFirstHop turns DiversifyTransports from a preference into
	// a requirement: instead of conceding a shared first hop, the dial fails
	// with router.ErrNoDisjointFirstHop. The skysocks standby pool sets it so
	// it can tell "there are more disjoint routes to dial" from "the topology
	// has none left" and stop filling at the real bound.
	RequireDisjointFirstHop bool
	// TunnelRole labels the route group this dial creates — "active" or
	// "standby" — for `visor state --select mux_route_groups` and the proxy
	// status page. The dialing app's own label; empty everywhere else.
	TunnelRole string
}

// Explicit reports whether the request carries any per-call option at all.
// When it does not, the dial is the plain router-picks-freely default and both
// the app-side client and the gateway take the ordinary Dial path. Any
// explicit value — INCLUDING 1 — is an override that must reach the networker
// (MuxRoutes=1 / MinHops=1 lets an app force a single / direct route out from
// under a visor-global min_hops or mux_routes > 1).
func (r DialOptionsReq) Explicit() bool {
	return r.Direct || r.DiversifyTransports || r.RequireDisjointFirstHop ||
		r.MuxRoutes != 0 || r.MinHops != 0 ||
		r.ForwardMinHops != 0 || r.ReverseMinHops != 0 ||
		r.ForwardMuxRoutes != 0 || r.ReverseMuxRoutes != 0
}

// Dial dials to the remote.
func (r *RPCIngressGateway) Dial(remote *appnet.Addr, resp *DialResp) (err error) {
	defer rpcutil.LogCall(r.log, "Dial", remote)(resp, &err)
	return r.dialInternal(*remote, &DialOptionsReq{Addr: *remote}, resp)
}

// DialWithOptions dials to the remote honoring per-call dial options.
// Honored options: MuxRoutes (N parallel mux routes), MinHops (require
// N intermediates), ForwardMinHops / ReverseMinHops (per-direction
// MinHops overrides for bandwidth-asymmetric workloads). Together
// these exercise the operator's mux>direct hypothesis and the
// asymmetric-routing extension: MuxRoutes > 1 with MinHops >= 2 forces
// N disjoint non-direct paths; per-direction MinHops lets forward and
// reverse have different constraints (direct upstream + multi-hop
// downstream). Falls back to plain Dial semantics when no opts demand
// the router path OR the registered networker for the address family
// isn't *SkywireNetworker (e.g. dmsg, where mux is inherent).
func (r *RPCIngressGateway) DialWithOptions(req *DialOptionsReq, resp *DialResp) (err error) {
	defer rpcutil.LogCall(r.log, "DialWithOptions", req)(resp, &err)
	if req == nil {
		return errors.New("DialWithOptions: nil request")
	}
	return r.dialInternal(req.Addr, req, resp)
}

// dialInternal is the common dial body shared by Dial + DialWithOptions.
// When req carries no per-call opts (all MuxRoutes/MinHops/Forward*/
// Reverse* <= 1) it falls through to plain DialContext. Otherwise it
// goes through SkywireNetworker.DialContextWithOptions so the opts
// surface is honored.
func (r *RPCIngressGateway) dialInternal(remote appnet.Addr, req *DialOptionsReq, resp *DialResp) error {
	reservedConnID, free, err := r.cm.ReserveNextID()
	if err != nil {
		return err
	}

	// Thread the calling app's name on the dial context so router-side
	// log entries pick up app_name=<n> for 'cli proxy start --verbose'.
	// r.proc may be nil in unit tests that exercise the gateway in
	// isolation (see pkg/app/conn_test.go); fall back to "" then.
	var appName string
	if r.proc != nil {
		appName = r.proc.conf.AppName
	}
	// Bound the whole setup so a stuck route-setup path can't wedge the app in
	// "starting" forever (see dialSetupCeiling). The ctx scopes only the dial /
	// route setup, not the returned conn's lifetime, so a long-lived route group
	// is unaffected.
	dialCtx, cancelDial := context.WithTimeout(appnet.WithAppName(context.Background(), appName), dialSetupCeiling)
	defer cancelDial()
	conn, err := dialWithMuxRoutes(dialCtx, remote, req)
	if err != nil {
		free()
		return err
	}

	wrappedConn, err := appnet.WrapConn(conn)
	if err != nil {
		free()
		return err
	}

	if err := r.cm.Set(*reservedConnID, wrappedConn); err != nil {
		if cErr := wrappedConn.Close(); cErr != nil {
			r.log.WithError(cErr).Error("Error closing wrappedConn.")
		}
		free()
		return err
	}

	localAddr := wrappedConn.LocalAddr().(appnet.Addr)

	resp.ConnID = *reservedConnID
	resp.LocalPort = localAddr.Port
	// This app now owns that port, which is what lets it report tunnel events
	// on the route group behind it (NoteMuxEvent).
	r.noteDialedPort(localAddr.Port)

	return nil
}

// dialWithMuxRoutes dials remote either via the single-route default
// path (appnet.DialContext) or via the SkywireNetworker DialOptions
// path when the caller requested any per-call opts (mux routes,
// minimum hops, or per-direction MinHops). The type-assertion
// fallback to the default path matches VisorCat's contract (a test-
// mock or future alternative networker silently degrades to single-
// route, preserving correctness with no extra plumbing).
func dialWithMuxRoutes(ctx context.Context, remote appnet.Addr, req *DialOptionsReq) (net.Conn, error) {
	// Take the default (global-inheriting) path only when the app requested
	// NOTHING explicit — every per-call field left at its zero value (see
	// DialOptionsReq.Explicit). Zero means "inherit the visor-global default".
	if req == nil || !req.Explicit() {
		return appnet.DialContext(ctx, remote)
	}
	nw, err := appnet.ResolveNetworker(remote.Net)
	if err != nil {
		return nil, err
	}
	sw, ok := nw.(*appnet.SkywireNetworker)
	if !ok {
		// Non-skynet networker (e.g. dmsg) — per-call opts are no-op
		// concepts at this layer. Fall through to the default dial.
		return appnet.DialContext(ctx, remote)
	}
	opts := router.DefaultDialOptions()
	opts.MuxRoutes = req.MuxRoutes
	opts.MinHops = req.MinHops
	opts.ForwardMinHops = req.ForwardMinHops
	opts.ReverseMinHops = req.ReverseMinHops
	opts.ForwardMuxRoutes = req.ForwardMuxRoutes
	opts.ReverseMuxRoutes = req.ReverseMuxRoutes
	opts.DiversifyTransports = req.DiversifyTransports
	opts.RequireDisjointFirstHop = req.RequireDisjointFirstHop
	opts.TunnelRole = req.TunnelRole
	if req.Direct {
		// Force a 1-hop direct dial that creates the transport on demand and
		// bypasses the route-finder. Mirrors the policy-layer Fallback="direct"
		// short-circuit (UseExistingTpOnly + MinHops=1), plus EnsureDirectTransport
		// so a missing/dropped transport is recreated instead of failing.
		opts.EnsureDirectTransport = true
		opts.UseExistingTpOnly = true
		opts.MinHops = 1
		opts.MuxRoutes = 0
		opts.ForwardMuxRoutes = 0
		opts.ReverseMuxRoutes = 0
	}
	return sw.DialContextWithOptions(ctx, remote, opts)
}

// Listen starts listening.
func (r *RPCIngressGateway) Listen(local *appnet.Addr, lisID *uint16) (err error) {
	defer rpcutil.LogCall(r.log, "Listen", local)(lisID, &err)

	nextLisID, free, err := r.lm.ReserveNextID()
	if err != nil {
		return err
	}

	l, err := appnet.Listen(*local)
	if err != nil {
		free()
		return err
	}

	if err := r.lm.Set(*nextLisID, l); err != nil {
		if cErr := l.Close(); cErr != nil {
			r.log.WithError(cErr).Error("Error closing listener.")
		}
		free()
		return err
	}

	*lisID = *nextLisID
	return nil
}

// AcceptResp contains response parameters for `Accept`.
type AcceptResp struct {
	Remote appnet.Addr
	ConnID uint16
}

// Accept accepts connection from the listener specified by `lisID`.
func (r *RPCIngressGateway) Accept(lisID *uint16, resp *AcceptResp) (err error) {
	defer rpcutil.LogCall(r.log, "Accept", lisID)(resp, &err)

	log := r.log.WithField("func", "Accept")

	log.Debug("Getting listener...")
	lis, err := r.getListener(*lisID)
	if err != nil {
		return err
	}

	log.Debug("Reserving next ID...")
	connID, free, err := r.cm.ReserveNextID()
	if err != nil {
		return err
	}

	log.Debug("Accepting conn...")
	conn, err := lis.Accept()
	if err != nil {
		free()
		return err
	}

	log.Debug("Wrapping conn...")
	wrappedConn, err := appnet.WrapConn(conn)
	if err != nil {
		free()
		return err
	}

	if err := r.cm.Set(*connID, wrappedConn); err != nil {
		if cErr := wrappedConn.Close(); cErr != nil {
			r.log.WithError(cErr).Error("Failed to close wrappedConn.")
		}
		free()
		return err
	}

	remote := wrappedConn.RemoteAddr().(appnet.Addr)

	resp.Remote = remote
	resp.ConnID = *connID

	return nil
}

// WriteReq contains arguments for `Write`.
type WriteReq struct {
	ConnID uint16
	B      []byte
}

// WriteResp contains response parameters for `Write`.
type WriteResp struct {
	N   int
	Err *RPCIOErr
}

// Write writes to the connection.
func (r *RPCIngressGateway) Write(req *WriteReq, resp *WriteResp) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	resp.N, err = conn.Write(req.B)
	resp.Err = ioErrToRPCIOErr(err)

	// avoid error in RPC pipeline, error is included in response body
	return nil
}

// ReadReq contains arguments for `Read`.
type ReadReq struct {
	ConnID uint16
	BufLen int
}

// ReadResp contains response parameters for `Read`.
type ReadResp struct {
	B   []byte
	N   int
	Err *RPCIOErr
}

// Read reads data from connection specified by `connID`.
func (r *RPCIngressGateway) Read(req *ReadReq, resp *ReadResp) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	buf := make([]byte, req.BufLen)

	resp.N, err = conn.Read(buf)
	if resp.N != 0 {
		resp.B = make([]byte, resp.N)
		copy(resp.B, buf[:resp.N])
	}
	if err != nil {
		if err.Error() != io.EOF.Error() {
			// we don't print warning if the conn is already closed
			_, ok := r.cm.Get(req.ConnID)
			if ok {
				r.log.WithError(err).Warn("Received unexpected error when reading from server.")
			}
		}
	}

	if wrappedConn, ok := conn.(*appnet.WrappedConn); ok {
		if skywireConn, ok := wrappedConn.Conn.(*appnet.SkywireConn); ok {
			if ngErr := skywireConn.GetError(); ngErr != nil {
				err = ngErr
			}
		}
	}

	resp.Err = ioErrToRPCIOErr(err)

	// avoid error in RPC pipeline, error is included in response body
	return nil
}

// CloseConn closes connection specified by `connID`.
func (r *RPCIngressGateway) CloseConn(connID *uint16, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "CloseConn", connID)(nil, &err)

	conn, err := r.popConn(*connID)
	if err != nil {
		return err
	}

	return conn.Close()
}

// CloseListener closes listener specified by `lisID`.
func (r *RPCIngressGateway) CloseListener(lisID *uint16, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "CloseListener", lisID)(nil, &err)

	lis, err := r.popListener(*lisID)
	if err != nil {
		return err
	}

	return lis.Close()
}

// DeadlineReq contains arguments for deadline methods.
type DeadlineReq struct {
	ConnID   uint16
	Deadline time.Time
}

// SetDeadline sets deadline for connection specified by `connID`.
func (r *RPCIngressGateway) SetDeadline(req *DeadlineReq, _ *struct{}) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	return conn.SetDeadline(req.Deadline)
}

// SetReadDeadline sets read deadline for connection specified by `connID`.
func (r *RPCIngressGateway) SetReadDeadline(req *DeadlineReq, _ *struct{}) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	return conn.SetReadDeadline(req.Deadline)
}

// SetWriteDeadline sets read deadline for connection specified by `connID`.
func (r *RPCIngressGateway) SetWriteDeadline(req *DeadlineReq, _ *struct{}) error {
	conn, err := r.getConn(req.ConnID)
	if err != nil {
		return err
	}

	return conn.SetWriteDeadline(req.Deadline)
}

// popListener gets listener from the manager by `lisID` and removes it.
// Handles type assertion.
func (r *RPCIngressGateway) popListener(lisID uint16) (net.Listener, error) {
	lisIfc, err := r.lm.Pop(lisID)
	if err != nil {
		return nil, fmt.Errorf("no listener: %w", err)
	}

	return idmanager.AssertListener(lisIfc)
}

// popConn gets conn from the manager by `connID` and removes it.
// Handles type assertion.
func (r *RPCIngressGateway) popConn(connID uint16) (net.Conn, error) {
	connIfc, err := r.cm.Pop(connID)
	if err != nil {
		return nil, fmt.Errorf("no conn: %w", err)
	}

	return idmanager.AssertConn(connIfc)
}

// getListener gets listener from the manager by `lisID`. Handles type assertion.
func (r *RPCIngressGateway) getListener(lisID uint16) (net.Listener, error) {
	lisIfc, ok := r.lm.Get(lisID)
	if !ok {
		return nil, fmt.Errorf("no listener with key %d", lisID)
	}

	return idmanager.AssertListener(lisIfc)
}

// getConn gets conn from the manager by `connID`. Handles type assertion.
func (r *RPCIngressGateway) getConn(connID uint16) (net.Conn, error) {
	connIfc, ok := r.cm.Get(connID)
	if !ok {
		return nil, fmt.Errorf("no conn with key %d", connID)
	}

	return idmanager.AssertConn(connIfc)
}

func ioErrToRPCIOErr(err error) *RPCIOErr {
	if err == nil {
		return nil
	}

	rpcIOErr := &RPCIOErr{
		Text: err.Error(),
	}

	if netErr, ok := err.(net.Error); ok {
		rpcIOErr.IsNetErr = true
		rpcIOErr.IsTimeoutErr = netErr.Timeout()
	}

	return rpcIOErr
}
