// Package router pkg/router/setup_rpc_gateway.go
package router

import (
	"context"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

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
