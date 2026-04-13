// Package visor pkg/visor/embedded_route_setup.go
package visor

import (
	"context"
	"errors"
	"net/rpc"
	"time"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"

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

	// stats aggregates per-request success/failure metadata for the
	// RouteSetupStats RPC and `skywire-cli route rsn-stats` CLI.
	stats *setupmetrics.Collector
}

// Serve starts the route setup-node listener on DmsgSetupPort.
// This allows other visors to connect and request route setup.
func (ers *EmbeddedRouteSetup) Serve(ctx context.Context) error {
	const timeout = 2 * time.Minute

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
	// Use the pre-wired Collector (falling back to Empty if the struct
	// was constructed without one) so its snapshots include every
	// attempt that hit the listener.
	var metrics setupmetrics.Metrics = setupmetrics.NewEmpty()
	if ers.stats != nil {
		metrics = ers.stats
	}

	// Limit concurrent route setup requests. Each request dials remote
	// visors via dmsg, reserving ephemeral ports for up to
	// HandshakeTimeout (5s) per dial attempt. With sequential phase
	// dial trying up to 6 servers per destination, a single request
	// can hold ~12 ports at peak. The circuit breaker fast-fails
	// known-bad destinations (0ms), but new/unknown destinations
	// still burn ports until they time out. 32 concurrent × 12 ports
	// = 384 ports worst case — well within the 16K porter range
	// while leaving headroom for the visor's own dmsg operations.
	const maxConcurrent = 32
	sem := make(chan struct{}, maxConcurrent)

	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			// Check if context was canceled or entity closed (normal shutdown)
			if ctx.Err() != nil || errors.Is(err, dmsg.ErrEntityClosed) {
				ers.log.Debug("Embedded route setup-node listener stopped")
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

		// Acquire semaphore before spawning. Non-blocking — if the sem is
		// full we drop this request IMMEDIATELY rather than waiting, so
		// the accept loop keeps draining the listener's accept buffer
		// (AcceptBufferSize=20). If we block here, the buffer fills and
		// dmsg drops streams with "listener accept chan maxed", which is
		// worse than dropping with a clear reason.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			conn.Close() //nolint:errcheck,gosec
			return nil
		default:
			ers.log.Warn("Route setup request dropped: concurrency limit reached")
			if ers.stats != nil {
				ers.stats.RecordConcurrencyDrop()
			}
			conn.Close() //nolint:errcheck,gosec
			continue
		}

		go func() {
			defer func() { <-sem }()
			rpcS.ServeConn(conn)
		}()
	}
}

// CreateRouteGroup creates a route group by directly calling the setup-node logic.
// This bypasses the need to dial a remote setup-node over dmsg.
func (ers *EmbeddedRouteSetup) CreateRouteGroup(ctx context.Context, biRt routing.BidirectionalRoute) (routing.EdgeRules, error) {
	const timeout = 2 * time.Minute

	ers.log.WithField("src", biRt.Desc.SrcPK()).WithField("dst", biRt.Desc.DstPK()).
		Debug("Creating route group via embedded setup-node")

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := router.WrapDmsgClient(ers.dmsgC)
	// Share the same Collector as the listener so local self-dials
	// (visor → own embedded setup-node) show up in the same snapshot
	// rather than needing a second data source.
	var metrics setupmetrics.Metrics = setupmetrics.NewEmpty()
	if ers.stats != nil {
		metrics = ers.stats
	}

	return router.CreateRouteGroup(ctx, dialer, biRt, metrics)
}

// Stats returns the current collector snapshot. Returns a zero-value
// snapshot if stats are disabled (shouldn't happen in normal use, but
// keeps the RPC layer simple).
func (ers *EmbeddedRouteSetup) Stats() setupmetrics.StatsSnapshot {
	if ers.stats == nil {
		return setupmetrics.StatsSnapshot{}
	}
	return ers.stats.Snapshot()
}

// ResetStats clears all counters and ring buffers on the embedded
// route setup-node's stats collector. Useful before starting a
// diagnostic capture window.
func (ers *EmbeddedRouteSetup) ResetStats() {
	if ers.stats != nil {
		ers.stats.Reset()
	}
}

// PK returns the public key of the embedded route setup-node.
func (ers *EmbeddedRouteSetup) PK() cipher.PubKey {
	return ers.pk
}

// DmsgClient returns the dmsg client used by the embedded route setup-node.
func (ers *EmbeddedRouteSetup) DmsgClient() *dmsg.Client {
	return ers.dmsgC
}
