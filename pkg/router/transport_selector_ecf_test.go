package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// leg is a small ecfLegState constructor for the table tests.
func ecfLeg(rttMs, jitterMs, cwndBytes, inflightBytes float64, ready bool) ecfLegState {
	return ecfLegState{
		rttMs:         rttMs,
		jitterMs:      jitterMs,
		cwndBytes:     cwndBytes,
		inflightBytes: inflightBytes,
		ready:         ready,
	}
}

// TestECFPick is the deterministic table-driven test of the ECF hold-back
// predicate (the pure ecfPick decision). It feeds synthetic legs + a backlog
// (encoded as the fast leg's inflightBytes) and asserts which leg is chosen.
func TestECFPick(t *testing.T) {
	tests := []struct {
		name    string
		legs    []ecfLegState
		waiting bool
		want    int
		wantW   bool // expected waiting latch afterwards
	}{
		{
			name: "no ready leg returns -1",
			legs: []ecfLegState{
				ecfLeg(10, 0, 1000, 0, false),
				ecfLeg(100, 0, 1000, 0, false),
			},
			want: -1,
		},
		{
			name: "single ready leg is chosen",
			legs: []ecfLegState{
				ecfLeg(10, 0, 1000, 5000, true), // saturated but only option
			},
			want:  0,
			wantW: false,
		},
		{
			name: "fast leg not saturated: use it, ignore slow leg",
			legs: []ecfLegState{
				ecfLeg(10, 0, 10000, 0, true), // room to spare
				ecfLeg(100, 0, 10000, 0, true),
			},
			want:  0,
			wantW: false,
		},
		{
			name: "unknown-capacity fast leg (cold start) is used",
			legs: []ecfLegState{
				ecfLeg(10, 0, 0, 0, true), // cwnd unknown -> never saturated
				ecfLeg(100, 0, 10000, 0, true),
			},
			want:  0,
			wantW: false,
		},
		{
			// CANONICAL CASE: one fast leg + one 10x-RTT slow leg under
			// backlog. The fast leg is saturated (backlog present) but can
			// drain it far sooner than the slow leg delivers even one frame,
			// so ECF must NOT schedule onto the slow leg.
			name: "fast + 10x-RTT slow under moderate backlog: hold on fast, not slow",
			legs: []ecfLegState{
				ecfLeg(10, 0, 10000, 20000, true), // saturated, k=20000, n=3, n*rttF=30
				ecfLeg(100, 0, 1e9, 0, true),      // slow leg, rttS=100
			},
			want:  0, // 30 < 100 -> hold on fast
			wantW: true,
		},
		{
			// Same topology but a HUGE backlog: now the fast leg would take
			// longer to drain than the slow leg needs to deliver one frame,
			// so spilling to the slow leg is the earliest-completion choice.
			name: "fast + 10x-RTT slow under huge backlog: spill to slow",
			legs: []ecfLegState{
				ecfLeg(10, 0, 10000, 200000, true), // k=200000, n=21, n*rttF=210
				ecfLeg(100, 0, 1e9, 0, true),       // rttS=100
			},
			want:  1, // 210 >= 100 -> spill
			wantW: false,
		},
		{
			// Jitter margin d widens the slow leg's effective delivery time,
			// making the hold decision stickier. With d=60, rttS+d=160 and
			// n*rttF=150 (k=140000 -> n=15) stays under it -> hold on fast.
			name: "jitter margin keeps a borderline frame on the fast leg",
			legs: []ecfLegState{
				ecfLeg(10, 0, 10000, 140000, true), // n=15, n*rttF=150
				ecfLeg(100, 60, 1e9, 0, true),      // rttS=100, jitter 60 -> d=60
			},
			want:  0, // 150 < 160 -> hold
			wantW: true,
		},
		{
			// Two ready spill candidates: the saturated fast leg spills to the
			// lower-RTT of the two remaining legs (xs = smallest RTT with
			// capacity), here leg 1 (rtt 50) over leg 2 (rtt 80).
			name: "spill picks the lowest-RTT candidate with capacity",
			legs: []ecfLegState{
				ecfLeg(10, 0, 1000, 500000, true), // hopelessly backlogged -> spill
				ecfLeg(50, 0, 1e9, 0, true),
				ecfLeg(80, 0, 1e9, 0, true),
			},
			want:  1,
			wantW: false,
		},
		{
			// A saturated fast leg with NO spill target that has capacity
			// stays on the fast leg (filter variant never returns NO-LEG).
			name: "all legs saturated: stay on fast leg",
			legs: []ecfLegState{
				ecfLeg(10, 0, 1000, 500000, true),
				ecfLeg(100, 0, 1000, 500000, true),
			},
			want:  0,
			wantW: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := tt.waiting
			got := ecfPick(tt.legs, tt.waiting, &w)
			assert.Equal(t, tt.want, got, "chosen leg")
			if tt.want >= 0 {
				assert.Equal(t, tt.wantW, w, "waiting latch")
			}
		})
	}
}

