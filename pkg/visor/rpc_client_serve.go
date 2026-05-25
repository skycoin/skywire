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
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// rpcSkynetDialTimeout bounds the skynet dial attempt before falling
// back to dmsg. Skynet only gets attempted at all when a stcpr or
// sudph transport to the hypervisor already exists, so this timeout
// is just for the route-setup leg above that existing transport.
const rpcSkynetDialTimeout = 2 * time.Second

// rpcSkynetCooldown is how long we stop trying skynet for the
// hypervisor RPC conn after a failure on that path.
//
// A "failure" here means either the dial failed, or a previously-
// established skynet conn ended unexpectedly (caught either by ServeConn
// returning a read error or by the idleConn watcher). Either signal
// indicates the skynet route is unhealthy enough that we shouldn't
// keep cycling onto it on every redial — dmsg is the reliable
// baseline and we should sit on it for a bit before retrying skynet.
//
// 5 min lines up with autoUpgradeHypervisorTransport's reconcile
// cadence: after that interval, the auto-upgrade pass would have
// re-checked / re-established the transport, so a fresh skynet try
// makes sense.
const rpcSkynetCooldown = 5 * time.Minute

// hypervisorRPCIdleTimeout is the maximum time the visor will hold
// an idle (no traffic in either direction) served RPC conn to its
// hypervisor before closing it and redialing.
//
// Under normal operation the hypervisor's Summary polling cadence
// (driven by UI refresh) keeps the conn active. The idle-detect
// catches the case where the underlying skynet route or dmsg session
// dies silently: the hypervisor's next poll times out, its close
// can't deliver a close frame over the broken session, and our
// blocking Read on the served end of the conn sits parked forever.
const hypervisorRPCIdleTimeout = 10 * time.Minute

// rpcTransport names the transport carrying a given served RPC conn,
// recorded by dialHypervisorRPC so ServeRPCClient can apply the
// skynet cooldown when an unexpectedly-closed conn was on skynet.
type rpcTransport string

const (
	transportSkynet rpcTransport = "skynet"
	transportDmsg   rpcTransport = "dmsg"
)

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// hasFastTransportTo reports whether the visor has an established
// stcpr or sudph transport to the given peer. This is the gate for
// trying skynet — without an underlying transport, a skynet dial
// would go out to the router for a route that can't be built and
// either fail or hang up to its 2s budget.
func hasFastTransportTo(tpM *transport.Manager, pk cipher.PubKey) bool {
	if tpM == nil {
		return false
	}
	if tp, err := tpM.GetTransport(pk, tptypes.STCPR); err == nil && tp != nil {
		return true
	}
	if tp, err := tpM.GetTransport(pk, tptypes.SUDPH); err == nil && tp != nil {
		return true
	}
	return false
}

// dialHypervisorRPC opens the visor's persistent RPC connection to
// the hypervisor. It prefers skynet (the fast direct path) whenever:
//
//  1. A stcpr or sudph transport to the hypervisor exists — i.e.,
//     autoUpgradeHypervisorTransport has already done its job. Without
//     this, a skynet dial would either fail or wait out the 2s budget
//     while the router tries (and fails) to build a route.
//  2. There has been no recent skynet failure (rpcSkynetCooldown). A
//     failure here means a previous skynet conn either failed to dial
//     or died on us silently — there's no point cycling back onto
//     skynet on every redial when the path is flaky.
//
// In all other cases it dials over dmsg. dmsg is the reliable
// baseline: relay disconnects propagate failure cleanly up the
// stack as EOF, so the retrier above can redial without operator
// intervention.
//
// The returned rpcTransport identifies which path served the
// returned conn, so ServeRPCClient can record a skynet failure if
// the conn ends unexpectedly.
func dialHypervisorRPC(
	ctx context.Context,
	log logrus.FieldLogger,
	tpM *transport.Manager,
	dmsgC *dmsg.Client,
	rAddr dmsg.Addr,
	lastSkyFail *atomic.Int64,
) (net.Conn, rpcTransport, error) {
	if hasFastTransportTo(tpM, rAddr.PK) && !inSkynetCooldown(lastSkyFail) {
		skyAddr := appnet.Addr{
			Net:    appnet.TypeSkynet,
			PubKey: rAddr.PK,
			Port:   routing.Port(rAddr.Port),
		}
		skyCtx, cancel := context.WithTimeout(ctx, rpcSkynetDialTimeout)
		conn, err := appnet.DialContext(skyCtx, skyAddr)
		cancel()
		if err == nil {
			log.WithField("via", "skynet").Info("Dialed.")
			return conn, transportSkynet, nil
		}
		// Eligible for skynet but the dial failed. Record the failure
		// so the next redial cycle defers to dmsg until the cooldown
		// elapses, then fall through to dmsg for this attempt.
		lastSkyFail.Store(time.Now().UnixNano())
		log.WithError(err).Debug("Skynet dial to hypervisor failed; falling back to dmsg.")
	}

	log.WithField("via", "dmsg").Info("Dialing...")
	conn, err := dmsgC.Dial(ctx, rAddr)
	if err != nil {
		return nil, "", err
	}
	return conn, transportDmsg, nil
}

// inSkynetCooldown reports whether the most recent recorded skynet
// failure is recent enough to skip skynet on this dial cycle.
func inSkynetCooldown(lastFail *atomic.Int64) bool {
	last := lastFail.Load()
	if last == 0 {
		return false
	}
	return time.Since(time.Unix(0, last)) < rpcSkynetCooldown
}

// ServeRPCClient repetitively dials to a remote hypervisor and serves
// a RPC server to that address. The dial uses dmsg by default, switching
// to skynet when an underlying fast transport exists and isn't in
// cooldown — see dialHypervisorRPC.
func ServeRPCClient(ctx context.Context, log logrus.FieldLogger, tpM *transport.Manager, dmsgC *dmsg.Client, rpcS *rpc.Server, rAddr dmsg.Addr, errCh chan<- error) {
	const maxBackoff = time.Second * 5
	retry := netutil.NewRetrier(log, netutil.DefaultInitBackoff, maxBackoff, netutil.DefaultTries, netutil.DefaultFactor)

	// lastSkyFail is per-hypervisor (this function is one goroutine
	// per hypervisor PK). Records the most recent skynet failure so
	// dialHypervisorRPC can apply the cooldown.
	var lastSkyFail atomic.Int64

	for {
		var conn net.Conn
		var via rpcTransport
		err := retry.Do(ctx, func() (rErr error) {
			conn, via, rErr = dialHypervisorRPC(ctx, log, tpM, dmsgC, rAddr, &lastSkyFail)
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

		log.WithField("via", string(via)).Info("Serving RPC client...")
		// Idle-detect wraps the conn so we don't sit parked in Read
		// forever if the underlying session (skynet route OR dmsg)
		// dies silently. Defense in depth on top of dmsg's own
		// session-level close detection.
		idleConn := newIdleConn(conn, hypervisorRPCIdleTimeout, log)
		connCtx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel is called when ServeConn returns
		go func() {
			rpcS.ServeConn(idleConn)
			cancel()
		}()
		<-connCtx.Done()
		idleConn.stop()

		// If a skynet conn ended for any reason other than the
		// visor shutting down, treat that as a skynet-path failure
		// and start the cooldown — keeps us from immediately cycling
		// back onto the same flaky path on the next dial.
		if via == transportSkynet && !isDone(ctx) {
			lastSkyFail.Store(time.Now().UnixNano())
		}

		log.WithError(conn.Close()).
			WithField("context_done", isDone(ctx)).
			WithField("via", string(via)).
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
