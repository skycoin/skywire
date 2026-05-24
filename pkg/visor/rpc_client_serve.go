// Package visor pkg/visor/rpc_client_serve.go
package visor

import (
	"context"
	"net"
	"net/rpc"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/routing"
)

// rpcSkynetDialTimeout bounds the skynet dial attempt before
// falling back to dmsg. Short enough that a missing route doesn't
// stall the visor's reconnect loop; long enough for a routed dial
// to succeed on a healthy network.
const rpcSkynetDialTimeout = 2 * time.Second

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// dialHypervisorRPC tries to open a connection to the hypervisor's
// RPC port over the skywire router first, falling back to dmsg.
//
// The hypervisor's ServeRPC (hypervisor.go) dual-listens for RPC on
// dmsg AND skynet at the same port via goServeSkynetMirror. Once
// the visor has a stcpr / sudph transport to the hypervisor (via
// autoUpgradeHypervisorTransport), the skynet path lets RPC traffic
// flow over the fast p2p path instead of the dmsg relay.
func dialHypervisorRPC(ctx context.Context, log logrus.FieldLogger, dmsgC *dmsg.Client, rAddr dmsg.Addr) (net.Conn, error) {
	// Skynet attempt in a goroutine with a hard outer ceiling.
	// appnet.DialContext doesn't reliably honor context cancellation
	// in all code paths (the AppDirectMux direct-dial branch and
	// parts of route setup do I/O that's not always ctx-bound), so
	// we wrap with an external timer. The goroutine closes any conn
	// it gets if the outer dial has already moved on.
	type skyResult struct {
		conn net.Conn
		err  error
	}
	skyCh := make(chan skyResult, 1)
	go func() {
		skyAddr := appnet.Addr{
			Net:    appnet.TypeSkynet,
			PubKey: rAddr.PK,
			Port:   routing.Port(rAddr.Port),
		}
		skyCtx, skyCancel := context.WithTimeout(ctx, rpcSkynetDialTimeout)
		defer skyCancel()
		conn, err := appnet.DialContext(skyCtx, skyAddr)
		select {
		case skyCh <- skyResult{conn, err}:
		default:
			if err == nil && conn != nil {
				_ = conn.Close() //nolint:errcheck
			}
		}
	}()

	select {
	case r := <-skyCh:
		if r.err == nil {
			log.WithField("via", "skynet").Info("Dialed.")
			return r.conn, nil
		}
	case <-time.After(rpcSkynetDialTimeout):
		// Skynet attempt exceeded its budget — fall through to dmsg.
	}

	log.WithField("via", "dmsg").Info("Dialing...")
	return dmsgC.Dial(ctx, rAddr)
}

// ServeRPCClient repetitively dials to a remote hypervisor and serves a
// RPC server to that address.
//
// The dial path tries skynet first (via the skywire router) and falls
// back to dmsg. See dialHypervisorRPC.
func ServeRPCClient(ctx context.Context, log logrus.FieldLogger, dmsgC *dmsg.Client, rpcS *rpc.Server, rAddr dmsg.Addr, errCh chan<- error) {
	const maxBackoff = time.Second * 5
	retry := netutil.NewRetrier(log, netutil.DefaultInitBackoff, maxBackoff, netutil.DefaultTries, netutil.DefaultFactor)
	for {
		var conn net.Conn
		err := retry.Do(ctx, func() (rErr error) {
			conn, rErr = dialHypervisorRPC(ctx, log, dmsgC, rAddr)
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
		connCtx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel is called when ServeConn returns
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
