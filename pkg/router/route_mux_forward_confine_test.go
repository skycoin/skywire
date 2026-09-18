// Package router pkg/router/route_mux_forward_confine_test.go c2-net-routing
package router

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// fwdConfineRig builds the live legs-2 shape: ONE directional route group whose
// initiator (the forward/upload sender) has two MULTIHOP legs and no direct leg
// at all — leg 0 the 166 ms leg via the NL intermediary, leg 1 the 44 ms leg via
// the US one, the two legs and latencies of the 2026-09-16 mux-legs-2 set.
func fwdConfineRig(t *testing.T) (*routeMux, []*transport.ManagedTransport, []routing.Rule) {
	t.Helper()
	log := logging.MustGetLogger("forward-confine-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")
	hopSlow := pkFromHex(t, "021b17d2d884d9d73eeca0572143ac97de97e30ca61a682c71b5b968f37c86d206")
	hopFast := pkFromHex(t, "0224f0b6a1b017a1d3cb4a949a6047d13cac1c0580060888dcf1b9b3e09e123e6a")

	m := newRouteMux(log, true)
	m.setDirectional(true, dst, src)

	slow := &transport.ManagedTransport{}
	fast := &transport.ManagedTransport{}
	slow.Entry.ID = uuid.New()
	fast.Entry.ID = uuid.New()
	setRemoteForTest(slow, hopSlow)
	setRemoteForTest(fast, hopFast)
	tps := []*transport.ManagedTransport{slow, fast}
	fwd := make([]routing.Rule, 2)

	m.growLegs(2)
	m.markLegReady(0)
	m.markLegReady(1)
	m.SetLegLatencyFn(func(id uuid.UUID) float64 {
		if id == slow.Entry.ID {
			return 166
		}
		return 44
	})
	return m, tps, fwd
}

// setFwdWindows gives both legs an ECF window: the fast leg a SMALL one (it
// saturates almost at once under a 10 MB upload) and the slow leg a large one,
// so a scheduler free to spill has every reason to.
func setFwdWindows(m *routeMux, fastWindow, slowWindow float64) {
	m.tpSelector.SetECFState([]ecfLegState{
		{rttMs: 166, rttMinMs: 166, rateBps: 1 << 20, cwndBytes: slowWindow, ready: true},
		{rttMs: 44, rttMinMs: 44, rateBps: 1 << 20, cwndBytes: fastWindow, ready: true},
	})
}

// TestForwardConfinedLegWaitsInsteadOfSpilling is criterion 5's exit gate: 10 MB
// of forward frames over a 44 ms and a 166 ms leg, with the 44 ms leg's send
// window small enough to fill within the first few frames. Every frame must
// still leave on the 44 ms leg, the writer must PARK for the window (the
// send_window_waits counter moves), and the 166 ms leg must carry nothing.
//
// Before this change the frames went to whichever leg the ECF scheduler had
// room on. Live that is mux-legs-2 row 7 — 79 % of a 10 MB upload on the 166 ms
// leg, 21 % on the 44 ms one — and every such row collapsed (x0.68 at 10 MB,
// x0.24 at 50 MB), because the peer's no-skip reorder frontier waits out the
// 122 ms of skew between the two legs.
func TestForwardConfinedLegWaitsInsteadOfSpilling(t *testing.T) {
	m, tps, fwd := fwdConfineRig(t)
	setFwdWindows(m, 128*1024, 8*1024*1024)

	// A short wait bound keeps the test quick; the behavior under test is that
	// the writer waits at all rather than moving the frame.
	prev := SendWindowWaitMax()
	if !SetSendWindowWaitMax(2 * time.Millisecond) {
		t.Fatal("SetSendWindowWaitMax refused a positive value")
	}
	defer SetSendWindowWaitMax(prev) //nolint:errcheck

	closed := make(chan struct{})
	payload := make([]byte, 64*1024)
	seen := map[int]int{}
	const frames = 160 // 160 x 64 KiB = 10 MiB
	for i := 0; i < frames; i++ {
		m.waitSendWindow(tps, closed)
		_, _, idx, err := m.selectTransportRaw(tps, fwd, payload)
		if err != nil {
			t.Fatalf("frame %d: selectTransportRaw: %v", i, err)
		}
		seen[idx]++
		// The frame is now unacknowledged on that leg: this is what feedInflight
		// reads back, so the window fills exactly as it does on the wire.
		m.retxBuf.Store(uint32(i), payload, tps[idx].Entry.ID) //nolint:gosec
	}
	if seen[0] != 0 {
		t.Errorf("the 166 ms leg carried %d of %d forward frames — the confinement spilled", seen[0], frames)
	}
	if seen[1] != frames {
		t.Errorf("expected all %d frames on the 44 ms leg; got %v", frames, seen)
	}
	if waits := atomic.LoadUint64(&m.sendWindowWaits); waits == 0 {
		t.Error("the writer never parked for the confined leg's window — it must wait, not spill")
	}
}

