// Package router pkg/router/rack_tlp_test.go c2-net-routing
package router

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// setLegRTTs gives the mux n active legs with the given per-leg EWMA RTTs (ms).
func setLegRTTs(m *routeMux, rtts []float64) {
	m.growLegs(len(rtts))
	m.legMu.Lock()
	for i, r := range rtts {
		m.legs[i].ecfRttMs = r
	}
	m.legMu.Unlock()
}

func TestPTOInterval(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("tlp-test")
	m := newRouteMux(log, true)

	// No RTT yet → conservative default (2× the no-RTT rack default).
	if got, want := m.ptoInterval(), rackDefaultNoRTT*2; got != want {
		t.Fatalf("no-RTT PTO = %v, want %v", got, want)
	}

	// PTO tracks 2× the slowest active leg's RTT.
	setLegRTTs(m, []float64{50, 120, 300})
	if got, want := m.ptoInterval(), time.Duration(300*tlpPTOFactor)*time.Millisecond; got != want {
		t.Fatalf("PTO = %v, want %v (slowest 300ms × %.1f)", got, want, tlpPTOFactor)
	}

	// A very fast path floors (fresh mux: growLegs only grows, never shrinks).
	mFast := newRouteMux(log, true)
	setLegRTTs(mFast, []float64{2, 3})
	if got := mFast.ptoInterval(); got != tlpMinPTO {
		t.Fatalf("fast-path PTO = %v, want floor %v", got, tlpMinPTO)
	}
	// A very slow path caps.
	mSlow := newRouteMux(log, true)
	setLegRTTs(mSlow, []float64{5000})
	if got := mSlow.ptoInterval(); got != tlpMaxPTO {
		t.Fatalf("slow-path PTO = %v, want cap %v", got, tlpMaxPTO)
	}
}

func TestTLPProbeSeq(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("tlp-test")
	m := newRouteMux(log, true)
	setLegRTTs(m, []float64{50}) // PTO = 100ms (floored)

	now := time.Now()

	// Nothing outstanding → no probe.
	if _, due := m.tlpProbeSeq(now); due {
		t.Fatal("probe due with empty retx buffer")
	}

	// Outstanding data, but the sender is NOT yet idle for a PTO → no probe.
	m.retxBuf.Store(10, []byte("a"), 0)
	m.retxBuf.Store(11, []byte("b"), 0)
	m.retxBuf.Store(12, []byte("c"), 0) // tail = 12
	atomic.StoreInt64(&m.lastSendNano, now.UnixNano())
	if _, due := m.tlpProbeSeq(now.Add(10 * time.Millisecond)); due {
		t.Fatal("probe due before a full PTO of idle")
	}

	// Idle past the PTO → probe the TAIL sequence exactly.
	seq, due := m.tlpProbeSeq(now.Add(m.ptoInterval() + time.Millisecond))
	if !due {
		t.Fatal("probe not due after idle PTO with outstanding data")
	}
	if seq != 12 {
		t.Fatalf("probe seq = %d, want tail 12", seq)
	}

	// Probe budget: a second due check probes again, a third is refused.
	if _, due := m.tlpProbeSeq(now.Add(time.Hour)); !due {
		t.Fatal("second probe (within budget) should be due")
	}
	if _, due := m.tlpProbeSeq(now.Add(time.Hour)); due {
		t.Fatalf("probe budget %d exceeded but still firing", tlpMaxProbes)
	}

	// Ack progress resets the budget → probing resumes.
	m.onSACKReceived(9, nil, 0, false) // lastContig advances 0→9
	if _, due := m.tlpProbeSeq(now.Add(time.Hour)); !due {
		t.Fatal("probe should resume after ack-progress reset the budget")
	}
}

func TestDSACKGrowDecay(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("tlp-test")
	m := newRouteMux(log, true)

	if got := atomic.LoadInt64(&m.rackFactorMilli); got != rackFactorMin {
		t.Fatalf("initial reorder factor = %d, want baseline %d", got, rackFactorMin)
	}

	// Each DSACK widens by one grow step, capped at rackFactorMax.
	m.growRackFactor(5)
	if got, want := atomic.LoadInt64(&m.rackFactorMilli), rackFactorMin+rackDSACKGrowStep; got != want {
		t.Fatalf("after 1 DSACK factor = %d, want %d", got, want)
	}
	for i := 0; i < 100; i++ {
		m.growRackFactor(uint32(i))
	}
	if got := atomic.LoadInt64(&m.rackFactorMilli); got != rackFactorMax {
		t.Fatalf("saturated factor = %d, want cap %d", got, rackFactorMax)
	}

	// Clean SACKs decay it back down, never below the baseline.
	for i := 0; i < 1000; i++ {
		m.decayRackFactor()
	}
	if got := atomic.LoadInt64(&m.rackFactorMilli); got != rackFactorMin {
		t.Fatalf("decayed factor = %d, want baseline floor %d", got, rackFactorMin)
	}
}

