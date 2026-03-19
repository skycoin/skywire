// Package visor pkg/visor/embedded_route_setup.go
package visor

import (
	"context"
	"net/rpc"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// EmbeddedRouteSetup holds the state of an embedded Route Setup Node.
// It uses a separate dmsg client with its own PK/SK identity to:
// 1. Accept incoming route setup requests from other visors on DmsgSetupPort (36)
// 2. Dial remote visors to set up routes for the local visor
type EmbeddedRouteSetup struct {
	dmsgC *dmsg.Client
	pk    cipher.PubKey
	log   *logging.Logger
}

// Serve starts the route setup-node listener on DmsgSetupPort.
// This allows other visors to connect and request route setup.
func (ers *EmbeddedRouteSetup) Serve(ctx context.Context) error {
	const timeout = 30 * time.Second

	ers.log.WithField("dmsg_port", skyenv.DmsgSetupPort).Info("Starting embedded route setup-node listener")
	lis, err := ers.dmsgC.Listen(skyenv.DmsgSetupPort)
	if err != nil {
		return err
	}

	// Close listener when context is canceled
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			ers.log.WithError(err).Warn("Embedded route setup-node listener closed with error")
		}
	}()

	ers.log.WithField("dmsg_port", skyenv.DmsgSetupPort).Info("Accepting route setup requests")
	metrics := setupmetrics.NewEmpty()

	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			// Check if context was canceled (normal shutdown)
			if ctx.Err() != nil {
				ers.log.Debug("Embedded route setup-node listener stopped (context canceled)")
				return nil
			}
			ers.log.WithError(err).Warn("Failed to accept stream on route setup-node")
			return err
		}

		reqPK := conn.RemoteAddr().(dmsg.Addr).PK
		ers.log.WithField("remote_pk", reqPK).Debug("Accepted route setup request")

		gw := &router.SetupRPCGateway{
			Metrics: metrics,
			Ctx:     ctx,
			Conn:    conn,
			ReqPK:   reqPK,
			Dialer:  router.WrapDmsgClient(ers.dmsgC),
			Timeout: timeout,
		}

		rpcS := rpc.NewServer()
		if err := rpcS.Register(gw); err != nil {
			ers.log.WithError(err).Error("Failed to register RPC gateway")
			conn.Close() //nolint:errcheck,gosec
			continue
		}

		go rpcS.ServeConn(conn)
	}
}

// CreateRouteGroup creates a route group by directly calling the setup-node logic.
// This bypasses the need to dial a remote setup-node over dmsg.
func (ers *EmbeddedRouteSetup) CreateRouteGroup(ctx context.Context, biRt routing.BidirectionalRoute) (routing.EdgeRules, error) {
	const timeout = 30 * time.Second

	ers.log.WithField("src", biRt.Desc.SrcPK()).WithField("dst", biRt.Desc.DstPK()).
		Debug("Creating route group via embedded setup-node")

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := router.WrapDmsgClient(ers.dmsgC)
	metrics := setupmetrics.NewEmpty()

	return router.CreateRouteGroup(ctx, dialer, biRt, metrics)
}

// PK returns the public key of the embedded route setup-node.
func (ers *EmbeddedRouteSetup) PK() cipher.PubKey {
	return ers.pk
}

// DmsgClient returns the dmsg client used by the embedded route setup-node.
func (ers *EmbeddedRouteSetup) DmsgClient() *dmsg.Client {
	return ers.dmsgC
}
