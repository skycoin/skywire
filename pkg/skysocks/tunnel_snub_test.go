// Package skysocks pkg/skysocks/tunnel_snub_test.go
package skysocks

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// The snub table. Each row is a tunnel set as the evaluator sees it; the
// property under test is WHICH tunnels are snubbed, because that is what
// decides whether outstanding chunks are re-issued.
func TestPlanSnubs(t *testing.T) {
	const bound = 3 * time.Second
	silent := snubView{active: true, outstanding: 2, since: 4 * time.Second, bound: bound}
	moving := snubView{active: true, outstanding: 2, since: 300 * time.Millisecond, bound: bound}
	idle := snubView{active: true, outstanding: 0, since: time.Minute, bound: bound}

	for _, tc := range []struct {
		name         string
		views        []snubView
		snub, unsnub []int
	}{{
		name:  "a silent tunnel with work outstanding is snubbed while another is moving",
		views: []snubView{silent, moving},
		snub:  []int{0},
	}, {
		name:  "a slow but PROGRESSING tunnel is never snubbed, however deep its queue",
		views: []snubView{moving, moving},
	}, {
		name:  "silence just under the bound is not yet a snub",
		views: []snubView{{active: true, outstanding: 1, since: bound - time.Millisecond, bound: bound}, moving},
	}, {
		name:  "a tunnel holding NO work is not snubbed however long it has been quiet",
		views: []snubView{idle, moving},
	}, {
		name:  "EVERY active tunnel silent snubs nothing — the fault is not the tunnels'",
		views: []snubView{silent, silent, silent},
	}, {
		name:  "the last un-snubbed tunnel is never taken away either",
		views: []snubView{{active: true, snubbed: true, holdLeft: time.Second}, silent},
	}, {
		name:   "a served hold comes back for its one-chunk probe",
		views:  []snubView{{active: true, snubbed: true, holdLeft: -time.Millisecond}, moving},
		unsnub: []int{0},
	}, {
		name:  "a standby tunnel is outside the rule in both directions",
		views: []snubView{{active: false, outstanding: 2, since: time.Minute, bound: bound}, moving},
	}, {
		name:  "a lone active tunnel is never snubbed — there is nowhere to re-issue to",
		views: []snubView{silent},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			snub, unsnub := planSnubs(tc.views)
			require.Equal(t, tc.snub, snub, "snubbed")
			require.Equal(t, tc.unsnub, unsnub, "un-snubbed")
		})
	}
}

// The meter's own half: the bound is floored at twice the tunnel's smoothed
// RTT, so a far tunnel is judged on ITS round trip.
func TestSnubBoundFlooredByRTT(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	m := new(tunnelMeter)
	require.Equal(t, tunnelSnubAfter, m.snubBound(), "unmeasured: the knob governs")

	m.recordRTT(40 * time.Millisecond)
	require.Equal(t, tunnelSnubAfter, m.snubBound(), "a near tunnel: the knob still governs")

	m2 := new(tunnelMeter)
	m2.recordRTT(2500 * time.Millisecond)
	require.Equal(t, tunnelSnubAfter, m2.snubBound(),
		"at the default the knob outlasts any real RTT — no tunnel is 10s away")

	// The floor is what still governs once an operator sweeps the knob DOWN
	// live (`proxy settings tunnel.snub_after=...`): a far tunnel is judged on
	// ITS round trip and not on the rig's.
	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelSnubAfter: int64(3 * time.Second)}))
	require.Equal(t, 3*time.Second, new(tunnelMeter).snubBound(), "a near tunnel: the knob governs")
	require.Equal(t, 5*time.Second, m2.snubBound(), "a far tunnel: 2x its RTT")
}

// Progress is per TUNNEL, not per chunk: a chunk queued behind others rides on
// the bytes those others deliver. This is the whole difference from the
// per-chunk time-to-first-byte bound that thrashed.
func TestSinceProgressIsPerTunnel(t *testing.T) {
	m := new(tunnelMeter)
	t0 := time.Now()
	m.startWork(t0) // chunk A
	m.startWork(t0) // chunk B, queued behind it
	require.Equal(t, 2, m.outstandingWork())
	require.Equal(t, 5*time.Second, m.sinceProgress(t0.Add(5*time.Second)), "no byte yet: judged from the work")

	// A byte on chunk A refreshes the TUNNEL, so chunk B's own wait is invisible.
	m.noteChunkByte(t0.Add(4 * time.Second))
	require.Equal(t, time.Second, m.sinceProgress(t0.Add(5*time.Second)))

	// An upload ack does the same on the other side.
	m.noteUploadAck(t0.Add(5 * time.Second))
	require.Zero(t, m.sinceProgress(t0.Add(5*time.Second)))

	m.endWork()
	m.endWork()
	require.Zero(t, m.outstandingWork())
}

