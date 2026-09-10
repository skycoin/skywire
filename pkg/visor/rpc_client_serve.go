// Package visor pkg/visor/rpc_client_serve.go c3-vis-core
package visor

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
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
//
// 90s lines up with the dmsg.StreamIdleTimeout (2min) on the
// hypervisor side — by the time the hypervisor's view of the
// stream is decisively dead, the visor's redial path has already
// fired here. Previous value (10min) left peers showing
// "last seen N minutes ago" in the hypervisor UI for the full
// idle window before the visor finally noticed and redialed;
// operators correctly flagged this as a regression from before
// the silent-stream-death fix (#2806). Trade-off: a quieter
// hypervisor (UI closed for >90s) triggers a single redial per
// 90s window — cheap, the dial completes in ~100ms over an
// already-established dmsg session.
const hypervisorRPCIdleTimeout = 90 * time.Second

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
	// A WebSocket transport is direct too: a browser visor attached to this
	// hypervisor opens one back to it at the page's own origin, and that socket
	// is the fast path for the RPC and pty this visor serves it.
	if tp, err := tpM.GetTransport(pk, tptypes.WS); err == nil && tp != nil {
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

// rpcUpgradePoll is how often a dmsg-served hypervisor RPC conn checks
// whether a direct transport to the hypervisor has appeared.
const rpcUpgradePoll = 3 * time.Second

// shouldUpgradeToSkynet is the decision behind watchForSkynetUpgrade: a
// direct transport exists and skynet is not in its failure cooldown.
func shouldUpgradeToSkynet(hasFast bool, lastSkyFail *atomic.Int64) bool {
	return hasFast && !inSkynetCooldown(lastSkyFail)
}

// watchForSkynetUpgrade closes conn (a dmsg-served RPC conn) once a direct
// transport to pk exists and skynet is dialable, so ServeRPCClient redials
// over skynet. Returns when ctx (the conn's serve context) ends.
func watchForSkynetUpgrade(ctx context.Context, log logrus.FieldLogger, tpM *transport.Manager, pk cipher.PubKey, lastSkyFail *atomic.Int64, conn net.Conn) {
	t := time.NewTicker(rpcUpgradePoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if shouldUpgradeToSkynet(hasFastTransportTo(tpM, pk), lastSkyFail) {
				log.Info("Direct transport to hypervisor is up; closing dmsg RPC conn to redial over skynet.")
				_ = conn.Close() //nolint:errcheck,gosec
				return
			}
		}
	}
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

const (
	// rpcDialInitBackoff / rpcDialMaxBackoff / rpcDialBackoffFactor pace the
	// redial after a transient failure (the peer is published but the dial
	// did not complete).
	rpcDialInitBackoff   = time.Second
	rpcDialMaxBackoff    = 5 * time.Second
	rpcDialBackoffFactor = 1.3
	// rpcAbsentMaxBackoff caps the pause between dials to a hypervisor that
	// has no discovery entry. Each attempt costs a discovery lookup and the
	// peer will not answer until it publishes again, so the pause doubles
	// from rpcDialInitBackoff up to this. A direct transport from the peer
	// cuts the pause short (waitForRedial), so a re-opened desk tab is
	// served within rpcUpgradePoll, and an off-LAN tab that publishes an
	// entry is reached within this bound.
	rpcAbsentMaxBackoff = time.Minute
)

// isPeerAbsent reports whether a dial failed because the hypervisor has no
// discovery entry. The dmsg client wraps dmsgdisc.ErrKeyNotFound through a few
// layers (some by string), so match both ways, as pkg/dmsg does.
func isPeerAbsent(err error) bool {
	return err != nil &&
		(errors.Is(err, dmsgdisc.ErrKeyNotFound) || strings.Contains(err.Error(), dmsgdisc.ErrKeyNotFound.Error()))
}

// nextDialBackoff grows a transient-failure pause geometrically to
// rpcDialMaxBackoff; zero (fresh) starts at rpcDialInitBackoff.
func nextDialBackoff(prev time.Duration) time.Duration {
	if prev <= 0 {
		return rpcDialInitBackoff
	}
	next := time.Duration(float64(prev) * rpcDialBackoffFactor)
	if next > rpcDialMaxBackoff {
		return rpcDialMaxBackoff
	}
	return next
}

// absentDialBackoff is the pause after the n-th consecutive "no discovery
// entry" failure: 1s, 2s, 4s, … capped at rpcAbsentMaxBackoff.
func absentDialBackoff(n int) time.Duration {
	d := rpcDialInitBackoff
	for i := 1; i < n && d < rpcAbsentMaxBackoff; i++ {
		d *= 2
	}
	if d > rpcAbsentMaxBackoff {
		return rpcAbsentMaxBackoff
	}
	return d
}

// waitForRedial sleeps for wait, returning early (true) when a direct
// transport to pk appears — the next dial then rides skynet instead of
// waiting out a pause meant for an absent peer. Returns false when ctx ends.
func waitForRedial(ctx context.Context, wait time.Duration, tpM *transport.Manager, pk cipher.PubKey) bool {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	poll := time.NewTicker(rpcUpgradePoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return true
		case <-poll.C:
			if hasFastTransportTo(tpM, pk) {
				return true
			}
		}
	}
}

