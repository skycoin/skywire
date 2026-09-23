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
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
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
	require.Contains(t, got[0].reason, "promoter: standby beat an unproductive active by 5.00x")
	require.Contains(t, got[0].reason, "goodput unmeasured vs unmeasured",
		"an RTT swap says so, and says that neither side had bytes behind it")

	require.Equal(t, router.MuxEventTunnelPromoted, got[1].event)
	require.EqualValues(t, 1001, got[1].port)
	require.Equal(t, TunnelRoleActive, got[1].role)
}

// The audition: while everything is idle, a standby whose capacity was never
// measured is offered the next SIBLING chunk stream — the only way a tunnel
// carrying nothing can ever get a capacity number (#4965), and it costs no
// extra bytes.
func TestPromoter_AuditionOffersASiblingChunkStreamToAnUnprovenStandby(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 100, 95) // inside the margin: a plausible candidate, not a swap
	defer cleanup()

	c.maybePromote()
	require.Equal(t, []*yamux.Session{standby}, c.auditionsArmed(), "the unproven standby is on offer")

	require.Same(t, standby, c.pickSessionKind(pickRecv, pickSibling), "and the next sibling chunk auditions it")
	require.Same(t, active, c.pickSessionKind(pickRecv, pickSibling), "one stream per offer; the next goes back to the active tunnel")
	require.True(t, c.IsStandby(standby), "an audition is a measurement, not a promotion")
}

// The audition must never take the ENTRY stream. It is the stream a browser
// connection is answered on, and for a splittable GET it is also chunk0 — the
// 2 MiB probe whose body is copied to the browser ahead of every other chunk —
// so a cold standby under it holds up the whole download. Measured on the rig
// 2026-09-16 (bench/2026-09-16/03ece1e95-smoke, mux-tunnels-2 rows 10 and 13):
// 5.1 and 5.3 MB/s against 8.3-8.4 for the rows either side of them.
func TestPromoter_AuditionNeverTakesTheEntryStream(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 100, 95)
	defer cleanup()

	c.maybePromote()
	require.Equal(t, []*yamux.Session{standby}, c.auditionsArmed())

	require.Same(t, active, c.pickSession(), "the entry stream — stream0, chunk0 of a splittable GET — stays on an active tunnel")
	require.Equal(t, []*yamux.Session{standby}, c.auditionsArmed(), "and the offer is not spent on it either: it waits")

	// stream0 is now in flight, which is the normal condition of every sibling
	// pick that follows it. The offer is still there for the first of them.
	st, err := active.Open()
	require.NoError(t, err)
	defer st.Close() //nolint:errcheck
	require.Eventually(t, func() bool { return active.NumStreams() == 1 }, time.Second, 5*time.Millisecond)

	require.Same(t, standby, c.pickSessionKind(pickRecv, pickSibling), "the first sibling chunk takes the audition")
	require.Same(t, active, c.pickSessionKind(pickRecv, pickSibling), "one stream per offer")
	require.True(t, c.IsStandby(standby), "an audition is a measurement, not a promotion")
}

// ...and it never touches a transfer: with any tunnel busy no offer is made.
func TestPromoter_NoAuditionIsOfferedWhileAnythingIsBusy(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 100, 95)
	defer cleanup()

	st, err := active.Open()
	require.NoError(t, err)
	defer st.Close() //nolint:errcheck
	require.Eventually(t, func() bool { return active.NumStreams() == 1 }, time.Second, 5*time.Millisecond)

	// No offer is made while the transfer runs, so nothing can consume one.
	for i := 0; i < 3; i++ {
		c.maybePromote()
		require.Empty(t, c.auditionsArmed(), "an audition is armed only from a quiet moment")
	}
	require.Same(t, active, c.pickSessionKind(pickRecv, pickSibling), "a busy client places streams as usual")
	require.True(t, c.IsStandby(standby))
}

