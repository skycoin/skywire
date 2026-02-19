// Package router pkg/router/setup_rpc_gateway.go
package router

import (
	"context"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// SetupRPCGateway is a RPC interface for setup node.
type SetupRPCGateway struct {
	Metrics setupmetrics.Metrics
	Ctx     context.Context
	Conn    net.Conn
	ReqPK   cipher.PubKey
	Dialer  network.Dialer
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

	initRules, err := CreateRouteGroup(ctx, g.Dialer, route, g.Metrics)
	if err != nil {
		log.WithError(err).Warn("CreateRouteGroup failed")
		return err
	}

	// Confirm routes with initiating visor.
	*rules = initRules
	return nil
}

// HealthCheckArgs is an empty struct for the health check call.
type HealthCheckArgs struct{}

// HealthCheckReply is returned by the HealthCheck RPC method.
type HealthCheckReply struct {
	Status string
}

// HealthCheck to test if the setup node is responsive.
func (g *SetupRPCGateway) HealthCheck(_ *HealthCheckArgs, reply *HealthCheckReply) error {
	log := logging.MustGetLogger("health-check")
	log.WithField("remote_pk", g.ReqPK.String()).Info("Health check received from RSN")
	reply.Status = "OK"
	return nil
}
