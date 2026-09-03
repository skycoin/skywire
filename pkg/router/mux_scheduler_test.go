package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schedLeg builds an ecfLegState with the fields the OTIAS/STMS estimators read
// (rate is what makes their arrival/queue terms non-degenerate, unlike the ECF
// table which drives most cases off cwnd/inflight alone).
func schedLeg(rttMs, rateBps, cwndBytes, inflightBytes float64, ready bool) ecfLegState {
	return ecfLegState{
		rttMs:         rttMs,
		rateBps:       rateBps,
		cwndBytes:     cwndBytes,
		inflightBytes: inflightBytes,
		ready:         ready,
	}
}

// TestOTIASPick_InOrderSoonestLeg is the core OTIAS assertion: it picks the leg
// with the earliest ESTIMATED ARRIVAL, which is NOT always the lowest-RTT leg —
// a fast leg carrying a big backlog can arrive later than a slower idle leg.
func TestOTIASPick(t *testing.T) {
	tests := []struct {
		name string
		legs []ecfLegState
		want int
	}{
		{
			name: "no ready leg returns -1",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1e6, 0, false),
				schedLeg(100, 1e6, 1e6, 0, false),
			},
			want: -1,
		},
		{
			name: "two idle legs: lowest-RTT (soonest arrival) wins",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1e6, 0, true),  // arrival ~5ms
				schedLeg(100, 1e6, 1e6, 0, true), // arrival ~50ms
			},
			want: 0,
		},
		{
			// Fast leg has a large backlog it drains slowly (1 MB @ 1 MB/s =
			// 1000ms queue + 5ms one-way = ~1005ms). The slow-RTT leg is idle
			// (arrival ~50ms). OTIAS must pick the slower-RTT leg because the
			// segment ARRIVES sooner there — the whole point of the scheduler.
			name: "backlogged fast leg loses to idle slow leg on arrival time",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1e6, 1e6, true), // arrival ~1005ms
				schedLeg(100, 1e6, 1e6, 0, true),  // arrival ~50ms
			},
			want: 1,
		},
		{
			// Three legs: pick the true arrival minimum. leg0 backlogged
			// (~1005ms), leg1 idle 100ms (~50ms), leg2 idle 40ms (~20ms).
			name: "three legs: soonest arrival among all",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1e6, 1e6, true),
				schedLeg(100, 1e6, 1e6, 0, true),
				schedLeg(40, 1e6, 1e6, 0, true),
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, otiasPick(tt.legs))
		})
	}
}

// TestOTIASPick_FansOutUnderCharge proves the out-of-order-for-in-order behavior
// end-to-end: with a fast and a slow leg both idle, OTIAS front-loads the fast
// leg, but as that leg's charged in-flight backlog grows its estimated arrival
// rises past the idle slow leg's and OTIAS diverts later segments to the slow
// leg. So both legs carry data — it does not dump the whole stream on one leg.
func TestSelectOTIAS_FansOut(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeOTIAS)

	// Fast leg: 10ms, ~1 MB/s. Slow leg: 120ms, ~1 MB/s. rate=0 draining is
	// avoided by giving a real rate; the per-pick charge is what shifts arrivals.
	ts.SetECFState([]ecfLegState{
		{rttMs: 10, rateBps: 1e6, cwndBytes: 1e9, ready: true},
		{rttMs: 120, rateBps: 1e6, cwndBytes: 1e9, ready: true},
	})

	counts := map[int]int{}
	payload := make([]byte, 1400)
	for i := 0; i < 400; i++ {
		counts[ts.SelectForPayload(payload)]++
	}
	assert.Greater(t, counts[0], 0, "fast leg carries the head of the stream")
	assert.Greater(t, counts[1], 0, "slow leg carries later segments once the fast leg's queue builds")
	assert.Greater(t, counts[0], counts[1], "fast leg still carries the larger share")
}