// A standby whose GOODPUT is measured needs no audition: the audition exists
// to produce that number, and re-running it inside tunnel.goodput_fresh would
// spend a chunk stream re-learning what is already known.
func TestPromoter_NoAuditionForAProvenStandby(t *testing.T) {
	c, _, standby, cleanup := promoterClient(t, 100, 95)
	defer cleanup()
	setGoodput(c, standby, 8e6)

	c.maybePromote()
	require.Empty(t, c.auditionsArmed())
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
	c.sessionsMu.Unlock()
	require.Empty(t, c.auditionsArmed())
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

// A standby that is still carrying the stream of its own audition is BUSY, and
// the offer must not re-arm under it. armAudition used to scan only the active
// set for streams, and tunnelAuditionEvery is 60 s while a 50 MB upload on a
// bad leg runs for minutes — so on the rig 2026-09-17 (fe53f42dc) the same
// unproven standby was offered, and took, a second consecutive upload.
func TestPromoter_NoAuditionWhileTheAuditionedStandbyIsStillBusy(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 100, 95)
	defer cleanup()

	c.maybePromote()
	require.Equal(t, []*yamux.Session{standby}, c.auditionsArmed())
	require.Same(t, standby, c.pickSessionKind(pickRecv, pickSibling), "a sibling chunk stream takes the offer")

	st, err := standby.Open()
	require.NoError(t, err)
	require.Eventually(t, func() bool { return standby.NumStreams() == 1 }, time.Second, 5*time.Millisecond)

	// The transfer outlives the per-tunnel rate limit, so that guard is no
	// longer what keeps the offer from coming back.
	c.sessionsMu.Lock()
	c.auditionedAt[standby] = time.Now().Add(-2 * tunnelAuditionEvery)
	c.sessionsMu.Unlock()

	for i := 0; i < 3; i++ {
		c.maybePromote()
		require.Empty(t, c.auditionsArmed(), "a busy standby is in flight, not idle")
	}
	require.Same(t, active, c.pickSessionKind(pickRecv, pickSibling), "so the next chunk stream goes to the measured tunnel")
	require.NoError(t, st.Close())
}

// --- the capacity-aware rule -----------------------------------------------

// setGoodput gives one tunnel a MEASURED delivered goodput, the way an
// audition (or a spell in the active set) would have.
func setGoodput(c *Client, s *yamux.Session, bps float64) {
	c.sessionsMu.Lock()
	m := c.recvStamp[s]
	c.sessionsMu.Unlock()
	m.mu.Lock()
	m.gpBps, m.gpWins, m.gpAt = bps, tunnelGoodputMinWindows, time.Now()
	m.mu.Unlock()
}

// moveBytes puts n bytes on a tunnel's wire counters, so the next promoter
// tick sees them as traffic that happened since the previous one.
func moveBytes(c *Client, s *yamux.Session, n uint64) {
	c.sessionsMu.Lock()
	m := c.recvStamp[s]
	c.sessionsMu.Unlock()
	m.rx.Add(n)
}

// THE DEFECT, as a test. On the rig 2026-09-18 the promoter parked the tunnel
// that was carrying the object because a standby pinged 2.92x better (132 ms
// against 384 ms). RTT is a property of the path's length and says nothing
// about its width: here the near standby has been measured at a third of the
// far active tunnel's throughput, and no number of ticks may swap it in.
func TestPromoter_RTTBetterButSlowerStandbyIsNotPromoted(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 384, 132) // 2.91x on RTT
	defer cleanup()
	setGoodput(c, active, 9<<20)  // 9 MB/s — the tunnel carrying the bytes
	setGoodput(c, standby, 3<<20) // 3 MB/s — near, and a third as fat

	for i := 0; i < 10; i++ {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
	}
	require.True(t, c.IsStandby(standby), "a measured comparison decides, and RTT gets no second vote")
	require.False(t, c.IsStandby(active))
}

