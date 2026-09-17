package skysocks

import (
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"
)

// TestPickAnyTakesTheLowestLatencyTunnel proves the policy for a LONE stream —
// a browser connection, an upload: it goes to the direct (lowest-RTT) tunnel,
// not to whichever tunnel has moved the most bytes.
//
// Measured live 2026-09-17: with a 470 ms Sydney tunnel beside a direct one,
// the capacity-scored pick sent all five 50 MB uploads of a set over Sydney —
// 0.23 MB/s against 9.8 on the direct tunnel — because a lone stream was
// scored on bytes moved, and downloads had moved plenty over Sydney. A range
// chunk is still capacity-weighted: it IS competing with sibling chunks for
// the pipe.
func TestPickAnyTakesTheLowestLatencyTunnel(t *testing.T) {
	near, closeNear := newTestSession(t)
	defer closeNear()
	far, closeFar := newTestSession(t)
	defer closeFar()

	now := time.Now()
	mNear, mFar := new(tunnelMeter), new(tunnelMeter)
	mNear.rttMs = 40
	mFar.rttMs = 300
	// ...and the FAR tunnel is the one with the proven download capacity.
	mNear.rxCapBps, mNear.busyAt = 1.0e6, now
	mFar.rxCapBps, mFar.busyAt = 9.0e6, now

	c := &Client{
		sessions:  []*yamux.Session{far, near},
		recvStamp: map[*yamux.Session]*tunnelMeter{near: mNear, far: mFar},
		closeC:    make(chan struct{}),
	}

	require.Same(t, near, c.pickSessionFor(pickAny),
		"a lone stream takes the lowest-latency tunnel whatever the other has moved")
	require.Same(t, near, c.pickSession(), "and pickSession is exactly that")
	require.Same(t, far, c.pickSessionFor(pickRecv),
		"a range chunk is still weighed on proven download capacity")

	// A benched tunnel (#4971) sits out the latency pick the same way it sits
	// out the capacity one.
	mNear.bench(time.Now())
	require.Same(t, far, c.pickSessionFor(pickAny),
		"the benched low-latency tunnel is skipped while another is live")
	mNear.unbench()
	require.Same(t, near, c.pickSessionFor(pickAny), "and is picked again once unbenched")

	// An unmeasured tunnel sorts last: it is unproven, not fast.
	mNear.rttMs = 0
	require.Same(t, far, c.pickSessionFor(pickAny), "a never-pinged tunnel does not win on a zero RTT")
}

// TestPickAnyIgnoresDownloadHistory proves the lone-stream pick has no decay
// or capacity input at all: after a long download-only run — which is what
// eroded the old score — the same tunnel still wins.
func TestPickAnyIgnoresDownloadHistory(t *testing.T) {
	near, closeNear := newTestSession(t)
	defer closeNear()
	far, closeFar := newTestSession(t)
	defer closeFar()

	mNear, mFar := new(tunnelMeter), new(tunnelMeter)
	mNear.rttMs, mFar.rttMs = 40, 300
	now := time.Unix(2000, 0)
	mNear.busyAt, mFar.busyAt = now, now

	c := &Client{
		sessions:  []*yamux.Session{far, near},
		recvStamp: map[*yamux.Session]*tunnelMeter{near: mNear, far: mFar},
		closeC:    make(chan struct{}),
	}
	require.Same(t, near, c.pickSessionFor(pickAny))

	// 34 s of downloads, the far tunnel delivering nine times as much.
	for i := 0; i < 68; i++ {
		now = now.Add(meterSampleMin)
		mNear.rx.Add(uint64(1.0e6 * meterSampleMin.Seconds()))
		mFar.rx.Add(uint64(9.0e6 * meterSampleMin.Seconds()))
		mNear.sample(now, true)
		mFar.sample(now, true)
	}

	require.Same(t, near, c.pickSessionFor(pickAny),
		"download history must not move a lone stream off the direct tunnel")
	require.Same(t, far, c.pickSessionFor(pickRecv), "the chunk pick did learn from it")
}

// TestTunnelMeterRTT covers the estimate itself: the first ping seeds it whole
// (an EWMA from zero would halve it) and later pings are folded in.
func TestTunnelMeterRTT(t *testing.T) {
	m := new(tunnelMeter)
	_, ok := m.rtt()
	require.False(t, ok, "a never-pinged tunnel has no RTT")

	m.recordRTT(400 * time.Millisecond)
	ms, ok := m.rtt()
	require.True(t, ok)
	require.InDelta(t, 400.0, ms, 0.1, "the first sample seeds the estimate whole")

	m.recordRTT(200 * time.Millisecond)
	ms, _ = m.rtt()
	require.InDelta(t, 400-tunnelRTTAlpha*200, ms, 0.1)

	m.recordRTT(0) // a failed ping carries no RTT and must not be folded in
	ms2, _ := m.rtt()
	require.Equal(t, ms, ms2)
}

