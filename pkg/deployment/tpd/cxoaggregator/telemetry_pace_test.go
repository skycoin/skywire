package cxoaggregator

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func newTelKey(t *testing.T) telKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return telKey{uuid.New(), pk}
}

func TestTelemetryPacerHoldsWithinWindow(t *testing.T) {
	var p telemetryPacer
	k := newTelKey(t)
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	out := p.offer(k, telSnap{sent: 10, throughput: 5, latMin: 1, latMax: 3, latAvg: 2}, t0)
	require.Len(t, out, 1, "first report applies")
	require.EqualValues(t, 10, out[0].sent)
	require.Equal(t, t0.UnixNano(), out[0].at)

	require.Empty(t, p.offer(k, telSnap{sent: 20, throughput: 50}, t0.Add(45*time.Second)))
	require.Empty(t, p.offer(k, telSnap{sent: 30, throughput: 7}, t0.Add(90*time.Second)))
	require.Empty(t, p.due(t0.Add(100*time.Second)), "window not passed")

	// The window passes with no newer report: the held one is flushed with
	// the latest counters and the peak throughput.
	held := p.due(t0.Add(telemetryApplyEvery + time.Second))
	require.Len(t, held, 1)
	require.EqualValues(t, 30, held[0].s.sent)
	require.EqualValues(t, 50, held[0].s.throughput)

	// Latency survives a newer report that has none.
	require.Empty(t, p.offer(k, telSnap{sent: 40, latMin: 4, latMax: 6, latAvg: 5}, t0.Add(telemetryApplyEvery+10*time.Second)))
	out = p.offer(k, telSnap{sent: 50}, t0.Add(2*telemetryApplyEvery+2*time.Second))
	require.Len(t, out, 1)
	require.EqualValues(t, 50, out[0].sent)
	require.EqualValues(t, 5, out[0].latAvg)
}

// A report held at 23:59 is applied dated 23:59 when the first report of the
// next day arrives, and that report is applied at once behind it, so the
// old day keeps its last bytes and the new day starts sharp.
func TestTelemetryPacerMidnight(t *testing.T) {
	var p telemetryPacer
	k := newTelKey(t)
	t0 := time.Date(2026, 9, 30, 23, 50, 0, 0, time.UTC)
	require.Len(t, p.offer(k, telSnap{sent: 1}, t0), 1)
	late := time.Date(2026, 9, 30, 23, 59, 30, 0, time.UTC)
	require.Empty(t, p.offer(k, telSnap{sent: 2}, late))

	next := late.Add(45 * time.Second)
	out := p.offer(k, telSnap{sent: 3}, next)
	require.Len(t, out, 2)
	require.EqualValues(t, 2, out[0].sent)
	require.Equal(t, late.UnixNano(), out[0].at, "the held row keeps its own day")
	require.EqualValues(t, 3, out[1].sent)
	require.Equal(t, next.UnixNano(), out[1].at)

	// With no report after midnight the flush still dates the held row to
	// the old day, within one flush tick.
	k2 := newTelKey(t)
	require.Len(t, p.offer(k2, telSnap{sent: 1}, t0), 1)
	require.Empty(t, p.offer(k2, telSnap{sent: 2}, late))
	held := p.due(late.Add(telemetryFlushEvery + time.Second))
	require.Len(t, held, 1)
	require.Equal(t, late.UnixNano(), held[0].s.at)
	require.EqualValues(t, 2, held[0].s.sent)
}

// A row identical to the last written is not written again until
// unchangedRefresh passes; any change is written at the next window.
func TestTelemetryPacerSkipsUnchanged(t *testing.T) {
	var p telemetryPacer
	k := newTelKey(t)
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	row := telSnap{sent: 7, recv: 9, throughput: 100, latMin: 1, latMax: 2, latAvg: 1.5, tp: tpCodeOf("stcpr")}
	require.Len(t, p.offer(k, row, t0), 1)

	at := t0
	for at.Sub(t0) < unchangedRefresh-telemetryApplyEvery {
		at = at.Add(telemetryApplyEvery)
		lower := row
		lower.throughput = 50 // under the stored peak: nothing to write
		require.Empty(t, p.offer(k, lower, at), "unchanged at %v", at.Sub(t0))
	}
	at = at.Add(telemetryApplyEvery)
	require.Len(t, p.offer(k, row, at), 1, "refreshed at %v so its TTLs do not lapse", at.Sub(t0))

	// A moved counter is never skipped.
	moved := row
	moved.sent++
	require.Len(t, p.offer(k, moved, at.Add(telemetryApplyEvery)), 1)
	// Nor is a higher throughput peak.
	peak := moved
	peak.throughput = 200
	require.Len(t, p.offer(k, peak, at.Add(2*telemetryApplyEvery)), 1)
}