// TestECFPick_Hysteresis verifies the beta hysteresis: once waiting is latched,
// the slow leg's delivery estimate is inflated by (1+ecfBeta), so a frame that
// would spill when not waiting is instead held when waiting.
func TestECFPick_Hysteresis(t *testing.T) {
	// Tune so the predicate is just BELOW the spill boundary once hysteresis
	// applies but just ABOVE it without. n*rttF = 105 (k=95000 -> n=10.5).
	// rttS+d = 100. Without hysteresis: 105 >= 100 -> spill (leg 1).
	// With hysteresis: 105 < 1.25*100=125 -> hold (leg 0).
	legs := []ecfLegState{
		ecfLeg(10, 0, 10000, 95000, true),
		ecfLeg(100, 0, 1e9, 0, true),
	}

	got := ecfPick(legs, false, nil)
	assert.Equal(t, 1, got, "without hysteresis: spill to slow leg")

	got = ecfPick(legs, true, nil)
	assert.Equal(t, 0, got, "with hysteresis latched: hold on fast leg")
}

// TestSelectECF_Integration drives the selector end-to-end through SetECFState
// + SelectForPayload, confirming the mode routes via the ECF pick and that the
// inflight accounting charges the chosen leg.
func TestSelectECF_Integration(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeECF)

	// Two legs; the fast leg (0) has ample capacity, so every DATA frame
	// should land on it until it saturates.
	ts.SetECFState([]ecfLegState{
		{rttMs: 10, cwndBytes: 1e9, rateBps: 0, ready: true},
		{rttMs: 100, cwndBytes: 1e9, rateBps: 0, ready: true},
	})

	payload := make([]byte, 512)
	for i := 0; i < 50; i++ {
		idx := ts.SelectForPayload(payload)
		require.Equal(t, 0, idx, "fast leg with capacity should always win")
	}

	// The fast leg should now show accumulated inflight; the slow leg none.
	ts.mu.RLock()
	fastInflight := ts.ecfLegs[0].inflightBytes
	slowInflight := ts.ecfLegs[1].inflightBytes
	ts.mu.RUnlock()
	assert.Greater(t, fastInflight, 0.0, "chosen leg accrues inflight bytes")
	assert.Equal(t, 0.0, slowInflight, "unchosen leg accrues nothing")
}

// TestSelectECF_SpillsWhenFastSaturated confirms that once the fast leg is
// modeled as saturated with a large backlog, the selector spills DATA frames
// onto the slower ready leg rather than head-of-line-stalling on the fast one.
func TestSelectECF_SpillsWhenFastSaturated(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeECF)

	// Fast leg saturated with a huge backlog (rate 0 so time-drain won't clear
	// it), small cwnd; slow leg has capacity. n*rttF = (1+200000/1000)*10 huge
	// >> rttS -> spill.
	ts.SetECFState([]ecfLegState{
		{rttMs: 10, cwndBytes: 1000, inflightBytes: 200000, rateBps: 0, ready: true},
		{rttMs: 100, cwndBytes: 1e9, rateBps: 0, ready: true},
	})

	idx := ts.SelectForPayload(make([]byte, 512))
	assert.Equal(t, 1, idx, "saturated fast leg under huge backlog spills to slow leg")
}

// TestSelectECF_BootstrapFallsBackToSchedule confirms that with no ECF state
// the selector falls back to the schedule (never panics / returns garbage).
func TestSelectECF_BootstrapFallsBackToSchedule(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeECF)
	// No SetECFState call: ecfLegs empty. Provide a schedule via a fake rebuild
	// by directly seeding the schedule the way Rebuild would for live legs.
	ts.mu.Lock()
	ts.schedule = []int{0, 1}
	ts.mu.Unlock()

	idx := ts.SelectECF(512)
	assert.Contains(t, []int{0, 1}, idx, "bootstrap falls back to a schedule index")
}
