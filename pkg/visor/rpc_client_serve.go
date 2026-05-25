// Package visor pkg/visor/rpc_client_serve.go
package visor

import (
	"context"
	"net"
	"net/rpc"
	"sync"
	"sync/atomic"
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

// hypervisorRPCIdleTimeout is the maximum time the visor will hold
// an idle (no traffic in either direction) served RPC conn to its
// hypervisor before closing it and redialing. The hypervisor polls
// us with Summary RPC calls on its UI refresh cadence; under normal
// operation those polls happen well within this window and the
// idle timer never fires.
//
// This timeout exists for the case where the dmsg session (or the
// skywire route) underneath the conn dies silently — the hypervisor's
// next poll fails, its rpc.Client.Close() runs but can't deliver a
// close frame over the broken session, and our blocking Read on the
// served end of the conn stays parked indefinitely. Without this
// timer the visor sits with an orphaned conn for the rest of the
// process lifetime; the hypervisor sees it as "last seen N minutes
// ago" with no recovery short of a visor restart.
//
// 10 min is a tradeoff: long enough that normal UI-idle periods
// don't cause reconnect churn, short enough that operators don't
// stare at stale "last seen" rows for hours.
const hypervisorRPCIdleTimeout = 10 * time.Minute

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
		// Wrap the conn so an extended idle period (no read or write
		// activity for hypervisorRPCIdleTimeout) forces the conn closed
		// and triggers a redial. Guards against silently-dead dmsg /
		// route sessions where the hypervisor's close on a failed poll
		// can't reach us. See the const doc for the full rationale.
		idleConn := newIdleConn(conn, hypervisorRPCIdleTimeout, log)
		connCtx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel is called when ServeConn returns
		go func() {
			rpcS.ServeConn(idleConn)
			cancel()
		}()
		<-connCtx.Done()
		idleConn.stop()

		log.WithError(conn.Close()).
			WithField("context_done", isDone(ctx)).
			Debug("Conn closed. Redialing...")
	}
}

// idleConn wraps a net.Conn and closes it if no Read or Write
// activity occurs within idleTimeout. The wrapped Read / Write
// stamp lastActivity on every successful byte transfer; a watcher
// goroutine ticks every idleTimeout/4 and closes the conn when the
// stamp is older than idleTimeout.
//
// Close on idle is the safe action — the consumer (rpcS.ServeConn)
// sees the close as a Read error, returns, and the outer
// ServeRPCClient loop redials. There's no false-positive harm:
// reconnect to the hypervisor is cheap relative to staying
// stranded.
type idleConn struct {
	net.Conn
	lastActivity atomic.Int64 // unix nanos
	idleTimeout  time.Duration
	stopOnce     sync.Once
	stopped      chan struct{}
	log          logrus.FieldLogger
}

func newIdleConn(c net.Conn, idleTimeout time.Duration, log logrus.FieldLogger) *idleConn {
	ic := &idleConn{
		Conn:        c,
		idleTimeout: idleTimeout,
		stopped:     make(chan struct{}),
		log:         log,
	}
	ic.lastActivity.Store(time.Now().UnixNano())
	go ic.watch()
	return ic
}

func (c *idleConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.lastActivity.Store(time.Now().UnixNano())
	}
	return n, err
}

func (c *idleConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.lastActivity.Store(time.Now().UnixNano())
	}
	return n, err
}

// stop tells the watcher to exit. Called from ServeRPCClient after
// the ServeConn goroutine returns, so we don't leak the watcher
// past the conn's lifetime.
func (c *idleConn) stop() {
	c.stopOnce.Do(func() { close(c.stopped) })
}

func (c *idleConn) watch() {
	t := time.NewTicker(c.idleTimeout / 4)
	defer t.Stop()
	for {
		select {
		case <-c.stopped:
			return
		case now := <-t.C:
			last := time.Unix(0, c.lastActivity.Load())
			if now.Sub(last) >= c.idleTimeout {
				c.log.WithField("idle_for", now.Sub(last)).
					Info("Hypervisor RPC conn idle past threshold; closing to force redial.")
				_ = c.Conn.Close() //nolint:errcheck
				return
			}
		}
	}
}