// ...and the same pair the other way round: when the standby has actually been
// measured delivering more, the swap is exactly what should happen — after the
// hold, like every other swap.
func TestPromoter_GoodputBetterStandbyIsPromotedAfterTheHold(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 40, 384) // RTT says KEEP the active one
	defer cleanup()
	setGoodput(c, active, 2<<20)
	setGoodput(c, standby, 9<<20) // 4.5x, clear of the 1.5x margin

	c.maybePromote()
	require.True(t, c.IsStandby(standby), "the first qualifying tick only starts the clock")

	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	require.False(t, c.IsStandby(standby), "held for the whole window; swap")
	require.True(t, c.IsStandby(active))
	require.Equal(t, 1, c.activeLiveCount(), "a swap, not a widening")
}

// An advantage inside the goodput margin is noise, however long it lasts.
func TestPromoter_GoodputInsideTheMarginNeverSwaps(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 200, 40)
	defer cleanup()
	setGoodput(c, active, 6<<20)
	setGoodput(c, standby, 8<<20) // 1.33x — inside 1.5x

	for i := 0; i < 10; i++ {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
	}
	require.True(t, c.IsStandby(standby),
		"1.33x is inside the goodput margin, and the 5x RTT advantage does not rescue it")
	require.False(t, c.IsStandby(active))
}

// The mid-transfer guard, which is the case yamux stream counts miss. A
// striped upload opens and closes a stream per chunk, so between two chunks
// the tunnel carrying the whole object reads as idle — and a promoter tick
// landing in that gap used to park it. Bytes on the wire have no such gap.
func TestPromoter_NoSwapWhileBytesAreMovingOnTheActiveTunnel(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 200, 40)
	defer cleanup()

	// A first tick establishes the byte baseline. The active tunnel is idle
	// and unmeasured here, so the RTT rule is live and would otherwise swap.
	c.maybePromote()
	require.True(t, c.IsStandby(standby))

	// Now the upload is in flight: no stream is open at this instant, but the
	// tunnel has moved a chunk's worth of bytes since the last tick.
	for i := 0; i < 10; i++ {
		moveBytes(c, active, uint64(tunnelPromoteQuietBytes)*4) //nolint:gosec // a positive test constant
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
		require.Zero(t, active.NumStreams(), "the gap between two chunks: no stream is open")
	}
	require.True(t, c.IsStandby(standby), "a tunnel moving bytes is mid-transfer, and mid-transfer is never parked")

	// The upload finishes. One quiet tick and the same advantage goes through.
	c.maybePromote()
	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	require.False(t, c.IsStandby(standby), "between transfers the swap is free, and it happens")
}

// Outstanding chunk or upload work is mid-transfer too, even before its first
// byte: a chunk that has been handed to a tunnel and is waiting on the first
// round trip moves nothing yet, and parking under it is the same defect.
func TestPromoter_NoSwapWhileTheActiveTunnelHoldsOutstandingWork(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 200, 40)
	defer cleanup()
	c.sessionsMu.Lock()
	m := c.recvStamp[active]
	c.sessionsMu.Unlock()
	m.startWork(time.Now())

	for i := 0; i < 10; i++ {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
	}
	require.True(t, c.IsStandby(standby), "a chunk is outstanding on it; the swap waits")

	m.endWork()
	c.maybePromote()
	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	require.False(t, c.IsStandby(standby))
}

// pool.freeze stops every discretionary swap, the capacity-driven one
// included: the operator is holding the active set still.
func TestPromoter_FreezeBlocksAGoodputSwap(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c, active, standby, cleanup := promoterClient(t, 200, 40)
	defer cleanup()
	setGoodput(c, active, 1<<20)
	setGoodput(c, standby, 9<<20) // 9x: as clear as an advantage gets

	require.True(t, skysettings.Apply(map[string]int64{skysettings.PoolFreeze: 1}))
	for i := 0; i < 10; i++ {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
	}
	require.True(t, c.IsStandby(standby), "frozen: no discretionary swap, whatever was measured")

	require.True(t, skysettings.Reset())
	c.maybePromote()
	backdatePromoteClock(c, tunnelPromoteHold)
	c.maybePromote()
	require.False(t, c.IsStandby(standby), "un-frozen, the same advantage swaps")
}

