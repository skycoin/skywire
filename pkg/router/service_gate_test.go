package router

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// A signal with nothing parked must not block and must not strand a waiter that
// arrives afterwards.
func TestServiceWakeSignalWithNobodyParked(t *testing.T) {
	var w serviceWake
	w.signal() // no-op: parked == 0

	w.parked.Add(1)
	ch := w.wait()
	w.signal()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("waiter registered after an idle signal was never released")
	}
}

// One signal must release every parked loop, not just one of them.
func TestServiceWakeBroadcastsToAllWaiters(t *testing.T) {
	var w serviceWake
	const waiters = 8

	var wg sync.WaitGroup
	w.parked.Add(waiters)
	chans := make([]<-chan struct{}, waiters)
	for i := range chans {
		chans[i] = w.wait()
	}
	// Every waiter shares the same generation, so all of them see the close.
	for i := range chans {
		wg.Add(1)
		go func(ch <-chan struct{}) {
			defer wg.Done()
			select {
			case <-ch:
			case <-time.After(2 * time.Second):
				t.Error("parked waiter was not released by the broadcast")
			}
		}(chans[i])
	}

	w.signal()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("broadcast did not release all waiters")
	}
}

// wait() after a signal must hand out a FRESH channel, or the next park would
// return instantly forever.
func TestServiceWakeInstallsFreshChannel(t *testing.T) {
	var w serviceWake
	w.parked.Add(1)
	first := w.wait()
	w.signal()
	<-first // closed

	second := w.wait()
	select {
	case <-second:
		t.Fatal("channel installed after a signal was already closed")
	default:
	}
}

// A gated loop must not run its fn while the gate says dormant, and must run it
// promptly once the gate opens and the group is signaled.
func TestServiceKnobLoopGatedParksAndWakes(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	t.Cleanup(func() { require.NoError(t, rg.Close()) })

	var dormant atomic.Bool
	dormant.Store(true)

	var runs atomic.Int32
	go rg.serviceKnobLoopGated("test", routersettings.TLPCheckInterval,
		func(time.Duration) { runs.Add(1) },
		func() bool { return dormant.Load() })

	// TLPCheckInterval is 100ms; several intervals must pass with no run.
	time.Sleep(500 * time.Millisecond)
	require.Zero(t, runs.Load(), "gated loop ran its fn while dormant")

	dormant.Store(false)
	rg.signalServiceWake()

	require.Eventually(t, func() bool { return runs.Load() > 0 },
		2*time.Second, 10*time.Millisecond,
		"gated loop did not resume after the gate opened and it was signaled")

	// And it keeps ticking once awake.
	before := runs.Load()
	require.Eventually(t, func() bool { return runs.Load() > before },
		2*time.Second, 10*time.Millisecond,
		"resumed loop did not keep ticking")
}

// Re-parking after a wake must work, so a burst of traffic followed by quiet
// returns the loop to zero cost.
func TestServiceKnobLoopGatedReparks(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	t.Cleanup(func() { require.NoError(t, rg.Close()) })

	var dormant atomic.Bool
	var runs atomic.Int32
	go rg.serviceKnobLoopGated("test", routersettings.TLPCheckInterval,
		func(time.Duration) { runs.Add(1) },
		func() bool { return dormant.Load() })

	require.Eventually(t, func() bool { return runs.Load() > 0 },
		2*time.Second, 10*time.Millisecond, "loop never started")

	dormant.Store(true)
	// Let it notice on its next tick, then confirm it has stopped.
	time.Sleep(300 * time.Millisecond)
	settled := runs.Load()
	time.Sleep(500 * time.Millisecond)
	require.Equal(t, settled, runs.Load(), "loop kept ticking after re-parking")
}

// An ungated loop must behave exactly as before.
func TestServiceKnobLoopUngatedStillTicks(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	t.Cleanup(func() { require.NoError(t, rg.Close()) })

	var runs atomic.Int32
	go rg.serviceKnobLoop("test", routersettings.TLPCheckInterval,
		func(time.Duration) { runs.Add(1) })

	require.Eventually(t, func() bool { return runs.Load() > 2 },
		2*time.Second, 10*time.Millisecond, "ungated loop did not tick")
}