// TestForwardSpillKnobRestoresOldBehaviour proves --forward-spill is live: with
// it on, a full window on the confined leg hands the frame to another leg, which
// is what the code did before.
func TestForwardSpillKnobRestoresOldBehaviour(t *testing.T) {
	m, tps, fwd := fwdConfineRig(t)
	setFwdWindows(m, 64*1024, 8*1024*1024)

	if !SetForwardSpill(true) {
		t.Fatal("SetForwardSpill(true) refused")
	}
	defer SetForwardSpill(forwardSpillDefault) //nolint:errcheck

	payload := make([]byte, 64*1024)
	spilled := 0
	for i := 0; i < 20; i++ {
		_, _, idx, err := m.selectTransportRaw(tps, fwd, payload)
		if err != nil {
			t.Fatalf("frame %d: selectTransportRaw: %v", i, err)
		}
		if idx == 0 {
			spilled++
		}
		m.retxBuf.Store(uint32(i), payload, tps[idx].Entry.ID) //nolint:gosec
	}
	if spilled == 0 {
		t.Error("--forward-spill=true did not spill a single frame off the saturated confined leg")
	}
}

// TestForwardConfinementHysteresis: measurement noise that momentarily inverts
// the leg order by 10 % must not move the forward direction, and a SUSTAINED
// 30 % inversion must move it exactly once, with a forward_rehomed event.
func TestForwardConfinementHysteresis(t *testing.T) {
	m, tps, fwd := fwdConfineRig(t)
	type rehome struct {
		prev, next int
		reason     string
	}
	var events []rehome
	m.SetForwardRehomeFn(func(prev, next int, _ *transport.ManagedTransport, _ int, reason string) {
		events = append(events, rehome{prev, next, reason})
	})

	sample := func() int {
		m.confinedFwdAtNano = 0 // a fresh measurement, without sleeping a refresh
		_, _, idx, err := m.selectTransportRaw(tps, fwd, []byte("payload"))
		if err != nil {
			t.Fatalf("selectTransportRaw: %v", err)
		}
		return idx
	}

	if idx := sample(); idx != 1 {
		t.Fatalf("forward should start on the 44 ms leg; got leg %d", idx)
	}
	if len(events) != 0 {
		t.Fatalf("the first placement is not a rehome; got %v", events)
	}

	// Jitter: the 166 ms leg momentarily reads 10 % BELOW the incumbent. Ten
	// consecutive such samples must not move the direction.
	m.SetLegLatencyFn(func(id uuid.UUID) float64 {
		if id == tps[0].Entry.ID {
			return 39.6 // 10 % below 44
		}
		return 44
	})
	for i := 0; i < 10; i++ {
		if idx := sample(); idx != 1 {
			t.Fatalf("sample %d: a 10 %% inversion moved the forward direction to leg %d", i, idx)
		}
	}
	if len(events) != 0 {
		t.Fatalf("a 10 %% inversion emitted %v", events)
	}

	// Sustained 30 % inversion: the first sample holds (one sample is not a
	// decision), the second moves the direction, and it stays moved.
	m.SetLegLatencyFn(func(id uuid.UUID) float64 {
		if id == tps[0].Entry.ID {
			return 30.8 // 30 % below 44
		}
		return 44
	})
	if idx := sample(); idx != 1 {
		t.Fatalf("one 30 %% sample moved the direction to leg %d; the switch needs %d", idx, forwardSwitchSamplesDefault)
	}
	if idx := sample(); idx != 0 {
		t.Fatalf("a sustained 30 %% inversion did not move the direction; still on leg %d", idx)
	}
	for i := 0; i < 5; i++ {
		if idx := sample(); idx != 0 {
			t.Fatalf("the direction moved back to leg %d — it flipped", idx)
		}
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly one forward_rehomed; got %v", events)
	}
	if events[0].prev != 1 || events[0].next != 0 || !strings.Contains(events[0].reason, "consecutive samples") {
		t.Errorf("rehome event does not name the margin decision: %+v", events[0])
	}
}