// A snub unblocks everything in flight on the tunnel, keeps it out of the
// picks, and after the hold returns it for exactly ONE chunk.
func TestSnubHoldAndOneChunkProbe(t *testing.T) {
	m := new(tunnelMeter)
	now := time.Now()
	ch := m.snubChan()

	require.True(t, m.snub(now, tunnelSnubHold))
	require.False(t, m.snub(now, tunnelSnubHold), "already snubbed")
	select {
	case <-ch:
	default:
		t.Fatal("the snub must unblock the chunks in flight on the tunnel")
	}
	require.True(t, m.isSnubbed())
	require.True(t, m.snubSitOut(), "no new chunk goes to a snubbed tunnel")

	require.True(t, m.unsnub())
	require.False(t, m.isSnubbed())
	require.False(t, m.snubSitOut(), "the probe is offered one chunk")
	select {
	case <-m.snubChan():
		t.Fatal("the fresh channel must not be closed")
	default:
	}

	m.startWork(time.Now())
	require.True(t, m.snubSitOut(), "and only one: the probe holds it now")
	m.noteChunkByte(time.Now())
	require.False(t, m.snubSitOut(), "a byte ends the probe and restores its share")
}

// A snubbed tunnel's chunk is re-issued FREE: no backoff, no budget charged —
// the same handling a tunnel death gets, because in both cases the bytes are
// still at the origin and the failure says nothing about the chunk.
func TestSnubbedChunkRetriesFree(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	require.True(t, freeRetry(fmt.Errorf("%w: aborted", errTunnelSnubbed)))
	require.True(t, freeRetry(fmt.Errorf("%w: gone", errSessionClosed)))
	require.False(t, freeRetry(errors.New("status 500")))

	tries := 0
	start := time.Now()
	buf, err := retryWithBudget(func() ([]byte, error) {
		if tries++; tries < 4 {
			return nil, fmt.Errorf("%w: chunk moved off a silent tunnel", errTunnelSnubbed)
		}
		return []byte("ok"), nil
	}, time.Millisecond, 50*time.Millisecond, time.Second)
	require.NoError(t, err)
	require.Equal(t, []byte("ok"), buf)
	require.Equal(t, 4, tries)
	require.Less(t, time.Since(start), 50*time.Millisecond, "a snub re-issues at once, it does not back off")
}

// The evaluator end to end over a live tunnel set: a silent tunnel holding work
// beside a moving one is snubbed, drops out of the picks (which is how its
// re-issued chunks land elsewhere), and comes back after the hold.
func TestEvaluateSnubsMovesChunksToTheMovingTunnel(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	silent, close0 := newTestSession(t)
	defer close0()
	moving, close1 := newTestSession(t)
	defer close1()

	now := time.Now()
	ms, mm := new(tunnelMeter), new(tunnelMeter)
	ms.rxCapBps, ms.busyAt, mm.rxCapBps, mm.busyAt = 5e6, now, 5e6, now
	c := &Client{
		sessions:  []*yamux.Session{silent, moving},
		recvStamp: map[*yamux.Session]*tunnelMeter{silent: ms, moving: mm},
		closeC:    make(chan struct{}),
	}

	// Both hold a chunk; only one of them is delivering bytes.
	ms.startWork(now.Add(-tunnelSnubAfter - time.Second))
	mm.startWork(now.Add(-tunnelSnubAfter - time.Second))
	mm.noteChunkByte(now.Add(-100 * time.Millisecond))

	c.evaluateSnubs(now)
	require.True(t, ms.isSnubbed(), "the silent tunnel is snubbed")
	require.False(t, mm.isSnubbed(), "the tunnel delivering bytes is not")
	require.Same(t, moving, c.pickSessionFor(pickRecv), "the re-issued chunk goes to the moving tunnel")
	ms.endWork() // the aborted chunk is booked off its tunnel, as the guard does

	// Before the hold is served the snub stands.
	c.evaluateSnubs(now.Add(tunnelSnubHold / 2))
	require.True(t, ms.isSnubbed())

	c.evaluateSnubs(now.Add(tunnelSnubHold + time.Millisecond))
	require.False(t, ms.isSnubbed(), "the hold is served")
	require.Same(t, silent, c.pickSessionFor(pickRecv), "and it is re-tried with one chunk")
}

