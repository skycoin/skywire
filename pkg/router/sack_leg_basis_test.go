// Package router pkg/router/sack_leg_basis_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// storeAged puts one frame in the retx buffer, tagged with the leg it rode and
// aged by age. Mirrors what the send path records (retxBuffer.Store + the
// sentAt stamp) without needing a live group.
func storeAged(m *routeMux, seq uint32, tp uuid.UUID, age time.Duration) {
	m.retxBuf.Store(seq, []byte("frame"), tp)
	m.retxBuf.mu.Lock()
	m.retxBuf.entries[seq].sentAt = time.Now().Add(-age)
	m.retxBuf.mu.Unlock()
}

// TestSACKDefersFrameYoungerThanItsLegBasis is the duplicate-bytes regression:
// two legs at 30 ms and 150 ms end-to-end with an even stripe, and a SACK that
// names BOTH legs' in-flight frames as missing. The 150 ms leg's frame is
// younger than that leg's own delay basis × the reorder margin, so it is still
// in ordinary flight there and must not be resent (it is counted as deferred);
// the 30 ms leg's frame of the same age is genuinely overdue and is.
func TestSACKDefersFrameYoungerThanItsLegBasis(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("sack-leg-basis-test")
	m := newRouteMux(log, true)
	// Both legs leave over a near first hop, so the group-wide basis reads fast
	// for both (this is the measured shape: the near edge tells you nothing
	// about the route). The group threshold lands on its 60 ms floor.
	setLegRTTs(m, []float64{30, 30})
	fast, slow := uuid.New(), uuid.New()
	m.setLegE2ERTT(fast, 30)
	m.setLegE2ERTT(slow, 150)
	require.Equal(t, rackFloorDefault, m.rackThreshold(), "group threshold at its floor")

	// Seq 7 rode the slow leg, seq 8 the fast one; both 100 ms old — past the
	// group's 60 ms, well inside the slow leg's 150×1.25 = 187.5 ms.
	storeAged(m, 7, slow, 100*time.Millisecond)
	storeAged(m, 8, fast, 100*time.Millisecond)

	got := m.onSACKReceived(6, []uint64{0b100}, 0, false) // bit 2 = seq 9 received
	require.Equal(t, []uint32{8}, got, "only the fast leg's hole is overdue")
	require.Equal(t, uint64(1), m.retxDeferredYoung.Load(),
		"the slow leg's in-flight frame is counted as deferred, not retransmitted")
}

// TestSACKRetransmitsOncePastLegBasis is the other half: the same frame, once
// it has aged past its own leg's basis, IS retransmitted — the deferral delays
// loss recovery by the leg's own delay, it does not disable it.
func TestSACKRetransmitsOncePastLegBasis(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("sack-leg-basis-test")
	m := newRouteMux(log, true)
	setLegRTTs(m, []float64{30, 30})
	slow := uuid.New()
	m.setLegE2ERTT(slow, 150)

	storeAged(m, 7, slow, 400*time.Millisecond) // > 150 ms × 1.25
	got := m.onSACKReceived(6, []uint64{0b100}, 0, false)
	require.Equal(t, []uint32{7}, got, "past its own leg's basis the hole is a loss")
	require.Zero(t, m.retxDeferredYoung.Load())
}

// TestQueueDeepLegWaitsBasisPlusMargin pins the clamp that produced the storm:
// a leg whose measured delay is past rackCeilDefault used to have its wait pulled back
// to exactly one basis — the MEAN of its delay distribution — so every frame
// slower than that mean was resent while in flight (the 14.6 MB-on-the-wire
// 10 MB upload). The wait is now the basis PLUS the reorder margin in that
// range too.
func TestQueueDeepLegWaitsBasisPlusMargin(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("sack-leg-basis-test")
	m := newRouteMux(log, true)
	setLegRTTs(m, []float64{20})
	deep := uuid.New()
	for i := 0; i < 6; i++ {
		m.recordAckDelayTp(deep, 2*time.Second) // a queue-deep leg: basis ≈ 2 s
	}
	require.Greater(t, m.rackThresholdFor(deep), 2*time.Second,
		"a basis past rackCeilDefault still earns its reorder margin")

	storeAged(m, 7, deep, 2200*time.Millisecond) // above the bare basis, below basis×1.25
	require.Empty(t, m.onSACKReceived(6, []uint64{0b100}, 0, false),
		"a frame inside the leg's basis+margin is in flight, not lost")
	require.Equal(t, uint64(1), m.retxDeferredYoung.Load())
}

// TestSingleLegGroupJudgedByGroupThreshold is the no-change case: with one leg
// the leg's basis is the group basis, so the group threshold governs exactly as
// before and nothing is ever deferred.
func TestSingleLegGroupJudgedByGroupThreshold(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("sack-leg-basis-test")
	m := newRouteMux(log, true)
	setLegRTTs(m, []float64{200}) // group threshold = 200 × 1.25 = 250 ms
	only := uuid.New()
	m.setLegE2ERTT(only, 200)
	require.Equal(t, m.rackThreshold(), m.rackThresholdFor(only),
		"a leg at the group basis keeps the group threshold")

	storeAged(m, 7, only, 100*time.Millisecond)
	require.Empty(t, m.onSACKReceived(6, []uint64{0b100}, 0, false), "younger than the threshold")
	require.Zero(t, m.retxDeferredYoung.Load(), "no per-leg deferral on a single-leg group")

	storeAged(m, 7, only, 400*time.Millisecond)
	require.Equal(t, []uint32{7}, m.onSACKReceived(6, []uint64{0b100}, 0, false), "older than the threshold")
	require.Zero(t, m.retxDeferredYoung.Load())
}
