package skysocks

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// newTestTunnelConn returns a conn the Client can wrap as a tunnel: a pipe with
// a yamux server answering on the far side, so AddTunnel / AddStandbyTunnel
// build a session that behaves like a real one.
func newTestTunnelConn(t *testing.T) (net.Conn, func()) {
	t.Helper()
	a, b := net.Pipe()
	ssess, err := yamux.Server(b, yamux.DefaultConfig())
	require.NoError(t, err)
	go func() {
		for {
			st, e := ssess.Accept()
			if e != nil {
				return
			}
			go io.Copy(io.Discard, st) //nolint:errcheck
		}
	}()
	return a, func() {
		_ = ssess.Close() //nolint:errcheck
		_ = a.Close()     //nolint:errcheck
		_ = b.Close()     //nolint:errcheck
	}
}

// newPoolClient builds a Client holding one ACTIVE tunnel, with an active
// target of 1 and a pool ceiling of max.
func newPoolClient(t *testing.T, max int) (*Client, func()) {
	t.Helper()
	conn, closeConn := newTestTunnelConn(t)
	c := &Client{closeC: make(chan struct{}), streams: map[uint32]streamMeta{}}
	require.NoError(t, c.AddTunnel(conn))
	c.SetTunnelTarget(1)
	c.SetStandbyPool(max)
	// These tests assert dial COUNTS, so pin the fill to one dial at a time —
	// the serial shape they were written against. The concurrent fill has its
	// own tests (TestPoolFill_RunsConcurrentDials).
	require.True(t, router.SetSetupFillInflight(1))
	t.Cleanup(routersettings.Reset)
	return c, closeConn
}

// waitFill waits for the single in-flight pool dial to finish.
func waitFill(t *testing.T, c *Client) {
	t.Helper()
	require.Eventually(t, func() bool { return c.poolFillInFlight.Load() == 0 }, 3*time.Second, 5*time.Millisecond)
}

// A standby tunnel is held, pinged and measured — and never picked while an
// active tunnel is live. This is the whole bargain: the pool costs nothing in
// stream placement until it is needed.
func TestPickSession_SkipsStandby(t *testing.T) {
	active, closeA := newTestSession(t)
	defer closeA()
	standby, closeS := newTestSession(t)
	defer closeS()

	now := time.Now()
	fast, slow := new(tunnelMeter), new(tunnelMeter)
	// The standby tunnel is the BETTER one by every statistic the picker uses,
	// so only the standby mask can explain the answer.
	fast.rttMs = 5
	fast.rxCapBps, fast.txCapBps, fast.busyAt = 9e6, 9e6, now
	slow.rttMs = 200
	slow.rxCapBps, slow.txCapBps, slow.busyAt = 1e6, 1e6, now

	c := &Client{
		sessions:  []*yamux.Session{active, standby},
		recvStamp: map[*yamux.Session]*tunnelMeter{active: slow, standby: fast},
		standby:   map[*yamux.Session]bool{standby: true},
		closeC:    make(chan struct{}),
	}
	require.Same(t, active, c.pickSessionFor(pickAny), "a lone stream must not land on a standby tunnel")
	require.Same(t, active, c.pickSessionFor(pickRecv), "a range chunk must not land on a standby tunnel either")
	require.True(t, c.IsStandby(standby))
	require.False(t, c.IsStandby(active))
}

// ...and the escape: with no active tunnel live, the pool is what the proxy
// runs on. Refusing to pick would be the collapse the pool exists to prevent.
func TestPickSession_FallsBackToStandbyWhenNoActiveIsLive(t *testing.T) {
	active, closeA := newTestSession(t)
	standby, closeS := newTestSession(t)
	defer closeS()

	m := new(tunnelMeter)
	m.rttMs = 40

	c := &Client{
		sessions:  []*yamux.Session{active, standby},
		recvStamp: map[*yamux.Session]*tunnelMeter{active: new(tunnelMeter), standby: m},
		standby:   map[*yamux.Session]bool{standby: true},
		closeC:    make(chan struct{}),
	}
	closeA()
	require.Eventually(t, active.IsClosed, time.Second, 5*time.Millisecond)

	require.Same(t, standby, c.pickSessionFor(pickAny), "the standby tunnel is all there is; pick it")
	require.Same(t, standby, c.pickSessionFor(pickRecv))
}