// TestSTMSPick_EarlierDataOnFastLeg is the core STMS assertion: while the fast
// leg's send window has room, every frame rides it (earlier data on the fast
// path). Once the fast window is full, later data slides onto a slower leg.
func TestSTMSPick(t *testing.T) {
	tests := []struct {
		name string
		legs []ecfLegState
		want int
	}{
		{
			name: "no ready leg returns -1",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1e6, 0, false),
			},
			want: -1,
		},
		{
			name: "fast leg with window room takes the frame (earlier data on fast)",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1e6, 0, true), // window wide open
				schedLeg(100, 1e6, 1e6, 0, true),
			},
			want: 0,
		},
		{
			// Fast leg's window is FULL (inflight >= cwnd): later data slides to
			// the slower leg that still has room.
			name: "fast window full: slide later data onto the slow leg",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1000, 5000, true), // saturated
				schedLeg(100, 1e6, 1e9, 0, true),    // room
			},
			want: 1,
		},
		{
			// Fast window full and the ONLY slow candidate also full: queue on
			// the fast leg (STMS never returns NO-LEG).
			name: "all legs saturated: stay on fast leg",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1000, 5000, true),
				schedLeg(100, 1e6, 1000, 5000, true),
			},
			want: 0,
		},
		{
			// Two slow candidates with room: STMS slides to the soonest-arriving
			// one. leg1 40ms idle (~20ms) beats leg2 200ms idle (~100ms).
			name: "fast full: slide to soonest-arriving slow leg",
			legs: []ecfLegState{
				schedLeg(10, 1e6, 1000, 5000, true), // saturated fast
				schedLeg(40, 1e6, 1e9, 0, true),
				schedLeg(200, 1e6, 1e9, 0, true),
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stmsPick(tt.legs))
		})
	}
}

// TestSTMS_vs_ECF_HoldbackDistinction pins the defining behavioral difference:
// on identical legs where ECF's hold-back predicate DECLINES the slow leg (the
// fast leg drains its backlog before the slow leg delivers one frame, so ECF
// holds on the fast leg), STMS instead commits the later data to the slow leg.
func TestSTMS_vs_ECF_HoldbackDistinction(t *testing.T) {
	// Canonical ECF hold case (from TestECFPick): fast 10ms cwnd 10000 backlog
	// 20000 (n=3, n*rttF=30) vs slow 100ms idle -> 30 < 100 so ECF holds on 0.
	legs := []ecfLegState{
		schedLeg(10, 0, 10000, 20000, true),
		schedLeg(100, 0, 1e9, 0, true),
	}
	assert.Equal(t, 0, ecfPick(legs, false, nil), "ECF holds the frame on the fast leg")
	assert.Equal(t, 1, stmsPick(legs), "STMS slides later data onto the slow leg (no hold-back decline)")
}

// TestSelectSTMS_Integration drives STMS through the selector: the fast leg with
// window room wins every frame until it saturates, then frames spill to the slow
// leg. Also confirms in-flight is charged to the chosen leg.
func TestSelectSTMS_Integration(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeSTMS)

	// Fast leg window = 1400 bytes (one frame) so it saturates immediately after
	// the first charge; rate 0 so the time-drain never clears it within the test.
	ts.SetECFState([]ecfLegState{
		{rttMs: 10, cwndBytes: 1400, rateBps: 0, ready: true},
		{rttMs: 100, cwndBytes: 1e9, rateBps: 0, ready: true},
	})

	payload := make([]byte, 1400)
	first := ts.SelectForPayload(payload)
	require.Equal(t, 0, first, "first (earliest) frame rides the fast leg")

	// Fast leg is now saturated; subsequent frames slide to the slow leg.
	spill := ts.SelectForPayload(payload)
	assert.Equal(t, 1, spill, "later frames slide onto the slow leg once the fast window is full")

	ts.mu.RLock()
	slowInflight := ts.ecfLegs[1].inflightBytes
	ts.mu.RUnlock()
	assert.Greater(t, slowInflight, 0.0, "the slid frame charges the slow leg's in-flight")
}

// TestSelectPredictive_BootstrapFallsBackToSchedule confirms OTIAS and STMS,
// like ECF, fall back to the mirrored schedule when no estimator state exists.
func TestSelectPredictive_BootstrapFallsBackToSchedule(t *testing.T) {
	for _, mode := range []WeightMode{WeightModeOTIAS, WeightModeSTMS} {
		ts := newTransportSelector()
		ts.SetMode(mode)
		ts.mu.Lock()
		ts.schedule = []int{0, 1}
		ts.mu.Unlock()
		idx := ts.SelectForPayload(make([]byte, 512))
		assert.Contains(t, []int{0, 1}, idx, "%v bootstrap falls back to a schedule index", mode)
	}
}

// TestPredictiveModeStrings pins the operator-facing mode tokens.
func TestPredictiveModeStrings(t *testing.T) {
	assert.Equal(t, "otias", WeightModeOTIAS.String())
	assert.Equal(t, "stms", WeightModeSTMS.String())
	assert.True(t, WeightModeOTIAS.isPredictive())
	assert.True(t, WeightModeSTMS.isPredictive())
	assert.True(t, WeightModeECF.isPredictive())
	assert.False(t, WeightModeCapacity.isPredictive())
}
