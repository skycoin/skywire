package skysocks

import (
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"
)

// TestTunnelMeterCapacity proves the meter keeps the peak rate a tunnel showed
// while busy, forgets it only under load, and learns nothing from an idle
// window: keepalive bytes never become a "proven" capacity.
func TestTunnelMeterCapacity(t *testing.T) {
	m := new(tunnelMeter)
	t0 := time.Unix(1000, 0)
	m.sample(t0, false) // first sample only anchors
	m.rx.Add(69)        // a ping while idle
	m.sample(t0.Add(time.Second), false)
	bps, fresh := m.capacity(t0.Add(time.Second))
	require.Zero(t, bps, "an idle window proves nothing")
	require.False(t, fresh)

	m.rx.Add(1_000_000)
	m.tx.Add(250_000)
	m.sample(t0.Add(2*time.Second), true)
	bps, fresh = m.capacity(t0.Add(2 * time.Second))
	require.InDelta(t, 1e6, bps, 1)
	require.True(t, fresh)

	m.sample(t0.Add(3*time.Second), false) // idle: nothing moved, nothing forgotten
	bps, fresh = m.capacity(t0.Add(3 * time.Second))
	require.InDelta(t, 1e6, bps, 1)
	require.True(t, fresh, "within meterFresh of the busy window")
	_, fresh = m.capacity(t0.Add(2*time.Second + meterFresh + time.Millisecond))
	require.False(t, fresh, "past meterFresh the estimate is stale")

	m.sample(t0.Add(4*time.Second), true) // busy and slower: the estimate decays
	bps, _ = m.capacity(t0.Add(4 * time.Second))
	require.InDelta(t, 0.9e6, bps, 1)
	m.sample(t0.Add(4*time.Second+100*time.Millisecond), true) // too soon: no sample
	bps, _ = m.capacity(t0.Add(4 * time.Second))
	require.InDelta(t, 0.9e6, bps, 1)
}

// TestPickSessionWeighsCapacity proves a range chunk goes to the tunnel that
// would give it the most bandwidth — proven download capacity over the streams
// already on the tunnel — while a tunnel with nothing proven is credited the
// best known capacity so it gets probed. (A lone stream is picked on latency
// instead; see TestPickAnyTakesTheLowestLatencyTunnel.)
func TestPickSessionWeighsCapacity(t *testing.T) {
	s0, close0 := newTestSession(t)
	defer close0()
	s1, close1 := newTestSession(t)
	defer close1()
	now := time.Now()
	direct, twoHop := new(tunnelMeter), new(tunnelMeter)
	direct.rxCapBps, direct.txCapBps, direct.busyAt = 2e6, 9e6, now // the rig's direct tunnel: slow down, fast up
	twoHop.rxCapBps, twoHop.txCapBps, twoHop.busyAt = 3e6, 5e6, now
	c := &Client{
		sessions:  []*yamux.Session{s1, s0},
		recvStamp: map[*yamux.Session]*tunnelMeter{s0: direct, s1: twoHop},
		closeC:    make(chan struct{}),
	}
	require.Same(t, s1, c.pickSessionFor(pickRecv), "a range chunk weighs download capacity alone")

	// Three chunks already on the two-hop tunnel: (3+1)/3e6 loses to (0+1)/2e6.
	for i := 0; i < 3; i++ {
		st, err := s1.Open()
		require.NoError(t, err)
		defer st.Close() //nolint:errcheck
	}
	require.Same(t, s0, c.pickSessionFor(pickRecv), "open streams dilute a tunnel's capacity")

	// A cold tunnel is credited the best known capacity: it beats a busy one.
	s2, close2 := newTestSession(t)
	defer close2()
	c.sessions = append(c.sessions, s2)
	c.recvStamp[s2] = new(tunnelMeter)
	st, err := s0.Open()
	require.NoError(t, err)
	defer st.Close() //nolint:errcheck
	require.Same(t, s2, c.pickSessionFor(pickRecv), "an unproven tunnel is probed ahead of a busy proven one")
}

// TestPickSessionProbesStaleIdleTunnel proves the starvation escape: while a
// transfer is in progress, an idle tunnel whose estimate is stale is credited
// the best known capacity and gets a stream (so it is measured again), yet
// with every tunnel idle the same stale estimate still steers a lone stream
// to the tunnel proven faster.
func TestPickSessionProbesStaleIdleTunnel(t *testing.T) {
	fast, close0 := newTestSession(t)
	defer close0()
	slow, close1 := newTestSession(t)
	defer close1()
	mFast, mSlow := new(tunnelMeter), new(tunnelMeter)
	mFast.rxCapBps, mFast.busyAt = 5e6, time.Now()
	mSlow.rxCapBps, mSlow.busyAt = 0.3e6, time.Now().Add(-meterFresh-time.Second) // measured once, in slow start, long ago
	c := &Client{
		sessions:  []*yamux.Session{fast, slow},
		recvStamp: map[*yamux.Session]*tunnelMeter{fast: mFast, slow: mSlow},
		closeC:    make(chan struct{}),
	}
	require.Same(t, fast, c.pickSessionFor(pickRecv), "all idle: the stale estimate still says the fast tunnel")

	st, err := fast.Open()
	require.NoError(t, err)
	defer st.Close() //nolint:errcheck
	require.Same(t, slow, c.pickSessionFor(pickRecv), "a transfer is running: the stale idle tunnel is probed")

	// Once the slow tunnel carries a stream its own (fresh) estimate governs
	// again, and the fast tunnel with one stream still wins: 2/5e6 < 2/0.3e6.
	mSlow.busyAt = time.Now()
	st2, err := slow.Open()
	require.NoError(t, err)
	defer st2.Close() //nolint:errcheck
	require.Same(t, fast, c.pickSessionFor(pickRecv), "a busy tunnel is weighed by what it proves")
}