// The fill stops AT the ceiling and says so once — the bound that keeps one app
// start from becoming a setup-node storm.
func TestPoolFill_StopsAtCeiling(t *testing.T) {
	var dials atomic.Int64
	var closers []func()
	defer func() {
		for _, fn := range closers {
			fn()
		}
	}()

	c, closeFirst := newPoolClient(t, 3)
	closers = append(closers, closeFirst)
	c.SetPoolDial(func() (net.Conn, error) {
		dials.Add(1)
		conn, closeConn := newTestTunnelConn(t)
		closers = append(closers, closeConn)
		return conn, nil
	})

	// Two dials take the pool from 1 held to the ceiling of 3.
	for i := 0; i < 2; i++ {
		c.maybePoolFill()
		waitFill(t, c)
	}
	require.EqualValues(t, 2, dials.Load())
	held, standby, settled, _, _ := c.StandbyPoolState()
	require.Equal(t, 3, held)
	require.Equal(t, 2, standby, "everything past the active target is standby")
	require.False(t, settled, "not settled until a fill tick observes the ceiling")

	// The next tick observes the ceiling, settles, and DOES NOT dial again —
	// however many ticks follow.
	for i := 0; i < 5; i++ {
		c.maybePoolFill()
		waitFill(t, c)
	}
	require.EqualValues(t, 2, dials.Load(), "the ceiling must not be dialed past")
	_, _, settled, at, reason := c.StandbyPoolState()
	require.True(t, settled)
	require.False(t, at.IsZero())
	require.Equal(t, "pool ceiling", reason, "the ceiling stopped it, not the topology — a bench must not read this as the disjoint bound")
}

// Exhaustion is a settled answer about the topology, so one refusal ends the
// fill for good. Re-asking cannot change it — and asking anyway is #4325.
func TestPoolFill_StopsAtExhaustionAndDoesNotRedial(t *testing.T) {
	var dials atomic.Int64
	c, closeFirst := newPoolClient(t, 8)
	defer closeFirst()
	c.SetPoolDial(func() (net.Conn, error) {
		dials.Add(1)
		// Exactly the shape the app sees: the router's sentinel flattened to
		// text by the app-server RPC boundary and wrapped by the dial path.
		return nil, errors.New("dial server: " + router.ErrNoDisjointFirstHop.Error() +
			": all 4 candidate route(s) leave over a first hop a sibling route group already holds")
	})

	for i := 0; i < 5; i++ {
		c.maybePoolFill()
		waitFill(t, c)
	}
	require.EqualValues(t, 1, dials.Load(), "one refusal must end the fill")

	held, standby, settled, at, reason := c.StandbyPoolState()
	require.Equal(t, 1, held)
	require.Zero(t, standby)
	require.True(t, settled)
	require.False(t, at.IsZero())
	require.Equal(t, "no disjoint first hop left", reason, "the bench must be able to tell exhaustion from the ceiling")

	// A tunnel death is the ONE thing that frees a first hop, so it — and only
	// it — re-arms the fill.
	c.armPoolFill()
	c.maybePoolFill()
	waitFill(t, c)
	require.EqualValues(t, 2, dials.Load(), "a tunnel death re-arms the fill")
}

// An ordinary dial FAILURE is not exhaustion, so it buys a bounded second look:
// maxRedialFails dials make a round, the fill then waits out a backoff, and only
// after poolRetryRounds does it rest. Measured on the rig 2026-09-17, treating a
// failure like exhaustion parked the pool at five tunnels with four disjoint rig
// intermediates still free.
func TestPoolFill_RetriesFailuresInBoundedRounds(t *testing.T) {
	var dials atomic.Int64
	c, closeFirst := newPoolClient(t, 8)
	defer closeFirst()
	c.SetPoolDial(func() (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("route setup timed out")
	})

	drainRound := func() {
		// More ticks than a round can use: the extra ones must be swallowed by
		// the backoff, not spent on dials.
		for i := 0; i < 8; i++ {
			c.maybePoolFill()
			waitFill(t, c)
		}
	}

	drainRound()
	require.EqualValues(t, maxRedialFails, dials.Load(), "a round is exactly maxRedialFails dials")
	_, _, settled, _, _ := c.StandbyPoolState()
	require.False(t, settled, "a failure round pauses the fill; it does not end it")
	c.redialMu.Lock()
	require.False(t, c.poolRetryAt.IsZero(), "the next round is scheduled")
	require.Equal(t, poolRetryDelay(1), poolRetryDelay(c.poolRetryRound))
	c.poolRetryAt = time.Time{} // the test does not wait out 30s
	c.redialMu.Unlock()

	drainRound()
	require.EqualValues(t, 2*maxRedialFails, dials.Load())
	_, _, settled, _, _ = c.StandbyPoolState()
	require.False(t, settled)
	c.redialMu.Lock()
	c.poolRetryAt = time.Time{}
	c.redialMu.Unlock()

	// Round poolRetryRounds closes the ledger: the fill rests until a death.
	drainRound()
	require.EqualValues(t, poolRetryRounds*maxRedialFails, dials.Load(), "the retry is bounded")
	_, _, settled, _, _ = c.StandbyPoolState()
	require.True(t, settled)

	// ...and a death re-arms it with a clean ledger.
	c.armPoolFill()
	c.maybePoolFill()
	waitFill(t, c)
	require.EqualValues(t, poolRetryRounds*maxRedialFails+1, dials.Load())
}

