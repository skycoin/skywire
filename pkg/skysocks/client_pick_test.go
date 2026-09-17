package skysocks

import (
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"
)

// TestTunnelMeterCapacity proves the meter keeps the peak rate a tunnel showed
// and forgets it only under load: an idle tunnel's proven capacity survives.
func TestTunnelMeterCapacity(t *testing.T) {
	m := new(tunnelMeter)
	t0 := time.Unix(1000, 0)
	m.sample(t0, false) // first sample only anchors
	m.rx.Add(1_000_000)
	m.tx.Add(250_000)
	m.sample(t0.Add(time.Second), true)
	require.InDelta(t, 1e6, m.capacity(pickRecv), 1)
	require.InDelta(t, 1.25e6, m.capacity(pickAny), 1)
	m.sample(t0.Add(2*time.Second), false) // idle: nothing moved, nothing forgotten
	require.InDelta(t, 1e6, m.capacity(pickRecv), 1)
	m.sample(t0.Add(3*time.Second), true) // busy and slower: the estimate decays
	require.InDelta(t, 0.9e6, m.capacity(pickRecv), 1)
	m.sample(t0.Add(3*time.Second+100*time.Millisecond), true) // too soon: no sample
	require.InDelta(t, 0.9e6, m.capacity(pickRecv), 1)
}

// TestPickSessionWeighsCapacity proves a new stream goes to the tunnel that
// would give it the most bandwidth — proven capacity for its direction over
// the streams already on the tunnel — while a tunnel with nothing proven is
// credited the best known capacity so it gets probed.
func TestPickSessionWeighsCapacity(t *testing.T) {
	s0, close0 := newTestSession(t)
	defer close0()
	s1, close1 := newTestSession(t)
	defer close1()
	direct, twoHop := new(tunnelMeter), new(tunnelMeter)
	direct.rxCapBps, direct.txCapBps = 2e6, 9e6 // the rig's direct tunnel: slow down, fast up
	twoHop.rxCapBps, twoHop.txCapBps = 3e6, 5e6
	c := &Client{
		sessions:  []*yamux.Session{s1, s0},
		recvStamp: map[*yamux.Session]*tunnelMeter{s0: direct, s1: twoHop},
		closeC:    make(chan struct{}),
	}
	require.Same(t, s0, c.pickSessionFor(pickAny), "a browser conn goes to the larger summed capacity")
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
	require.Same(t, s2, c.pickSessionFor(pickAny), "an unproven tunnel is probed ahead of a busy proven one")
}