// TestForwardRehomesWhenConfinedLegParks: a confined leg that is parked to warm
// standby (or dies) is not something to wait on — the direction re-confines to
// the next lowest-latency live leg, and says so.
func TestForwardRehomesWhenConfinedLegParks(t *testing.T) {
	m, tps, fwd := fwdConfineRig(t)
	var reasons []string
	m.SetForwardRehomeFn(func(_, _ int, _ *transport.ManagedTransport, _ int, reason string) {
		reasons = append(reasons, reason)
	})

	_, _, idx, err := m.selectTransportRaw(tps, fwd, []byte("payload"))
	if err != nil {
		t.Fatalf("selectTransportRaw: %v", err)
	}
	if idx != 1 {
		t.Fatalf("forward should start on the 44 ms leg; got leg %d", idx)
	}

	m.setLegStandby(1, true)
	m.confinedFwdAtNano = 0
	_, _, idx, err = m.selectTransportRaw(tps, fwd, []byte("payload"))
	if err != nil {
		t.Fatalf("selectTransportRaw after park: %v", err)
	}
	if idx != 0 {
		t.Fatalf("a parked confined leg must rehome the direction; still on leg %d", idx)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "no longer selectable") {
		t.Fatalf("expected one forward_rehomed naming the parked leg; got %v", reasons)
	}
}

// TestReverseFanoutUnchangedByForwardConfinement: the ACCEPTOR (the exit, which
// sends the download) is not confined by any of this — its fan-out across the
// reverse legs is the whole point of the mux and must be untouched.
func TestReverseFanoutUnchangedByForwardConfinement(t *testing.T) {
	log := logging.MustGetLogger("reverse-fanout-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")
	hopA := pkFromHex(t, "021b17d2d884d9d73eeca0572143ac97de97e30ca61a682c71b5b968f37c86d206")
	hopB := pkFromHex(t, "0224f0b6a1b017a1d3cb4a949a6047d13cac1c0580060888dcf1b9b3e09e123e6a")

	m := newRouteMux(log, true)
	m.setDirectional(false, dst, src) // acceptor: this end sends the REVERSE direction
	legA := &transport.ManagedTransport{}
	legB := &transport.ManagedTransport{}
	legA.Entry.ID = uuid.New()
	legB.Entry.ID = uuid.New()
	setRemoteForTest(legA, hopA)
	setRemoteForTest(legB, hopB)
	tps := []*transport.ManagedTransport{legA, legB}
	fwd := make([]routing.Rule, 2)
	m.growLegs(2)
	m.markLegReady(0)
	m.markLegReady(1)
	// Wildly asymmetric latencies: a confined direction would pin every frame to
	// legB. The reverse direction must still use both.
	m.SetLegLatencyFn(func(id uuid.UUID) float64 {
		if id == legA.Entry.ID {
			return 400
		}
		return 20
	})

	seen := map[int]int{}
	for i := 0; i < 200; i++ {
		_, _, idx, err := m.selectTransportRaw(tps, fwd, []byte("payload"))
		if err != nil {
			t.Fatalf("iteration %d: selectTransportRaw: %v", i, err)
		}
		seen[idx]++
	}
	if seen[0] == 0 || seen[1] == 0 {
		t.Errorf("the reverse direction must fan out across both legs; got %v", seen)
	}
	if idx := m.confinedForwardIdx(); idx >= 0 {
		t.Errorf("the acceptor must hold no forward confinement; got leg %d", idx)
	}
}

// TestForwardKnobRoundTrip pins the live knobs' accept/refuse contract.
func TestForwardKnobRoundTrip(t *testing.T) {
	defer SetForwardSpill(forwardSpillDefault)               //nolint:errcheck
	defer SetForwardSwitchMargin(forwardSwitchMarginDefault) //nolint:errcheck
	if ForwardSpill() != forwardSpillDefault {               //nolint:staticcheck
		t.Fatalf("forward spill should default to %v", forwardSpillDefault)
	}
	if got := ForwardSwitchMargin(); got != forwardSwitchMarginDefault {
		t.Fatalf("forward switch margin default = %v, want %v", got, forwardSwitchMarginDefault)
	}
	if !SetForwardSpill(true) || !ForwardSpill() {
		t.Error("SetForwardSpill(true) did not take")
	}
	if !SetForwardSpill(false) || ForwardSpill() {
		t.Error("SetForwardSpill(false) did not take")
	}
	if !SetForwardSwitchMargin(0.35) || ForwardSwitchMargin() != 0.35 {
		t.Error("SetForwardSwitchMargin(0.35) did not take")
	}
	for _, bad := range []float64{0, -0.1, 1, 1.5} {
		if SetForwardSwitchMargin(bad) {
			t.Errorf("SetForwardSwitchMargin(%v) should be refused", bad)
		}
	}
	if ForwardSwitchMargin() != 0.35 {
		t.Error("a refused margin must leave the value in force")
	}
}
