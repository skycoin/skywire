//go:build !tinygo || (js && wasm)

// Package router pkg/router/setup_rpc_gateway.go c2-net-routing
package router

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// errCascadeUnavailable is returned by the cascade-sign RPCs when the RSN
// has no CascadeBuilder configured (cascade disabled / DMSG-only mode).
var errCascadeUnavailable = errors.New("cascade signing unavailable on this setup node")

// SetupRPCGateway is a RPC interface for setup node.
type SetupRPCGateway struct {
	Metrics setupmetrics.Metrics
	Ctx     context.Context
	Conn    net.Conn
	ReqPK   cipher.PubKey
	Dialer  network.Dialer
	Pool    *ClientPool     // optional: reuse connections across requests
	Cascade *CascadeBuilder // optional: cascade route setup
	Timeout time.Duration
}

// DialRouteGroup dials RouteGroups for route and rules.
func (g *SetupRPCGateway) DialRouteGroup(route routing.BidirectionalRoute, rules *routing.EdgeRules) (err error) {
	log := logging.MustGetLogger("request:" + g.ReqPK.String())
	defer g.Metrics.RecordRequest()(rules, &err)

	ctx, cancel := context.WithTimeout(g.Ctx, g.Timeout)
	defer cancel()

	// Note: We intentionally do NOT close g.Conn on deadline exceeded.
	// The connection is managed by the RPC server in serveSetup() and closing it
	// here causes a race condition that corrupts the router state, making all
	// subsequent route setups fail with "read/write on closed pipe".
	// Context cancellation will propagate naturally through CreateRouteGroup.

	initRules, err := CreateRouteGroup(ctx, g.Dialer, g.Pool, g.Cascade, route, g.Metrics)
	if err != nil {
		log.WithError(err).Warn("CreateRouteGroup failed")
		return err
	}

	// Confirm routes with initiating visor.
	*rules = initRules
	return nil
}

// CascadeSignReserveArgs is the request for CascadeSignReserve.
type CascadeSignReserveArgs struct {
	Route routing.BidirectionalRoute
}

// CascadeSignReserveReply carries the RSN-signed reserve cascades. The RSN
// signs but does NOT send — the SOURCE injects these bytes into its own
// transports (Forward[0].TpID / Reverse[0].TpID) and collects the reserved
// route-ID ACKs.
type CascadeSignReserveReply struct {
	FwdSessionID    uint64
	FwdReserveBytes []byte
	RevSessionID    uint64
	RevReserveBytes []byte
}

// CascadeSignReserve is the RSN-side half of the cascade reserve phase. The
// RSN is a pure signing oracle: it builds and signs the nested per-hop
// reserve cascades for the bidirectional route and returns the bytes plus
// the session IDs. It never dials hops or sends anything itself.
func (g *SetupRPCGateway) CascadeSignReserve(args *CascadeSignReserveArgs, reply *CascadeSignReserveReply) error {
	log := logging.MustGetLogger("cascade-sign-reserve:" + g.ReqPK.String())
	if g.Cascade == nil {
		return errCascadeUnavailable
	}
	if err := args.Route.Check(); err != nil {
		log.WithError(err).Warn("CascadeSignReserve: invalid route")
		return err
	}

	fwdSessionID, fwdBytes, revSessionID, revBytes, err := signReserveCascades(g.Cascade, args.Route)
	if err != nil {
		log.WithError(err).Warn("CascadeSignReserve failed")
		return err
	}

	reply.FwdSessionID = fwdSessionID
	reply.FwdReserveBytes = fwdBytes
	reply.RevSessionID = revSessionID
	reply.RevReserveBytes = revBytes
	return nil
}

// CascadeSignInstallArgs is the request for CascadeSignInstall. The source
// supplies the route, the session IDs from the reserve phase, and the route
// IDs it collected per hop. The RSN recomputes rules deterministically from
// the route + IDs; it does NOT trust source-supplied rules.
type CascadeSignInstallArgs struct {
	Route        routing.BidirectionalRoute
	FwdSessionID uint64
	RevSessionID uint64
	FwdRouteIDs  []routing.RouteID
	RevRouteIDs  []routing.RouteID
}

// CascadeSignInstallReply carries the RSN-signed install cascades plus the
// initiating-edge rules the source installs locally and returns to the dialer.
type CascadeSignInstallReply struct {
	FwdInstallBytes []byte
	RevInstallBytes []byte
	InitEdge        routing.EdgeRules
}

