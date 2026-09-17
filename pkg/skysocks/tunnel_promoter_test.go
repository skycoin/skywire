package skysocks

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// newClosingPeerSession is newTestSession with one difference that matters
// here: the peer mirrors the client's FIN by closing its own end of the
// stream, so NumStreams() drops back to 0 when the client closes a stream.
// yamux keeps a half-closed stream in its map, and the "a swap waits for the
// streams to drain" rule is judged on exactly that count.
func newClosingPeerSession(t *testing.T) *yamux.Session {
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
			go func() {
				_, _ = io.Copy(io.Discard, st) //nolint:errcheck
				_ = st.Close()                 //nolint:errcheck
			}()
		}
	}()
	csess, err := yamux.Client(a, yamux.DefaultConfig())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = csess.Close() //nolint:errcheck
		_ = ssess.Close() //nolint:errcheck
		_ = a.Close()     //nolint:errcheck
		_ = b.Close()     //nolint:errcheck
	})
	return csess
}

// promoterClient builds a Client with one active tunnel at activeRTT and one
// standby at standbyRTT (windowed MINIMUM ping, the statistic the promoter
// reads), each with a distinct local port so a switch can be attributed.
func promoterClient(t *testing.T, activeRTT, standbyRTT float64) (*Client, *yamux.Session, *yamux.Session, func()) {
	t.Helper()
	c := &Client{closeC: make(chan struct{}), streams: map[uint32]streamMeta{}}
	c.recvStamp = map[*yamux.Session]*tunnelMeter{}
	c.standby = map[*yamux.Session]bool{}
	now := time.Now()

	mk := func(rtt float64, port routing.Port, standby bool) *yamux.Session {
		s := newClosingPeerSession(t)
		m := new(tunnelMeter)
		m.port = port
		m.rttMs = rtt
		m.rttWin.push(rtt, now)
		m.stamp.Store(now.UnixNano())
		c.sessions = append(c.sessions, s)
		c.recvStamp[s] = m
		if standby {
			c.standby[s] = true
		}
		return s
	}
	active := mk(activeRTT, 1000, false)
	standby := mk(standbyRTT, 1001, true)
	c.SetTunnelTarget(1)
	return c, active, standby, func() {}
}

// backdatePromoteClock pretends the current candidate has qualified for d, so
// a test can reach the hold without sleeping through it.
func backdatePromoteClock(c *Client, d time.Duration) {
	c.sessionsMu.Lock()
	for k := range c.promoteSince {
		c.promoteSince[k] = time.Now().Add(-d)
	}
	c.sessionsMu.Unlock()
}

// The margin: an advantage smaller than tunnelPromoteMargin is noise on a mesh
// whose route latency moves by more than 25 % inside an hour, and no number of
// ticks turns it into a swap.
func TestPromoter_SmallAdvantageNeverSwaps(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 100, 85) // 1.18x
	defer cleanup()

	for i := 0; i < 10; i++ {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
	}
	require.True(t, c.IsStandby(standby), "1.18x is inside the margin")
	require.False(t, c.IsStandby(active))
}

// ...and an advantage that clears the margin still has to LAST: the hold is
// what stopped the leg-level flap of #4968.
func TestPromoter_ClearAdvantageSwapsOnlyAfterTheHold(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 200, 40) // 5x
	defer cleanup()

	c.maybePromote()
	require.True(t, c.IsStandby(standby), "the first qualifying tick only starts the clock")
	c.maybePromote()
	require.True(t, c.IsStandby(standby), "still inside the hold")

	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	require.False(t, c.IsStandby(standby), "held for the whole window; swap")
	require.True(t, c.IsStandby(active), "and the tunnel it beat is parked, not dropped")
	require.Equal(t, 1, c.activeLiveCount(), "the active width is unchanged — this is a swap, not a widening")
}

// An advantage that lapses starts over. Without this a candidate that
// qualified once an hour ago would swap in on its next lucky tick.
func TestPromoter_LapsedAdvantageResetsTheHold(t *testing.T) {
	c, _, standby, cleanup := promoterClient(t, 200, 40)
	defer cleanup()

	c.maybePromote() // clock starts
	c.sessionsMu.Lock()
	require.Len(t, c.promoteSince, 1)
	// The standby's advantage evaporates: its good samples age out of the
	// window and what is left is worse than the active tunnel's.
	c.recvStamp[standby].rttWin = tunnelRTTWindow{}
	c.recvStamp[standby].rttWin.push(500, time.Now())
	c.sessionsMu.Unlock()

	c.maybePromote()
	c.sessionsMu.Lock()
	require.Empty(t, c.promoteSince, "the clock is dropped the moment the advantage lapses")
	c.sessionsMu.Unlock()
	require.True(t, c.IsStandby(standby))
}

