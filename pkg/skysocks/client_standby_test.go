package skysocks

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/router"
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
	return c, closeConn
}

// waitFill waits for the single in-flight pool dial to finish.
func waitFill(t *testing.T, c *Client) {
	t.Helper()
	require.Eventually(t, func() bool { return !c.poolFillInFlight.Load() }, 3*time.Second, 5*time.Millisecond)
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
	held, standby, settled, _ := c.StandbyPoolState()
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
	_, _, settled, at := c.StandbyPoolState()
	require.True(t, settled)
	require.False(t, at.IsZero())
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

	held, standby, settled, at := c.StandbyPoolState()
	require.Equal(t, 1, held)
	require.Zero(t, standby)
	require.True(t, settled)
	require.False(t, at.IsZero())

	// A tunnel death is the ONE thing that frees a first hop, so it — and only
	// it — re-arms the fill.
	c.armPoolFill()
	c.maybePoolFill()
	waitFill(t, c)
	require.EqualValues(t, 2, dials.Load(), "a tunnel death re-arms the fill")
}

// An ordinary dial failure backs off after maxRedialFails and settles, so an
// exit that has gone unreachable is not hammered either.
func TestPoolFill_BacksOffOnDialFailure(t *testing.T) {
	var dials atomic.Int64
	c, closeFirst := newPoolClient(t, 8)
	defer closeFirst()
	c.SetPoolDial(func() (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("route setup timed out")
	})

	for i := 0; i < 8; i++ {
		c.maybePoolFill()
		waitFill(t, c)
	}
	require.EqualValues(t, maxRedialFails, dials.Load())
	_, _, settled, _ := c.StandbyPoolState()
	require.True(t, settled)
}

// A pool dial lands in the ACTIVE set while that set is short — so the same
// loop that grows the pool also restores the aggregation width after a death,
// and only the surplus is standby.
func TestPoolFill_RefillsTheActiveSetFirst(t *testing.T) {
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
	held, standby, _, _ := c.StandbyPoolState()
	require.Equal(t, 2, held)
	require.Zero(t, standby, "the active set was short, so the new tunnel is active")
	require.Equal(t, 2, c.activeLiveCount())

	c.maybePoolFill()
	waitFill(t, c)
	held, standby, _, _ = c.StandbyPoolState()
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
	_, standby, settled, _ := c.StandbyPoolState()
	require.Zero(t, standby)
	require.False(t, settled)
}