// CascadeSignInstall is the RSN-side half of the cascade install phase. It
// reconstructs the IDReserver from the source-collected route IDs, recomputes
// the routing rules deterministically (GenerateRules), and builds+signs the
// nested per-hop install cascades. It returns the install bytes for the
// source to inject, plus the initiating EdgeRules.
func (g *SetupRPCGateway) CascadeSignInstall(args *CascadeSignInstallArgs, reply *CascadeSignInstallReply) error {
	log := logging.MustGetLogger("cascade-sign-install:" + g.ReqPK.String())
	if g.Cascade == nil {
		return errCascadeUnavailable
	}
	if err := args.Route.Check(); err != nil {
		log.WithError(err).Warn("CascadeSignInstall: invalid route")
		return err
	}

	fwdBytes, revBytes, initEdge, err := signInstallCascades(
		g.Cascade, args.Route,
		args.FwdSessionID, args.RevSessionID,
		args.FwdRouteIDs, args.RevRouteIDs,
	)
	if err != nil {
		log.WithError(err).Warn("CascadeSignInstall failed")
		return err
	}

	reply.FwdInstallBytes = fwdBytes
	reply.RevInstallBytes = revBytes
	reply.InitEdge = initEdge
	return nil
}

// SignTransportQueryArgs is the request for SignTransportQuery: the source asks
// the RSN to sign a transport-query capability targeting a destination.
type SignTransportQueryArgs struct {
	RequesterPK cipher.PubKey // source S making the request
	TargetPK    cipher.PubKey // destination D whose transports are requested
}

// SignTransportQueryReply carries the RSN-signed query. The RSN is a pure
// signing oracle here too: it signs the (RSN, target, requester, nonce) tuple
// and returns it; it does not contact the destination. The SOURCE carries the
// signed query to the destination (phase-1: dmsg-direct) and the destination
// verifies it against its trusted-RSN allowlist before responding.
type SignTransportQueryReply struct {
	Query *TransportQuery
}

// SignTransportQuery is the RSN-side signing endpoint for the RSN-oracle 2-hop
// route path. It mints a nonce, signs a TransportQuery targeting args.TargetPK
// on behalf of args.RequesterPK, and returns it. Reuses the RSN keypair the
// cascade builder already holds (g.Cascade.rsnPK / rsnSK), so the same
// trusted-RSN authorization that gates cascade route setup gates the query.
func (g *SetupRPCGateway) SignTransportQuery(args *SignTransportQueryArgs, reply *SignTransportQueryReply) error {
	log := logging.MustGetLogger("sign-transport-query:" + g.ReqPK.String())
	if g.Cascade == nil {
		return errCascadeUnavailable
	}
	var nonceBuf [8]byte
	if _, err := rand.Read(nonceBuf[:]); err != nil {
		return err
	}
	q := &TransportQuery{
		RSNPK:       g.Cascade.rsnPK,
		TargetPK:    args.TargetPK,
		RequesterPK: args.RequesterPK,
		Nonce:       binary.BigEndian.Uint64(nonceBuf[:]),
	}
	if err := q.Sign(g.Cascade.rsnSK); err != nil {
		log.WithError(err).Warn("SignTransportQuery: sign failed")
		return err
	}
	reply.Query = q
	return nil
}

// RelayPeersArgs is the request for the RelayPeers RPC.
type RelayPeersArgs struct{}

// RelayPeersReply contains the RSN's transport peer PKs so visors
// can cache them for future relay-based route setup without DMSG.
type RelayPeersReply struct {
	Peers []cipher.PubKey `json:"peers"`
}

// RelayPeers returns the PKs of visors that have direct transports
// to this RSN. Visors call this to populate their relay cache so
// subsequent route setup requests can be relayed through these peers
// instead of using DMSG.
func (g *SetupRPCGateway) RelayPeers(_ *RelayPeersArgs, reply *RelayPeersReply) error {
	if g.Cascade == nil || g.Cascade.tm == nil {
		reply.Peers = nil
		return nil
	}
	var peers []cipher.PubKey
	g.Cascade.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if !tp.IsClosed() {
			peers = append(peers, tp.Remote())
		}
		return true
	})
	reply.Peers = peers
	return nil
}

// HealthCheckArgs is an empty struct for the health check call.
type HealthCheckArgs struct{}

// HealthCheckReply is returned by the HealthCheck RPC method.
type HealthCheckReply struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

// HealthCheck to test if the setup node is responsive.
func (g *SetupRPCGateway) HealthCheck(_ *HealthCheckArgs, reply *HealthCheckReply) error {
	log := logging.MustGetLogger("health-check")
	log.WithField("remote_pk", g.ReqPK.String()).Info("Health check received from RSN")
	info := buildinfo.Get()
	reply.Status = "OK"
	reply.Version = info.Version
	reply.Commit = info.Commit
	reply.Date = info.Date
	return nil
}