// The park hold: a tunnel just parked cannot be promoted again inside
// tunnelParkMinHold, so two tunnels within a whisker of each other cannot
// trade places every tick.
func TestPromoter_ParkHoldKeepsAJustParkedTunnelOut(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 200, 40)
	defer cleanup()

	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	require.False(t, c.IsStandby(standby), "the swap happened")
	require.True(t, c.IsStandby(active))

	// Now make the PARKED tunnel look much better and ask again: the hold
	// keeps it out however good it looks.
	c.sessionsMu.Lock()
	c.recvStamp[active].rttWin.push(5, time.Now())
	c.sessionsMu.Unlock()
	for i := 0; i < 10; i++ {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
	}
	require.True(t, c.IsStandby(active), "inside tunnelParkMinHold a parked tunnel is not a candidate")

	// Past the hold it is a candidate again.
	c.sessionsMu.Lock()
	c.parkedAt[active] = time.Now().Add(-2 * tunnelParkMinHold)
	c.sessionsMu.Unlock()
	c.maybePromote()
	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	require.False(t, c.IsStandby(active), "past the hold, the better tunnel comes back")
}

// Idle only: an active tunnel carrying a stream is not a swap candidate at
// all, so a transfer in flight is never disturbed — the swap waits for the
// streams to drain.
func TestPromoter_NeverSwapsWhileTheActiveTunnelCarriesAStream(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 200, 40)
	defer cleanup()

	st, err := active.Open()
	require.NoError(t, err)
	require.Eventually(t, func() bool { return active.NumStreams() == 1 }, time.Second, 5*time.Millisecond)

	for i := 0; i < 10; i++ {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
	}
	require.True(t, c.IsStandby(standby), "the swap is deferred until the transfer drains")

	// Drained: the same advantage now swaps.
	require.NoError(t, st.Close())
	require.Eventually(t, func() bool { return active.NumStreams() == 0 }, time.Second, 5*time.Millisecond)
	c.maybePromote()
	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	require.False(t, c.IsStandby(standby), "with the tunnel idle the swap goes through")
}

// A standby that has stopped answering is not swapped in voluntarily, however
// good its last recorded ping: that is the "silently black-holing standby
// promoted into a transfer" case. (A FAILOVER still takes it — see
// promoteBestStandby — because then the alternative is no tunnel at all.)
func TestPromoter_StaleStandbyIsNotAVoluntaryCandidate(t *testing.T) {
	c, _, standby, cleanup := promoterClient(t, 200, 20)
	defer cleanup()
	c.sessionsMu.Lock()
	c.recvStamp[standby].stamp.Store(time.Now().Add(-10 * standbyRTTStale).UnixNano())
	c.sessionsMu.Unlock()

	for i := 0; i < 5; i++ {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
	}
	require.True(t, c.IsStandby(standby))
}

// Both switches are reported, with the numbers that decided them, so churn is
// countable in both directions from `visor state`.
func TestPromoter_SwapReportsAParkAndAPromote(t *testing.T) {
	c, _, _, cleanup := promoterClient(t, 200, 40)
	defer cleanup()
	rec := newNoteRecorder(2)
	c.muxNote = rec.note
	c.startMuxNotes()
	defer close(c.closeC)

	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()

	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("the swap was not reported: %+v", rec.events())
	}
	got := rec.events()
	require.Equal(t, router.MuxEventTunnelParked, got[0].event, "the park comes first: the active set is never momentarily two wide")
	require.EqualValues(t, 1000, got[0].port)
	require.Equal(t, TunnelRoleStandby, got[0].role)
	require.Contains(t, got[0].reason, "promoter: standby beat active by 5.00x")

	require.Equal(t, router.MuxEventTunnelPromoted, got[1].event)
	require.EqualValues(t, 1001, got[1].port)
	require.Equal(t, TunnelRoleActive, got[1].role)
}

// The audition: while everything is idle, a standby whose capacity was never
// measured is offered the next lone stream — the only way a tunnel carrying
// nothing can ever get a capacity number (#4965), and it costs no extra bytes.
func TestPromoter_AuditionOffersALoneStreamToAnUnprovenStandby(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 100, 95) // inside the margin: a plausible candidate, not a swap
	defer cleanup()

	c.maybePromote()
	c.sessionsMu.Lock()
	armed := c.audition
	c.sessionsMu.Unlock()
	require.Same(t, standby, armed, "the unproven standby is on offer")

	require.Same(t, standby, c.pickSessionFor(pickAny), "and the next lone stream auditions it")
	require.Same(t, active, c.pickSessionFor(pickAny), "one stream per offer; the next goes back to the active tunnel")
	require.True(t, c.IsStandby(standby), "an audition is a measurement, not a promotion")
}

// ...and it never touches a transfer: with any tunnel busy the offer is not
// made, and a standing offer is withdrawn.
func TestPromoter_AuditionIsWithdrawnWhileAnythingIsBusy(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 100, 95)
	defer cleanup()

	c.maybePromote()
	c.sessionsMu.Lock()
	require.Same(t, standby, c.audition)
	c.sessionsMu.Unlock()

	st, err := active.Open()
	require.NoError(t, err)
	defer st.Close() //nolint:errcheck
	require.Eventually(t, func() bool { return active.NumStreams() == 1 }, time.Second, 5*time.Millisecond)

	require.Same(t, active, c.pickSessionFor(pickAny), "a busy client places streams as usual")
	c.sessionsMu.Lock()
	require.Nil(t, c.audition, "the offer is withdrawn rather than deferred")
	c.sessionsMu.Unlock()

	// And no new offer is made while the transfer runs.
	c.maybePromote()
	c.sessionsMu.Lock()
	require.Nil(t, c.audition)
	c.sessionsMu.Unlock()
}