// A closing group must release a parked loop rather than leaking it.
func TestServiceKnobLoopGatedExitsOnClose(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())

	done := make(chan struct{})
	go func() {
		rg.serviceKnobLoopGated("test", routersettings.TLPCheckInterval,
			func(time.Duration) {}, func() bool { return true })
		close(done)
	}()

	time.Sleep(200 * time.Millisecond) // let it park
	require.NoError(t, rg.Close())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parked service loop did not exit when the group closed")
	}
}

// The kill switch: mux.idle_suspend_grace = 0 must report every group active, so
// the loops tick unconditionally as they did before.
func TestIdleSuspendGraceZeroDisablesSuspension(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	t.Cleanup(func() { require.NoError(t, rg.Close()) })

	require.NoError(t, routersettings.Set("mux.idle_suspend_grace", "0"))
	defer func() { require.NoError(t, routersettings.Set("mux.idle_suspend_grace", "2s")) }()
	rg.refreshKnobs()

	require.False(t, rg.muxDormant(), "suspension must be off at grace 0")
	require.False(t, rg.singleLegDormant(), "suspension must be off at grace 0")
}

// A single-leg group is dormant for the leg-gated loops; a two-leg group is not.
func TestSingleLegDormant(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	t.Cleanup(func() { require.NoError(t, rg.Close()) })

	require.True(t, rg.singleLegDormant(), "an empty group has no second leg")

	rg.mu.Lock()
	rg.tps = make([]*transport.ManagedTransport, 2)
	rg.mu.Unlock()
	require.False(t, rg.singleLegDormant(), "a two-leg group must run the leg loops")

	// tps[] without matching fwd[]/rvs[] entries is not a state Close accepts,
	// so put the group back the way it was found before the cleanup runs.
	rg.mu.Lock()
	rg.tps = nil
	rg.mu.Unlock()
}

// recentlyActive is the shared "has anything happened lately" term; a zero
// stamp means never.
func TestRecentlyActive(t *testing.T) {
	now := time.Now().UnixNano()
	require.False(t, recentlyActive(0, time.Second), "a zero stamp is never recent")
	require.True(t, recentlyActive(now, time.Second), "a just-now stamp is recent")
	require.False(t, recentlyActive(time.Now().Add(-5*time.Second).UnixNano(), time.Second),
		"a stamp older than the grace period is not recent")
}

// The receive term is what stops a download — which sends almost nothing — from
// reading as idle and thrashing the gated loops once per arriving packet.
func TestMuxDormantHeldOffByReceiveActivity(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	t.Cleanup(func() { require.NoError(t, rg.Close()) })
	rg.mux = newRouteMux(rg.logger, true)

	// Quiet: nothing sent, nothing received, nothing outstanding.
	require.True(t, rg.muxDormant(), "a group with no activity at all must be dormant")

	// A download: receives only, no sends.
	rg.lastRecv.Store(time.Now().UnixNano())
	require.False(t, rg.muxDormant(), "receive activity alone must hold the loops awake")

	// An upload: sends only.
	rg.lastRecv.Store(0)
	rg.lastSent.Store(time.Now().UnixNano())
	require.False(t, rg.muxDormant(), "send activity alone must hold the loops awake")

	// Both stale again.
	stale := time.Now().Add(-time.Hour).UnixNano()
	rg.lastSent.Store(stale)
	rg.lastRecv.Store(stale)
	require.True(t, rg.muxDormant(), "a group quiet past the grace period must be dormant")
}

// handlePacket must record receive activity, so the gate sees a download.
func TestHandlePacketRecordsReceiveActivity(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	t.Cleanup(func() { require.NoError(t, rg.Close()) })

	require.Zero(t, rg.lastRecv.Load(), "no packet has arrived yet")
	pkt, err := routing.MakeDataPacket(1, []byte("x"))
	require.NoError(t, err)
	require.NoError(t, rg.handlePacket(pkt))
	require.NotZero(t, rg.lastRecv.Load(), "handlePacket must stamp receive activity")
}
