package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSendWindowFollowsFeedbackDelayOverFirstHopBaseline pins the two halves
// of the window computation apart.
//
// The window must FOLLOW the live feedback delay — cwnd = delivered x
// max(first-hop baseline, send→ack delay) x margin. Sized over the first hop
// alone it starves a long path: measured live 2026-09-17, a 470 ms leg
// clamped to a first-hop multiple collapsed to the 128 KiB floor, a 50 MB
// upload running at 0.23 MB/s with wire/goodput 1.00 (no loss — the starved
// window shrank the delivery that sizes it).
//
// The BASELINE it is anchored to must nevertheless stay the first hop, which
// no queue of ours can inflate. #4970 put the combined end-to-end value on
// ecfRttMinMs, and since the send→ack EWMA has no time decay the queue one
// transfer built became the next one's baseline: five consecutive 50 MB
// uploads over one leg fell 3.90 → 3.05 → 2.80 → 1.59 → 0.62 MB/s at zero
// loss with the leg's delay reading ratcheting to 13435 ms.
func TestSendWindowFollowsFeedbackDelayOverFirstHopBaseline(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 1)
	m := rg.mux
	tpID := mts[0].Entry.ID
	mts[0].SetLatency(40) // first hop: 40 ms, and it stays 40 ms

	const delivPerSec = 1 << 20 // a constant 1 MiB/s of SACK-proven delivery
	var acked uint64

	// step advances one second of delivery at the given send→ack delay and
	// returns the window the refresh proved together with the feedback delay
	// the estimator held when it did (the EWMA lags each raw sample).
	step := func(adMs float64) (cwnd, fb float64) {
		t.Helper()
		if adMs > 0 {
			m.recordAckDelayTp(tpID, time.Duration(adMs)*time.Millisecond)
		}
		fb = m.ackDelayMsTp(tpID)
		acked += delivPerSec
		m.retxBuf.mu.Lock()
		m.retxBuf.ackedByTp[tpID] = acked
		m.retxBuf.mu.Unlock()
		// Pin the measurement interval to exactly one second so the delivery
		// rate the refresh computes is the constant above.
		m.legMu.Lock()
		if m.legs[0].ecfLastAckedBytes != 0 {
			m.legs[0].ecfLastAckedNano = time.Now().Add(-time.Second).UnixNano()
		}
		m.legMu.Unlock()
		m.refreshLegWindows(mts)
		return ecfStatesOf(m)[0].cwndBytes, fb
	}

	step(200) // seeds the acked snapshot; the next refresh is the first measured one
	var last float64
	for _, ad := range []float64{200, 400, 800, 1500, 3000} {
		got, fb := step(ad)
		want := delivPerSec * fb / 1000.0 * ecfWindowMarginDefault
		if want > ecfMaxWindowBytesDefault {
			want = ecfMaxWindowBytesDefault
		}
		require.InDelta(t, want, got, want*0.05,
			"the window must be the delivered rate over the %g ms feedback delay", fb)
		require.Greater(t, got, last, "a longer feedback delay must not shrink the window")
		last = got
	}
	require.Greater(t, last, delivPerSec*0.040*ecfWindowMarginDefault*10,
		"a 470ms-class path must not be sized as if it were its 40 ms first hop")

	// ...while the baseline it is anchored to never left the first hop.
	m.legMu.Lock()
	hopMin := m.legs[0].ecfHopRttMinMs
	m.legMu.Unlock()
	require.InDelta(t, 40.0, hopMin, 1.0,
		"the BDP baseline is the first-hop minimum, which a queue cannot inflate")

	// The end-to-end basis the ECF pick, the RACK threshold and the latency
	// band read is still the combined value #4970 put there.
	require.Greater(t, ecfStatesOf(m)[0].rttMs, 500.0,
		"ecfRttMs stays on the end-to-end feedback delay")

	// After ackDelayStale of quiet the estimate expires with the transfer, so
	// the next one is sized on the 40 ms first hop again rather than inheriting
	// the 3000 ms queue. (Aged by hand; the rule is the same.)
	m.ackDelayByTpMu.Lock()
	m.ackDelayByTp[tpID].lastNano = time.Now().Add(-ackDelayStale - time.Second).UnixNano()
	m.ackDelayByTpMu.Unlock()
	require.Zero(t, m.ackDelayMsTp(tpID), "a stale estimate describes a queue that has drained")

	got, _ := step(0)
	require.InDelta(t, delivPerSec*0.040*ecfWindowMarginDefault, got, float64(delivPerSec)*0.05,
		"once the ack-delay estimate expires the window is sized on the first hop again")
}

// TestAckDelayEstimateExpiresWhenIdle covers the expiry rule on its own: the
// per-leg estimate has no time decay, so without it the queue one transfer
// built was still the loss threshold and the window's feedback delay when the
// next one started. The next sample must seed the estimate whole rather than
// EWMA down from the stale value.
func TestAckDelayEstimateExpiresWhenIdle(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 1)
	m := rg.mux
	tpID := mts[0].Entry.ID

	m.recordAckDelayTp(tpID, 200*time.Millisecond)
	m.recordAckDelayTp(tpID, 3*time.Second)
	require.Greater(t, m.ackDelayMsTp(tpID), 1000.0)

	// Age the estimate past ackDelayStale: the transfer ended, the queue drained.
	m.ackDelayByTpMu.Lock()
	m.ackDelayByTp[tpID].lastNano = time.Now().Add(-ackDelayStale - time.Second).UnixNano()
	m.ackDelayByTpMu.Unlock()
	require.Zero(t, m.ackDelayMsTp(tpID), "a stale estimate describes a queue that has drained")

	// The next transfer measures its own delay from scratch.
	m.recordAckDelayTp(tpID, 250*time.Millisecond)
	require.InDelta(t, 250.0, m.ackDelayMsTp(tpID), 1.0,
		"the first sample after an idle gap seeds the estimate whole")
}
