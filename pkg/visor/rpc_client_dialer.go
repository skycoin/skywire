package visor

import (
	"context"
	"net/rpc"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/netutil"
	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// ServeRPCClient repetitively dials to a remote dmsg address and serves a RPC server to that address.
func ServeRPCClient(ctx context.Context, log logrus.FieldLogger, n *snet.Network, rpcS *rpc.Server, rAddr dmsg.Addr, errCh chan<- error) {
	for {
		var conn *snet.Conn
		err := netutil.NewDefaultRetrier(log).Do(ctx, func() (rErr error) {
			conn, rErr = n.Dial(ctx, snet.DmsgType, rAddr.PK, rAddr.Port)
			return rErr
		})
		if err != nil {
			if errCh != nil {
				errCh <- err
			}
			return
		}
		if conn == nil {
			log.WithField("conn == nil", conn == nil).
				Fatal("An unexpected occurrence happened.")
		}

		connCtx, cancel := context.WithCancel(ctx)
		go func() {
			rpcS.ServeConn(conn)
			cancel()
		}()
		<-connCtx.Done()

		log.WithError(conn.Close()).
			WithField("context_done", isDone(ctx)).
			Debug("ServeRPCClient: Closed conn.")
	}
}