// A dial that LANDS clears the failure ledger, so an exit that hiccups twice and
// then answers is not one failure away from a paused fill forever after.
func TestPoolFill_SuccessClearsTheFailureLedger(t *testing.T) {
	var closers []func()
	defer func() {
		for _, fn := range closers {
			fn()
		}
	}()
	var fail atomic.Bool
	fail.Store(true)

	c, closeFirst := newPoolClient(t, 8)
	closers = append(closers, closeFirst)
	c.SetPoolDial(func() (net.Conn, error) {
		if fail.Load() {
			return nil, errors.New("route setup timed out")
		}
		conn, closeConn := newTestTunnelConn(t)
		closers = append(closers, closeConn)
		return conn, nil
	})

	c.maybePoolFill()
	waitFill(t, c)
	c.redialMu.Lock()
	require.Equal(t, 1, c.poolFails)
	c.redialMu.Unlock()

	fail.Store(false)
	c.maybePoolFill()
	waitFill(t, c)
	c.redialMu.Lock()
	require.Zero(t, c.poolFails)
	require.Zero(t, c.poolRetryRound)
	require.True(t, c.poolRetryAt.IsZero())
	c.redialMu.Unlock()
}

// A pool dial ALWAYS lands in standby — and when the active set is short, the
// tunnel that fills it is chosen by rank from the whole pool, in the same call.
// A fresh dial is not evidence that a route carries traffic: measured on the
// rig 2026-09-17, a refill that joined the active set directly put every later
// upload on a brand-new sudph tunnel at 0.45 MB/s while six measured stcpr
// tunnels sat in the pool.
func TestPoolFill_RefillsTheActiveSetFromThePool(t *testing.T) {
	var closers []func()
	defer func() {
		for _, fn := range closers {
			fn()
		}
	}()

	c, closeFirst := newPoolClient(t, 4)
	closers = append(closers, closeFirst)
	c.SetTunnelTarget(2) // the app asked for two active tunnels; one is up
	c.SetPoolDial(func() (net.Conn, error) {
		conn, closeConn := newTestTunnelConn(t)
		closers = append(closers, closeConn)
		return conn, nil
	})

	c.maybePoolFill()
	waitFill(t, c)
	held, standby, _, _, _ := c.StandbyPoolState()
	require.Equal(t, 2, held)
	require.Zero(t, standby, "the active set was short, so a promote followed the dial in the same call")
	require.Equal(t, 2, c.activeLiveCount())

	c.maybePoolFill()
	waitFill(t, c)
	held, standby, _, _, _ = c.StandbyPoolState()
	require.Equal(t, 3, held)
	require.Equal(t, 1, standby, "the active set is full, so the surplus is standby")
	require.Equal(t, 2, c.activeLiveCount())
}

// --standby-pool 0 is the pre-pool behavior, byte for byte: no fill, ever.
func TestPoolFill_DisabledByZero(t *testing.T) {
	var dials atomic.Int64
	c, closeFirst := newPoolClient(t, 0)
	defer closeFirst()
	c.SetPoolDial(func() (net.Conn, error) {
		dials.Add(1)
		return nil, nil
	})
	for i := 0; i < 3; i++ {
		c.maybePoolFill()
		waitFill(t, c)
	}
	require.Zero(t, dials.Load())
	_, standby, settled, _, _ := c.StandbyPoolState()
	require.Zero(t, standby)
	require.False(t, settled)
}