// Skipping unchanged rows loses no bytes: the cumulative counters written
// still end at the last value reported.
func TestTelemetryPacerSkipKeepsCountersExact(t *testing.T) {
	var p telemetryPacer
	k := newTelKey(t)
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	var written, last uint64
	for i := 0; i < 400; i++ {
		now := t0.Add(time.Duration(i) * 45 * time.Second)
		if i%7 == 0 {
			last += uint64(i) // moves now and then, idle in between
		}
		for _, s := range p.offer(k, telSnap{sent: last}, now) {
			require.GreaterOrEqual(t, s.sent, written)
			written = s.sent
		}
	}
	for _, h := range p.due(t0.Add(400*45*time.Second + telemetryApplyEvery)) {
		written = h.s.sent
	}
	require.Equal(t, last, written)
}

func TestTelemetryPacerWindowIsLive(t *testing.T) {
	var p telemetryPacer
	require.Equal(t, telemetryApplyEvery, p.window())
	p.setWindow(time.Minute)
	require.Equal(t, time.Minute, p.window())
	p.setWindow(time.Hour)
	require.Equal(t, maxTelemetryApplyEvery, p.window())
	p.setWindow(0)
	require.Equal(t, telemetryApplyEvery, p.window())

	k := newTelKey(t)
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	p.setWindow(time.Minute)
	require.Len(t, p.offer(k, telSnap{sent: 1}, t0), 1)
	require.Len(t, p.offer(k, telSnap{sent: 2}, t0.Add(61*time.Second)), 1)
}

func TestTelemetryPacerForgetsIdleEntries(t *testing.T) {
	var p telemetryPacer
	k := newTelKey(t)
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	p.offer(k, telSnap{sent: 1}, t0)
	p.due(t0.Add(telemetryStateSweepAfter + time.Minute))
	require.Empty(t, p.m)
}

// A write that failed is retried at the next flush, and an identical report
// after it is not skipped as already written.
func TestTelemetryPacerRetriesFailedWrites(t *testing.T) {
	var p telemetryPacer
	k := newTelKey(t)
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	out := p.offer(k, telSnap{sent: 10}, t0)
	require.Len(t, out, 1)
	p.retry([]heldSnap{{k, out[0]}})

	held := p.due(t0.Add(telemetryFlushEvery))
	require.Len(t, held, 1, "the failed row is due again")
	require.EqualValues(t, 10, held[0].s.sent)

	p.retry(held)
	require.Len(t, p.offer(k, telSnap{sent: 10}, t0.Add(2*telemetryFlushEvery)), 1, "not skipped as unchanged")
}

// A transport type round-trips through its code, and the table stops
// growing once full.
func TestTpCodeTable(t *testing.T) {
	tpCodes.Lock()
	names, idx := slices.Clone(tpCodes.names), maps.Clone(tpCodes.idx)
	tpCodes.Unlock()
	defer func() {
		tpCodes.Lock()
		tpCodes.names, tpCodes.idx = names, idx
		tpCodes.Unlock()
	}()
	for _, s := range []string{"", "stcpr", "dmsg", "stcpr", "legacy-type"} {
		require.Equal(t, s, tpCodeOf(s).String())
	}
	for i := 0; i < 300; i++ {
		tpCodeOf(fmt.Sprintf("junk-%d", i))
	}
	require.Equal(t, "", tpCodeOf("one-too-many").String())
	require.Equal(t, "stcpr", tpCodeOf("stcpr").String())
}