// The wedge row of the 2026-09-18 two-tunnel/two-leg compose set. A route group
// is a reliable ORDERED stream: while its receive frontier is wedged behind a
// missing packet the app gets no chunk byte, no upload ack and not even the
// keepalive pong, so a head-of-line-blocked tunnel is indistinguishable from a
// dead one on app-local evidence — the router is the end that can still see the
// SACKs and the retransmits, and no app RPC carries that. What separates them
// is DURATION: those wedges cleared in 5 s to 16.5 s. The bound must outlast
// the longest of them, or a live tunnel is snubbed and the ~4 MB it still has
// outstanding is re-issued on its sibling while the original is in flight —
// six snub/unsnub pairs on one port, and 57.6-63.1 MB of wire for 50 MB.
func TestSnubBoundOutlastsAReorderWedge(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	require.Greater(t, tunnelSnubAfter, tunnelReorderWedgeClear,
		"the no-progress bound must sit above the longest measured wedge clear")
	require.Equal(t, tunnelSnubAfter, skysettings.Dur(skysettings.TunnelSnubAfter),
		"the registered knob default and the documented constant are one number")
	require.Equal(t, tunnelSnubAfter, new(tunnelMeter).snubBound(),
		"an unmeasured tunnel is judged on the knob")
}

// (a) A tunnel head-of-line blocked for the whole of the longest measured wedge
// is NOT snubbed, and (b) one that stays silent past the bound still is. The
// same two tunnels, the same evaluator, only the clock moves.
func TestEvaluateSnubsSparesAWedgedTunnelAndStillCatchesASilentOne(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	wedged, close0 := newTestSession(t)
	defer close0()
	moving, close1 := newTestSession(t)
	defer close1()

	t0 := time.Now()
	mw, mm := new(tunnelMeter), new(tunnelMeter)
	mw.rxCapBps, mw.busyAt, mm.rxCapBps, mm.busyAt = 5e6, t0, 5e6, t0
	c := &Client{
		sessions:  []*yamux.Session{wedged, moving},
		recvStamp: map[*yamux.Session]*tunnelMeter{wedged: mw, moving: mm},
		closeC:    make(chan struct{}),
	}
	mw.startWork(t0) // ~4 MB of chunks outstanding behind the frontier gap
	mm.startWork(t0)

	// (a) The wedge, sampled every tick right up to the longest one measured.
	// Nothing arrives on the wedged tunnel for any of it.
	for at := t0.Add(snubTick()); at.Sub(t0) <= tunnelReorderWedgeClear; at = at.Add(snubTick()) {
		mm.noteChunkByte(at) // the sibling keeps moving, so the liveness guard is not what spares it
		c.evaluateSnubs(at)
		require.False(t, mw.isSnubbed(),
			"a tunnel head-of-line blocked for %s must not be snubbed", at.Sub(t0))
	}

	// The wedge clears and the bytes the original attempt was always going to
	// deliver arrive — proof the re-issue would have been pure duplicate wire.
	cleared := t0.Add(tunnelReorderWedgeClear)
	mw.noteChunkByte(cleared)
	c.evaluateSnubs(cleared)
	require.False(t, mw.isSnubbed())

	// (b) Now it goes truly silent: no byte, no ack, past the bound.
	silentFor := cleared.Add(tunnelSnubAfter + time.Second)
	mm.noteChunkByte(silentFor)
	c.evaluateSnubs(silentFor)
	require.True(t, mw.isSnubbed(), "silence past the bound is still a snub")
	require.False(t, mm.isSnubbed())
	require.Same(t, moving, c.pickSessionFor(pickRecv), "and its chunks are re-issued on the sibling")
}