// ...but a DEAD active tunnel is replaced in the same tick regardless: the
// failover path is untouched by any of this, freeze and measurements alike.
func TestPromoter_DeadActiveIsReplacedEvenFrozen(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c, active, standby, cleanup := promoterClient(t, 40, 200) // the standby is WORSE; only a failover explains a promote
	defer cleanup()
	setGoodput(c, active, 9<<20)
	require.True(t, skysettings.Apply(map[string]int64{skysettings.PoolFreeze: 1}))

	require.NoError(t, active.Close())
	require.Eventually(t, active.IsClosed, time.Second, 5*time.Millisecond)

	c.maybePromote()
	require.False(t, c.IsStandby(standby), "a dead active is replaced on the very tick that sees it, frozen or not")
	require.Equal(t, 1, c.activeLiveCount())
}

// The rule itself, as a table. Everything above drives it through a live
// client; this pins the decision on its own.
func TestChooseSwap(t *testing.T) {
	gp := func(bps float64, rtt float64) tunnelCandidate {
		return tunnelCandidate{gp: bps, gpOK: true, rtt: rtt}
	}
	unmeasured := func(rtt float64) tunnelCandidate { return tunnelCandidate{rtt: rtt} }

	t.Run("goodput decides and RTT does not overrule it", func(t *testing.T) {
		worst, best, byGoodput := chooseSwap(
			[]tunnelCandidate{gp(9<<20, 384)}, []tunnelCandidate{gp(3<<20, 132)})
		require.Nil(t, worst)
		require.Nil(t, best)
		require.False(t, byGoodput)
	})
	t.Run("a measured advantage past the margin swaps", func(t *testing.T) {
		worst, best, byGoodput := chooseSwap(
			[]tunnelCandidate{gp(2<<20, 40)}, []tunnelCandidate{gp(9<<20, 384)})
		require.NotNil(t, worst)
		require.NotNil(t, best)
		require.True(t, byGoodput)
	})
	t.Run("an active measured delivering nothing is replaced by any measured standby", func(t *testing.T) {
		_, best, byGoodput := chooseSwap(
			[]tunnelCandidate{gp(0, 40)}, []tunnelCandidate{gp(1<<20, 900)})
		require.NotNil(t, best)
		require.True(t, byGoodput)
	})
	t.Run("RTT decides only when the active was never measured productive", func(t *testing.T) {
		worst, best, byGoodput := chooseSwap(
			[]tunnelCandidate{unmeasured(200)}, []tunnelCandidate{unmeasured(40)})
		require.NotNil(t, worst)
		require.NotNil(t, best)
		require.False(t, byGoodput)
	})
	t.Run("a productive active is never parked on RTT alone", func(t *testing.T) {
		worst, _, _ := chooseSwap(
			[]tunnelCandidate{gp(9<<20, 384)}, []tunnelCandidate{unmeasured(40)})
		require.Nil(t, worst, "the standby has no measurement to beat 9 MB/s with")
	})
	t.Run("a trickling active is below the productive floor, so RTT may still park it", func(t *testing.T) {
		worst, _, byGoodput := chooseSwap(
			[]tunnelCandidate{gp(float64(tunnelPromoteIdleBps)/2, 200)}, []tunnelCandidate{unmeasured(40)})
		require.NotNil(t, worst)
		require.False(t, byGoodput)
	})
	t.Run("a carrying active is not a candidate at all", func(t *testing.T) {
		a := gp(0, 900)
		a.carrying = true
		worst, _, _ := chooseSwap([]tunnelCandidate{a}, []tunnelCandidate{gp(9<<20, 40)})
		require.Nil(t, worst)
	})
	t.Run("the weakest active is the one given up", func(t *testing.T) {
		worst, _, byGoodput := chooseSwap(
			[]tunnelCandidate{gp(9<<20, 100), gp(1<<20, 50)}, []tunnelCandidate{gp(8<<20, 300)})
		require.NotNil(t, worst)
		require.EqualValues(t, 1<<20, worst.gp, "the 1 MB/s tunnel goes, not the 9 MB/s one beside it")
		require.True(t, byGoodput)
	})
}