// TestDSACKWidensThreshold proves the closed loop: a DSACK widens the reorder
// factor, which lengthens the RACK retransmit threshold on a fixed path.
func TestDSACKWidensThreshold(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("tlp-test")
	m := newRouteMux(log, true)
	setLegRTTs(m, []float64{200}) // 200ms slow leg

	base := m.rackThreshold()
	// Feed a SACK carrying a DSACK → factor widens → threshold grows.
	m.onSACKReceived(0, nil, 7, true)
	widened := m.rackThreshold()
	if !(widened > base) {
		t.Fatalf("DSACK did not widen threshold: base=%v widened=%v", base, widened)
	}
	// A clean SACK decays it back toward the baseline.
	m.onSACKReceived(0, nil, 0, false)
	if got := m.rackThreshold(); got >= widened {
		t.Fatalf("clean SACK did not decay threshold: was=%v now=%v", widened, got)
	}
}

func TestSACKTrackerDSACK(t *testing.T) {
	st := newSACKTracker()

	// In-order delivery: no duplicate.
	st.RecordReceived(1)
	st.RecordReceived(2)
	if _, ok := st.takeDSACK(); ok {
		t.Fatal("DSACK reported on clean in-order stream")
	}

	// Re-receiving an already-delivered seq → DSACK for that seq.
	st.RecordReceived(2)
	seq, ok := st.takeDSACK()
	if !ok || seq != 2 {
		t.Fatalf("duplicate-of-delivered: DSACK = (%d,%v), want (2,true)", seq, ok)
	}
	// takeDSACK clears the pending flag (reported once).
	if _, ok := st.takeDSACK(); ok {
		t.Fatal("DSACK still pending after being taken")
	}

	// Re-receiving a buffered out-of-order seq → DSACK too.
	st.RecordReceived(10) // gap: buffered out of order
	st.RecordReceived(10) // duplicate of the buffered seq
	seq, ok = st.takeDSACK()
	if !ok || seq != 10 {
		t.Fatalf("duplicate-of-buffered: DSACK = (%d,%v), want (10,true)", seq, ok)
	}
}

// TestSACKPacketDSACKRoundTrip covers the wire format: a DSACK SACK round-trips,
// a plain SACK reports no DSACK, and an old-format reader still reads the bitmap
// unchanged from a DSACK-bearing packet (backward compatibility).
func TestSACKPacketDSACKRoundTrip(t *testing.T) {
	words := []uint64{0b1011, 0x00000000000000FF}
	lastContig := uint32(4242)

	// Plain SACK: no DSACK field.
	plain := routing.MakeSACKPacket(1, lastContig, words)
	if _, ok := plain.SACKDSACK(); ok {
		t.Fatal("plain SACK reports a DSACK")
	}

	// DSACK SACK: round-trips seq, lastContig, and the bitmap.
	d := routing.MakeSACKPacketWithDSACK(1, lastContig, words, 777)
	if got := d.SACKLastContiguousSeq(); got != lastContig {
		t.Fatalf("lastContig = %d, want %d", got, lastContig)
	}
	if seq, ok := d.SACKDSACK(); !ok || seq != 777 {
		t.Fatalf("SACKDSACK = (%d,%v), want (777,true)", seq, ok)
	}
	gotWords := d.SACKWords()
	if len(gotWords) != len(words) {
		t.Fatalf("word count = %d, want %d", len(gotWords), len(words))
	}
	for i := range words {
		if gotWords[i] != words[i] {
			t.Fatalf("word[%d] = %x, want %x", i, gotWords[i], words[i])
		}
	}

	// Backward compat: the trailing DSACK bytes must not corrupt a reader that
	// only knows the bitmap (SACKWords reads exactly word_count words).
	if len(d.SACKWords()) != len(words) {
		t.Fatal("DSACK trailing field corrupted the bitmap read")
	}
}
