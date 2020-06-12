package setup

import (
	"context"
	"net"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/metrics"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

// RPCGateway is a RPC interface for setup node.
type RPCGateway struct {
	Metrics metrics.Recorder

	Ctx     context.Context
	Conn    net.Conn
	ReqPK   cipher.PubKey
	Dialer  snet.Dialer
	Timeout time.Duration
}

// DialRouteGroup dials RouteGroups for route and rules.
func (g *RPCGateway) DialRouteGroup(route routing.BidirectionalRoute, rules *routing.EdgeRules) (err error) {
	log := logging.MustGetLogger("request:" + g.ReqPK.String())

	startTime := time.Now()
	defer func() { g.Metrics.Record(time.Since(startTime), err != nil) }()

	ctx, cancel := context.WithTimeout(g.Ctx, g.Timeout)
	defer cancel()
	go func() {
		if <-ctx.Done(); ctx.Err() == context.DeadlineExceeded {
			log.WithError(ctx.Err()).
				WithField("close_error", g.Conn.Close()).
				Warn("Closed underlying connection because deadline was exceeded.")
		}
	}()

	initRules, err := CreateRouteGroup(ctx, g.Dialer, route)
	if err != nil {
		return err
	}

	// Confirm routes with initiating visor.
	*rules = initRules
	return nil
}