// TestMeterForgetsAStalledDirection keeps the decay rule honest: a direction
// is decayed only when it moved bytes (a window that asked nothing of it
// proves nothing about it), but a busy window in which NOTHING moved is a
// stalled tunnel and both estimates decay then — so a capacity a tunnel stops
// delivering is still forgotten within a few seconds of load.
func TestMeterForgetsAStalledDirection(t *testing.T) {
	m := new(tunnelMeter)
	t0 := time.Unix(3000, 0)
	m.sample(t0, false)
	m.rx.Add(1_000_000)
	m.tx.Add(1_000_000)
	m.sample(t0.Add(time.Second), true)
	require.InDelta(t, 1e6, m.rxCapBps, 1)
	require.InDelta(t, 1e6, m.txCapBps, 1)

	// Upload-only load: the download estimate was not measured, so it stands.
	m.tx.Add(1_000_000)
	m.sample(t0.Add(2*time.Second), true)
	require.InDelta(t, 1e6, m.rxCapBps, 1, "a window that moved no rx proves nothing about rx")

	// Busy but delivering nothing either way: both decay.
	for i := 1; i <= 3; i++ {
		m.sample(t0.Add(time.Duration(2+i)*time.Second), true)
	}
	require.InDelta(t, 1e6*0.9*0.9*0.9, m.rxCapBps, 1)
	require.InDelta(t, 1e6*0.9*0.9*0.9, m.txCapBps, 1)
}

// TestPickAnySpreadsConcurrentStreams covers the load half of the lone-stream
// rule: the pick minimizes rtt × (open streams + 1), so a second simultaneous
// stream leaves the lowest-RTT tunnel unless the alternative is more than
// (n+1)× slower to answer.
//
// Measured live 2026-09-17 (bench/2026-09-16/66e279fa6-tunsum): one client
// with two tunnels (Amsterdam, Atlanta), two concurrent 50 MB downloads of
// different objects, no range split — both landed on the SAME tunnel in 3 of 3
// trials (>99.5 % of the 100 MB on one transport) and summed 5.07 / 8.90 /
// 9.10 MB/s, against 10.41 for two independent one-tunnel clients.
func TestPickAnySpreadsConcurrentStreams(t *testing.T) {
	near, closeNear := newTestSession(t)
	defer closeNear()
	far, closeFar := newTestSession(t)
	defer closeFar()

	mNear, mFar := new(tunnelMeter), new(tunnelMeter)
	mNear.rttMs, mFar.rttMs = 40, 100
	c := &Client{
		sessions:  []*yamux.Session{far, near},
		recvStamp: map[*yamux.Session]*tunnelMeter{near: mNear, far: mFar},
		closeC:    make(chan struct{}),
	}

	// Idle: identical to the plain lowest-RTT rule.
	require.Same(t, near, c.pickSessionFor(pickAny), "with both tunnels idle the lowest RTT wins")

	// One stream on the 40 ms tunnel: 40×2 = 80 still beats an idle 100 ms
	// tunnel, so a tunnel is not abandoned for a much worse one.
	_, err := near.Open()
	require.NoError(t, err)
	require.Same(t, near, c.pickSessionFor(pickAny), "100 ms is more than 2x worse than 40 ms")

	// ...but an idle 60 ms tunnel does take the second stream (60 < 80). This
	// is the case that regressed: before the load factor every concurrent lone
	// stream stacked on the single lowest-RTT tunnel.
	mFar.rttMs = 60
	require.Same(t, far, c.pickSessionFor(pickAny), "the second concurrent stream moves to the idle tunnel")

	// A second stream on the 40 ms tunnel (40×3 = 120) hands the next one to
	// the 100 ms tunnel as well.
	mFar.rttMs = 100
	_, err = near.Open()
	require.NoError(t, err)
	require.Same(t, far, c.pickSessionFor(pickAny), "two streams on 40 ms outweigh an idle 100 ms tunnel")

	// An idle 200 ms tunnel still loses to a 40 ms tunnel carrying two.
	mFar.rttMs = 200
	require.Same(t, near, c.pickSessionFor(pickAny), "an idle 200 ms tunnel loses to 40x3 = 120")

	// A benched tunnel sits out the loaded pick exactly as it does the idle
	// one, and the surviving tunnel is picked however loaded it is.
	mFar.rttMs = 60
	mFar.bench(time.Now())
	require.Same(t, near, c.pickSessionFor(pickAny), "a benched tunnel is skipped while another is live")
	mFar.unbench()

	// The only live tunnel is always picked, load and RTT notwithstanding.
	c.sessions = []*yamux.Session{near}
	require.Same(t, near, c.pickSessionFor(pickAny), "a single tunnel is picked whatever it carries")
}