// A standby whose capacity IS proven needs no audition.
func TestPromoter_NoAuditionForAProvenStandby(t *testing.T) {
	c, _, standby, cleanup := promoterClient(t, 100, 95)
	defer cleanup()
	c.sessionsMu.Lock()
	m := c.recvStamp[standby]
	m.mu.Lock()
	m.rxCapBps, m.busyAt = 8e6, time.Now()
	m.mu.Unlock()
	c.sessionsMu.Unlock()

	c.maybePromote()
	c.sessionsMu.Lock()
	require.Nil(t, c.audition)
	c.sessionsMu.Unlock()
}

// A client with no pool is inert: the promoter must cost a proxy running
// --standby-pool 0 nothing but the call.
func TestPromoter_NoPoolIsANoOp(t *testing.T) {
	c, _, cleanup := failoverClient(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		c.maybePromote()
	}
	require.Equal(t, 1, c.activeLiveCount())
	c.sessionsMu.Lock()
	require.Empty(t, c.promoteSince)
	require.Nil(t, c.audition)
	c.sessionsMu.Unlock()
}

// The failover is answered by the FIRST tick that can see the session closed,
// and the promoter never gets to deliberate ahead of it.
//
// Measured on the rig 2026-09-17: the same first-hop cut was answered in 1.2 s
// in one run and in 13 s in the next, because only the liveness ticker (15 s)
// retired a self-closed session while the 5 s ticks merely observed it. Here
// the promoter tick IS the first tick, and the standby is active before it
// decides anything: the promote reason must be the failover's, never the
// promoter's.
func TestPromoter_ATickThatSeesTheCloseRetiresBeforeItDecides(t *testing.T) {
	// The standby is WORSE than the active tunnel, so the promoter would never
	// choose it — only the failover can explain it carrying streams.
	c, active, standby, cleanup := promoterClient(t, 40, 200)
	defer cleanup()
	rec := newNoteRecorder(2)
	c.muxNote = rec.note
	c.startMuxNotes()
	defer close(c.closeC)

	require.NoError(t, active.Close())
	require.Eventually(t, active.IsClosed, time.Second, 5*time.Millisecond)

	c.maybePromote() // the promoter tick is the first to see it
	require.False(t, c.IsStandby(standby), "promoted on the very tick that saw the close")
	require.Equal(t, 1, c.activeLiveCount())
	require.Same(t, standby, c.pickSessionFor(pickAny))

	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("the failover was not reported: %+v", rec.events())
	}
	got := rec.events()
	require.Equal(t, router.MuxEventTunnelRetired, got[0].event)
	require.Equal(t, "tunnel session closed", got[0].reason)
	require.Equal(t, router.MuxEventTunnelPromoted, got[1].event)
	require.Equal(t, "failover: active tunnel died", got[1].reason,
		"the retire owns this promote; a promoter decision would have had to wait for the hold")

	// Later ticks must not spend another standby on the same corpse.
	for i := 0; i < 5; i++ {
		c.maybePromote()
	}
	require.Len(t, rec.events(), 2, "the retire is once-only")
}

// The sweep is what every loop branch calls, and it answers each death once.
func TestSweepClosedTunnels_RetiresEachTunnelOnce(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 40, 200)
	defer cleanup()

	require.Zero(t, c.sweepClosedTunnels(), "nothing closed, nothing to do")
	require.NoError(t, active.Close())
	require.Eventually(t, active.IsClosed, time.Second, 5*time.Millisecond)

	require.Equal(t, 1, c.sweepClosedTunnels())
	require.Zero(t, c.sweepClosedTunnels(), "the second sweep finds the ledger already closed")
	require.False(t, c.IsStandby(standby))
}

// The window, not the latest sample, is the statistic. A tunnel whose ping
// spikes under its own load keeps the small samples that prove what the path
// really costs — the escape that ended the leg-level park/promote every 30 s.
func TestTunnelRTTWindow_MinimumSurvivesASpike(t *testing.T) {
	now := time.Now()
	var w tunnelRTTWindow
	for _, ms := range []float64{37, 136, 771, 955, 435} {
		w.push(ms, now)
	}
	require.EqualValues(t, 37, w.minMs(now))

	// Past the window the old minimum is gone and the recent truth stands.
	later := now.Add(2 * tunnelRTTMinWindow)
	w.push(400, later)
	require.EqualValues(t, 400, w.minMs(later))

	// Non-positive samples are not samples.
	w.push(0, later)
	w.push(-1, later)
	require.EqualValues(t, 400, w.minMs(later))
}
