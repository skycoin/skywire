package setup

import (
	"context"
	"fmt"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/routing"
)

// RPCGateway is a RPC interface for setup node.
type RPCGateway struct {
	logger  *logging.Logger
	reqPK   cipher.PubKey
	sn      *Node
	timeout time.Duration
}

// NewRPCGateway returns a new RPCGateway.
func NewRPCGateway(reqPK cipher.PubKey, sn *Node, timeout time.Duration) *RPCGateway {
	return &RPCGateway{
		logger:  logging.MustGetLogger(fmt.Sprintf("setup-gateway (%s)", reqPK)),
		reqPK:   reqPK,
		sn:      sn,
		timeout: timeout,
	}
}

// DialRouteGroup dials RouteGroups for route and rules.
func (g *RPCGateway) DialRouteGroup(route routing.BidirectionalRoute, rules *routing.EdgeRules) (err error) {
	startTime := time.Now()

	defer func() {
		g.sn.metrics.Record(time.Since(startTime), err != nil)
	}()

	g.logger.Infof("Received RPC DialRouteGroup request")

	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	initRules, err := g.sn.handleDialRouteGroup(ctx, route)
	if err != nil {
		return err
	}

	// Confirm routes with initiating visor.
	*rules = initRules

	return nil
}