// ServeRPCClient repetitively dials to a remote hypervisor and serves
// a RPC server to that address. The dial uses dmsg by default, switching
// to skynet when an underlying fast transport exists and isn't in
// cooldown — see dialHypervisorRPC.
func ServeRPCClient(ctx context.Context, log logrus.FieldLogger, tpM *transport.Manager, dmsgC *dmsg.Client, rpcS *rpc.Server, rAddr dmsg.Addr, errCh chan<- error) {
	// lastSkyFail is per-hypervisor (this function is one goroutine
	// per hypervisor PK). Records the most recent skynet failure so
	// dialHypervisorRPC can apply the cooldown.
	var lastSkyFail atomic.Int64

	var (
		wait   time.Duration // next pause after a failed dial
		absent int           // consecutive "no discovery entry" failures
	)
	for {
		conn, via, err := dialHypervisorRPC(ctx, log, tpM, dmsgC, rAddr, &lastSkyFail)
		if err != nil {
			if isDone(ctx) {
				if errCh != nil {
					log.WithError(ctx.Err()).Info("Pushed error into 'errCh'.")
					errCh <- ctx.Err()
				}
				log.WithError(ctx.Err()).Info("Stopped Serving.")
				return
			}
			if isPeerAbsent(err) {
				// The hypervisor has no discovery entry: a desk tab that was
				// closed, or a paired peer that is simply not online. Every
				// attempt is a discovery lookup, so back off far longer than
				// for a transient failure — and wake at once if it reappears
				// over a direct transport (a re-opened tab).
				absent++
				wait = absentDialBackoff(absent)
				log.WithError(err).WithField("wait", wait).Debug("Hypervisor is not published; waiting.")
			} else {
				absent = 0
				wait = nextDialBackoff(wait)
				log.WithError(err).Debug("Dial to hypervisor failed; retrying.")
			}
			if !waitForRedial(ctx, wait, tpM, rAddr.PK) {
				if errCh != nil {
					errCh <- ctx.Err()
				}
				log.WithError(ctx.Err()).Info("Stopped Serving.")
				return
			}
			continue
		}
		wait, absent = 0, 0
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
		// A conn that came up over dmsg is only the bootstrap: if a direct
		// transport to the hypervisor appears while it is serving (the
		// upgrade loop built one, or a browser visor attached over /tp/ws
		// after we had already started redialing), drop it so the next
		// dial rides skynet instead of sitting on the relay until idle.
		if via == transportDmsg {
			go watchForSkynetUpgrade(connCtx, log, tpM, rAddr.PK, &lastSkyFail, idleConn)
		}
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
