// Package router pkg/router/route_mux_leg_attribution_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

// legSentBytes returns the sent-byte count of the leg on transport id, and
// false when the group no longer has that leg.
func legSentBytes(rg *RouteGroup, id uuid.UUID) (uint64, bool) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	idx := rg.legIndexLocked(id)
	if idx < 0 {
		return 0, false
	}
	return rg.mux.legs[idx].sentBytes.Load(), true
}

// requireSentOnlyOn checks the write was credited to the leg on carried and to
// no other leg.
func requireSentOnlyOn(t *testing.T, rg *RouteGroup, mts []*transport.ManagedTransport, carried uuid.UUID) {
	t.Helper()
	for _, mt := range mts {
		sent, ok := legSentBytes(rg, mt.Entry.ID)
		if !ok {
			continue
		}
		if mt.Entry.ID == carried {
			require.NotZero(t, sent, "the leg that carried the frame must be credited")
		} else {
			require.Zero(t, sent, "a leg that carried nothing must not be credited")
		}
	}
}

// TestSentBytesFollowLegAcrossPrimarySwap checks a write is credited to the leg
// it went out on when a primary swap lands between the pick and the write.
func TestSentBytesFollowLegAcrossPrimarySwap(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 3)
	payload := []byte("payload")

	rg.mu.Lock()
	tp, rule, leg, err := rg.nextTransport(payload)
	rg.mu.Unlock()
	require.NoError(t, err)

	// Move the picked leg to another index, as a re-election during the write does.
	swap := tp.Entry.ID
	if tp == mts[0] {
		swap = mts[1].Entry.ID
	}
	rg.reelectPrimary(swap)

	_, err = rg.write(payload, tp, rule, leg)
	require.NoError(t, err)
	requireSentOnlyOn(t, rg, mts, tp.Entry.ID)
}

// TestSentBytesFollowLegAcrossRemoval checks a write is credited to the leg it
// went out on when a lower leg is removed between the pick and the write.
func TestSentBytesFollowLegAcrossRemoval(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 3)
	payload := []byte("payload")

	// Pick until the leg is the last one, so the removal shifts it down.
	last := mts[len(mts)-1]
	rg.mu.Lock()
	tp, rule, leg, err := rg.nextTransport(payload)
	for i := 0; i < 64 && err == nil && tp != last; i++ {
		tp, rule, leg, err = rg.nextTransport(payload)
	}
	rg.mu.Unlock()
	require.NoError(t, err)
	require.Same(t, last, tp, "the selector never picked the last leg")

	require.Equal(t, 1, rg.removeLegsByID([]uuid.UUID{mts[1].Entry.ID}, "test", "removed"))

	_, err = rg.write(payload, tp, rule, leg)
	require.NoError(t, err)
	requireSentOnlyOn(t, rg, mts, tp.Entry.ID)
}

// BenchmarkMuxRecordSent measures the per-packet sent accounting for one leg.
func BenchmarkMuxRecordSent(b *testing.B) {
	m := newBareMux(false)
	m.growLegs(4)
	b.ReportAllocs()
	for b.Loop() {
		m.recordSent(2, 1400)
	}
}