// Two tunnels within a whisker of each other must not trade places for ever.
// The margin, the hold and the park hold are three separate reasons they
// cannot, and this drives all three over many ticks.
func TestPromoter_MeasuredTunnelsDoNotPingPong(t *testing.T) {
	c, active, standby, cleanup := promoterClient(t, 100, 100)
	defer cleanup()

	// swaps counts every change of role across the whole run.
	was := c.IsStandby(standby)
	swaps := 0
	tick := func() {
		c.maybePromote()
		backdatePromoteClock(c, 2*tunnelPromoteHold)
		if now := c.IsStandby(standby); now != was {
			swaps++
			was = now
		}
	}

	// The standby is better, but only by 1.4x — inside the 1.5x margin.
	setGoodput(c, active, 5<<20)
	setGoodput(c, standby, 7<<20)
	for i := 0; i < 40; i++ {
		tick()
	}
	require.True(t, c.IsStandby(standby))
	require.Zero(t, swaps, "inside the margin nothing moves at all")

	// Now make it clearly better, and reverse the numbers the moment it wins.
	// One swap follows and the park hold refuses the trade back, so the pair
	// settles instead of oscillating however many ticks it is given.
	setGoodput(c, standby, 9<<20)
	for i := 0; i < 40; i++ {
		tick()
		if !c.IsStandby(standby) {
			setGoodput(c, active, 9<<20) // the parked tunnel now looks best
			setGoodput(c, standby, 1<<20)
		}
	}
	require.Equal(t, 1, swaps, "exactly one swap, and no trade back inside the park hold")
}

// --- the parallel audition ---------------------------------------------------

// auditionClient builds a Client with one active tunnel and n unproven
// standbys, each with a distinct RTT and a distinct last-measured time so the
// audition order is decidable. measuredAgo[i] is how long ago standby i's
// goodput last moved; a zero duration means it never has.
func auditionClient(t *testing.T, rtts []float64, measuredAgo []time.Duration) (*Client, []*yamux.Session) {
	t.Helper()
	c := &Client{closeC: make(chan struct{}), streams: map[uint32]streamMeta{}}
	c.recvStamp = map[*yamux.Session]*tunnelMeter{}
	c.standby = map[*yamux.Session]bool{}
	now := time.Now()

	mk := func(rtt float64, port routing.Port, standby bool, ago time.Duration) *yamux.Session {
		s := newClosingPeerSession(t)
		m := new(tunnelMeter)
		m.port = port
		m.rttMs = rtt
		m.rttWin.push(rtt, now)
		m.stamp.Store(now.UnixNano())
		if ago > 0 {
			// Measured, but long enough ago that goodput() no longer counts it
			// — an unproven standby with a stale number behind it.
			m.gpBps, m.gpWins, m.gpAt = 1e6, tunnelGoodputMinWindows, now.Add(-ago)
		}
		c.sessions = append(c.sessions, s)
		c.recvStamp[s] = m
		if standby {
			c.standby[s] = true
		}
		return s
	}
	mk(100, 1000, false, 0)
	out := make([]*yamux.Session, 0, len(rtts))
	for i, rtt := range rtts {
		out = append(out, mk(rtt, routing.Port(1001+i), true, measuredAgo[i])) //nolint:gosec
	}
	c.SetTunnelTarget(1)
	return c, out
}

