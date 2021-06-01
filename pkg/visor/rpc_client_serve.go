package visor

import (
	"context"
	"net"
	"net/rpc"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/netutil"
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
func ServeRPCClient(ctx context.Context, log logrus.FieldLogger, dmsgC *dmsg.Client, rpcS *rpc.Server, rAddr dmsg.Addr, errCh chan<- error) {
	const maxBackoff = time.Second * 5
	retry := netutil.NewRetrier(log, netutil.DefaultInitBackoff, maxBackoff, netutil.DefaultTries, netutil.DefaultFactor)

	for {
		var conn net.Conn
		err := retry.Do(ctx, func() (rErr error) {
			log.Info("Dialing...")
			addr := dmsg.Addr{rAddr.PK, rAddr.Port}
			conn, rErr = dmsgC.Dial(ctx, addr)
			return rErr
		})
		if err != nil {
			if errCh != nil {
				log.WithError(err).Info("Pushed error into 'errCh'.")
				errCh <- err
			}
			log.WithError(err).Info("Stopped Serving.")
			return
		}
		if conn == nil {
			log.WithField("conn == nil", conn == nil).Warn("An unexpected occurrence happened.")
			continue
		}

		log.Info("Serving RPC client...")
		connCtx, cancel := context.WithCancel(ctx)
		go func() {
			rpcS.ServeConn(conn)
			cancel()
		}()
		<-connCtx.Done()

		log.WithError(conn.Close()).
			WithField("context_done", isDone(ctx)).
			Debug("Conn closed. Redialing...")
	}
}