// The fill runs setup.fill_inflight dials AT ONCE. Serial was the reason a pool
// of eight took ~56 s to build (one dial per ~7 s tick, each paying its own
// route-finder/oracle query and its own setup-node request), and it is also why
// the initiator-side batcher had nothing to collect: concurrent dials to one
// exit are what become a single batched setup request.
func TestPoolFill_RunsConcurrentDials(t *testing.T) {
	var closers []func()
	defer func() {
		for _, fn := range closers {
			fn()
		}
	}()

	c, closeFirst := newPoolClient(t, 32)
	closers = append(closers, closeFirst)
	require.True(t, router.SetSetupFillInflight(6))

	var inFlight, peak, dials atomic.Int64
	var mu sync.Mutex
	release := make(chan struct{})
	c.SetPoolDial(func() (net.Conn, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release // hold every dial open so the concurrency is observable
		inFlight.Add(-1)
		dials.Add(1)
		mu.Lock()
		conn, closeConn := newTestTunnelConn(t)
		closers = append(closers, closeConn)
		mu.Unlock()
		return conn, nil
	})

	c.maybePoolFill()
	require.Eventually(t, func() bool { return inFlight.Load() == 6 }, 3*time.Second, 5*time.Millisecond,
		"six dials must be in flight together")
	close(release)
	waitFill(t, c)

	require.EqualValues(t, 6, dials.Load())
	require.EqualValues(t, 6, peak.Load(), "never more than setup.fill_inflight at once")
	held, standby, _, _, _ := c.StandbyPoolState()
	require.Equal(t, 7, held, "the active tunnel plus six")
	require.Equal(t, 6, standby)
}

// The launch count is bounded by the GAP to the ceiling, not just by the
// in-flight knob: a pool two short of its ceiling launches two, not eight.
func TestPoolFill_LaunchesOnlyUpToTheCeiling(t *testing.T) {
	var closers []func()
	defer func() {
		for _, fn := range closers {
			fn()
		}
	}()

	c, closeFirst := newPoolClient(t, 3) // 1 held, ceiling 3 -> room for 2
	closers = append(closers, closeFirst)
	require.True(t, router.SetSetupFillInflight(8))

	var dials atomic.Int64
	var mu sync.Mutex
	c.SetPoolDial(func() (net.Conn, error) {
		dials.Add(1)
		mu.Lock()
		conn, closeConn := newTestTunnelConn(t)
		closers = append(closers, closeConn)
		mu.Unlock()
		return conn, nil
	})

	c.maybePoolFill()
	waitFill(t, c)
	require.EqualValues(t, 2, dials.Load(), "the ceiling bounds the launch, the knob does not override it")
	held, _, _, _, _ := c.StandbyPoolState()
	require.Equal(t, 3, held)
}

// pool.freeze still stops the fill dead, however many dials the knob allows.
func TestPoolFill_FreezeBeatsConcurrency(t *testing.T) {
	c, closeFirst := newPoolClient(t, 32)
	defer closeFirst()
	require.True(t, router.SetSetupFillInflight(8))

	var dials atomic.Int64
	c.SetPoolDial(func() (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("must not be called")
	})

	t.Cleanup(func() { skysettings.Reset() })
	require.True(t, skysettings.Apply(map[string]int64{skysettings.PoolFreeze: 1}))

	c.maybePoolFill()
	waitFill(t, c)
	require.Zero(t, dials.Load(), "a frozen pool dials nothing")
}

// --- the capacity prior ------------------------------------------------------

// The prior is the MINIMUM over a leg's hops, because a path is as wide as its
// narrowest hop — and a hop the visor holds no entry for reports 0, which must
// read as "unknown" and not as "slow".
func TestTunnelPrior_IsTheNarrowestKnownHop(t *testing.T) {
	leg := func(alive bool, bps ...float64) proxystatus.Leg {
		l := proxystatus.Leg{Alive: alive}
		for _, b := range bps {
			l.Hops = append(l.Hops, proxystatus.Hop{ThroughputBps: b})
		}
		return l
	}

	require.Equal(t, 3e6, legPriorBps(leg(true, 9e6, 3e6, 7e6)), "the narrowest hop bounds the path")
	require.Equal(t, 9e6, legPriorBps(leg(true, 9e6, 0)), "an unknown hop is skipped, not counted as zero")
	require.Zero(t, legPriorBps(leg(true)), "no hops, no prior")
	require.Zero(t, legPriorBps(leg(true, 0, 0)), "nothing known about any hop")

	// A tunnel takes the BEST of its alive legs — a conservative claim, since
	// the legs stripe and a sum would promise an aggregate the mux must earn.
	// A dead leg says nothing about what the tunnel can carry.
	tun := proxystatus.Tunnel{Legs: []proxystatus.Leg{
		leg(true, 2e6), leg(true, 5e6), leg(false, 40e6),
	}}
	require.Equal(t, 5e6, tunnelPriorBps(tun))
}