// tunnel.audition_parallel offers may stand at once. One at a time measured a
// pool at one tunnel per tunnel.audition_every (60 s), so a pool of eight
// needed eight minutes of unbroken idleness — longer than the gap between two
// transfers ever is, which is how the spread policy kept finding unmeasured
// routes to promote.
func TestPromoter_AuditionsUpToTheParallelBound(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c, standbys := auditionClient(t,
		[]float64{90, 95, 92, 99, 91},
		[]time.Duration{0, 0, 0, 0, 0})

	c.maybePromote()
	require.Len(t, c.auditionsArmed(), tunnelAuditionParallel, "three offers, not one and not five")

	// ...and the bound is a live knob, not a constant.
	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelAuditionParallel: 5}))
	c.maybePromote()
	require.Len(t, c.auditionsArmed(), 5)
	require.Len(t, standbys, 5)

	// One stream per offer, and each offer goes to a different tunnel.
	seen := map[*yamux.Session]bool{}
	for i := 0; i < 5; i++ {
		s := c.pickSessionKind(pickRecv, pickSibling)
		require.True(t, c.IsStandby(s), "each sibling chunk takes one standby's offer")
		require.False(t, seen[s], "an offer is consumed, so no tunnel auditions twice")
		seen[s] = true
	}
	require.Empty(t, c.auditionsArmed(), "every offer spent")
	require.False(t, c.IsStandby(c.pickSessionKind(pickRecv, pickSibling)), "and the next chunk goes back to the active tunnel")
}

// The offers go to the standbys measured LONGEST ago — never measured first —
// so a pool rotates rather than re-auditioning whatever pings best.
func TestPromoter_AuditionsTheOldestMeasuredStandbysFirst(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	// The never-measured tunnel pings WORST, so an RTT-ordered pick would put
	// it last of the four; the two measured recently ping best.
	//
	// Every measured age is past the goodput freshness window, so all four are
	// unproven and only the ordering decides. The ages used to be 1 and 2
	// minutes against a 2-minute window: "recent" was then proven outright, and
	// "older" sat exactly on the boundary — stale only by the nanoseconds
	// between this fixture and maybePromote. Windows' coarse clock made those
	// nanoseconds zero, "older" counted as proven, and two were armed, not three.
	fresh := setTunnelGoodputFresh()
	c, sb := auditionClient(t,
		[]float64{99, 40, 45, 90},
		[]time.Duration{0, fresh + time.Minute, fresh + 2*time.Minute, fresh + 10*time.Minute})
	never, recent, older, oldest := sb[0], sb[1], sb[2], sb[3]

	c.maybePromote()
	armed := c.auditionsArmed()
	require.Len(t, armed, tunnelAuditionParallel)
	require.Contains(t, armed, never, "a tunnel that has never been measured is the oldest there is")
	require.Contains(t, armed, oldest)
	require.Contains(t, armed, older)
	require.NotContains(t, armed, recent, "the most recently measured standby waits its turn")
}

// Every existing rail still holds with several offers standing: nothing is
// armed while ANY tunnel is busy, and a proven standby is never a candidate.
func TestPromoter_ParallelAuditionsAreStillIdleOnlyAndUnprovenOnly(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c, sb := auditionClient(t,
		[]float64{90, 95, 92},
		[]time.Duration{0, 0, 0})

	// One standby has a FRESH measurement: an audition would teach nothing.
	setGoodput(c, sb[0], 8e6)
	c.maybePromote()
	armed := c.auditionsArmed()
	require.Len(t, armed, 2)
	require.NotContains(t, armed, sb[0], "a proven standby is not a candidate")

	// Something goes busy: no further offer is armed, however much room the
	// parallel bound leaves.
	c.sessionsMu.Lock()
	c.auditions = map[*yamux.Session]time.Time{}
	c.auditionedAt = map[*yamux.Session]time.Time{}
	c.sessionsMu.Unlock()
	st, err := sb[1].Open()
	require.NoError(t, err)
	defer st.Close() //nolint:errcheck
	require.Eventually(t, func() bool { return sb[1].NumStreams() == 1 }, time.Second, 5*time.Millisecond)

	for i := 0; i < 3; i++ {
		c.maybePromote()
		require.Empty(t, c.auditionsArmed(), "an audition is armed only from a quiet moment")
	}
}
