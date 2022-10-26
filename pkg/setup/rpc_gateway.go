// Package setup pkg/setup/rpc_gateway.go
package setup

import (
	"context"
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/setup/setupmetrics"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// RPCGateway is a RPC interface for setup node.
type RPCGateway struct {
	Metrics setupmetrics.Metrics
	Ctx     context.Context
	Conn    net.Conn
	ReqPK   cipher.PubKey
	Dialer  network.Dialer
	Timeout time.Duration
}

// DialRouteGroup dials RouteGroups for route and rules.
func (g *RPCGateway) DialRouteGroup(route routing.BidirectionalRoute, rules *routing.EdgeRules) (err error) {
	log := logging.MustGetLogger("request:" + g.ReqPK.String())
	defer g.Metrics.RecordRequest()(rules, &err)

	ctx, cancel := context.WithTimeout(g.Ctx, g.Timeout)
	defer cancel()
	go func() {
		if <-ctx.Done(); ctx.Err() == context.DeadlineExceeded {
			log.WithError(ctx.Err()).
				WithField("close_error", g.Conn.Close()).
				Warn("Closed underlying connection because deadline was exceeded.")
		}
	}()

	initRules, err := CreateRouteGroup(ctx, g.Dialer, route, g.Metrics)
	if err != nil {
		return err
	}

	// Confirm routes with initiating visor.
	*rules = initRules
	return nil
}