// The prior reaches the app over the channel it already uses to see its own
// tunnels — the ProxyStatus RPC — and is matched to a session by the tunnel's
// LOCAL PORT, the one name the app, the visor and the bench already share for
// one tunnel. A tunnel the visor could not name a port for is never guessed at.
func TestPullCapacityPriors_MatchesTunnelsByLocalPort(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	a, closeA := newTestSession(t)
	defer closeA()
	b, closeB := newTestSession(t)
	defer closeB()

	mA, mB := new(tunnelMeter), new(tunnelMeter)
	mA.port, mB.port = 1000, 1001
	c := &Client{
		sessions:  []*yamux.Session{a, b},
		recvStamp: map[*yamux.Session]*tunnelMeter{a: mA, b: mB},
		standby:   map[*yamux.Session]bool{b: true},
		closeC:    make(chan struct{}),
	}
	hop := func(bps float64) proxystatus.Hop { return proxystatus.Hop{ThroughputBps: bps} }
	snap := proxystatus.Snapshot{Tunnels: []proxystatus.Tunnel{
		{LocalPort: 1001, Legs: []proxystatus.Leg{{Alive: true, Hops: []proxystatus.Hop{hop(9e6), hop(4e6)}}}},
		{LocalPort: 0, Legs: []proxystatus.Leg{{Alive: true, Hops: []proxystatus.Hop{hop(99e6)}}}},
	}}
	pulls := 0
	c.appProxyStatus = func() (proxystatus.Snapshot, error) { pulls++; return snap, nil }

	now := time.Now()
	c.pullCapacityPriors(now)
	require.Equal(t, 1, pulls)
	require.Equal(t, 4e6, mB.prior(), "the standby is weighed by its narrowest hop")
	require.Zero(t, mA.prior(), "a tunnel the snapshot did not name keeps no prior")

	// The rate limit: inside tunnel.prior_refresh the visor is not asked again.
	c.pullCapacityPriors(now.Add(tunnelPriorRefresh / 2))
	require.Equal(t, 1, pulls)
	c.pullCapacityPriors(now.Add(2 * tunnelPriorRefresh))
	require.Equal(t, 2, pulls)

	// A route whose transports stop reporting throughput goes back to being
	// honestly unknown rather than coasting on a number from minutes ago.
	snap = proxystatus.Snapshot{Tunnels: []proxystatus.Tunnel{{LocalPort: 1001}}}
	c.pullCapacityPriors(now.Add(4 * tunnelPriorRefresh))
	require.Zero(t, mB.prior())
}

// capacityOrPrior is the one accessor the planner and the promoter read, and
// the distinction it draws is the whole point: a measurement, a prior, or
// nothing — never a prior dressed up as a measurement.
func TestCapacityOrPrior_NeverPassesAPriorOffAsAMeasurement(t *testing.T) {
	now := time.Now()
	m := new(tunnelMeter)

	bps, fresh, prior := m.capacityOrPrior(now, false)
	require.Zero(t, bps)
	require.False(t, fresh)
	require.False(t, prior, "nothing measured and no prior is not a prior")

	m.setPrior(5e6)
	bps, fresh, prior = m.capacityOrPrior(now, false)
	require.Equal(t, 5e6, bps)
	require.False(t, fresh, "a prior is never fresh: no busy window produced it")
	require.True(t, prior)

	// A real sample takes over the moment there is one, in that direction only.
	m.mu.Lock()
	m.rxCapBps, m.busyAt = 2e6, now
	m.mu.Unlock()
	bps, fresh, prior = m.capacityOrPrior(now, false)
	require.Equal(t, 2e6, bps, "evidence replaces the prior even when it is worse news")
	require.True(t, fresh)
	require.False(t, prior)

	bps, _, prior = m.capacityOrPrior(now, true)
	require.Equal(t, 5e6, bps, "the upload direction has still proven nothing")
	require.True(t, prior)
}
