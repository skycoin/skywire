//go:build !tinygo || (js && wasm)

package router

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// TestHolGapThreshold pins the frontier-gap-age threshold: a few-ms floor, else
// about one fastest-live-leg RTT, with a non-positive RTT falling back to floor.
func TestHolGapThreshold(t *testing.T) {
	tests := []struct {
		name      string
		fastestMs float64
		want      time.Duration
	}{
		{"no measurement -> floor", 0, holRetxGapFloor},
		{"negative -> floor", -5, holRetxGapFloor},
		{"tiny LAN RTT clamps to floor", 1, holRetxGapFloor},
		{"at floor boundary", 4, 4 * time.Millisecond},
		{"one fast-leg RTT", 20, 20 * time.Millisecond},
		{"wan RTT", 150, 150 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := holGapThreshold(tt.fastestMs); got != tt.want {
				t.Errorf("holGapThreshold(%v) = %v, want %v", tt.fastestMs, got, tt.want)
			}
		})
	}
}

// TestHolPerSeqInterval pins the per-seq re-nudge spacing: a floor, else one
// fast-leg RTT, non-positive RTT falling back to floor.
func TestHolPerSeqInterval(t *testing.T) {
	tests := []struct {
		name      string
		fastestMs float64
		want      time.Duration
	}{
		{"no measurement -> floor", 0, holRetxPerSeqFloor},
		{"tiny RTT clamps to floor", 2, holRetxPerSeqFloor},
		{"one fast-leg RTT", 40, 40 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := holPerSeqInterval(tt.fastestMs); got != tt.want {
				t.Errorf("holPerSeqInterval(%v) = %v, want %v", tt.fastestMs, got, tt.want)
			}
		})
	}
}

// bits builds a SACK bitmap word from the set bit indices, so tests can express
// "seqs at these offsets above lastContiguous are received" readably.
func bits(idxs ...uint32) uint64 {
	var w uint64
	for _, i := range idxs {
		w |= 1 << i
	}
	return w
}

// TestFrontierMissingSeqs pins extraction of the head-of-line run from a SACK:
// the stuck frontier (lastContiguous+1) plus contiguous holes behind it, stopping
// at the first received seq, capped at max, and nil for an empty bitmap.
func TestFrontierMissingSeqs(t *testing.T) {
	tests := []struct {
		name  string
		last  uint32
		words []uint64
		max   int
		want  []uint32
	}{
		{
			name: "empty bitmap -> nil (no HoL stall)",
			last: 10, words: nil, max: 4, want: nil,
		},
		{
			name: "max zero -> nil",
			last: 10, words: []uint64{bits(1, 2)}, max: 0, want: nil,
		},
		{
			// frontier=11 missing (bit0 unset), 12 received (bit1 set) ends the run.
			name: "single frontier hole, next received",
			last: 10, words: []uint64{bits(1, 2, 3)}, max: 4, want: []uint32{11},
		},
		{
			// bits 0..2 unset (11,12,13 missing), bit3 set (14 received) ends run.
			name: "contiguous run of holes then received",
			last: 10, words: []uint64{bits(3, 5)}, max: 4, want: []uint32{11, 12, 13},
		},
		{
			// all-zero word: every seq in window missing, capped at max.
			name: "all missing, capped at max",
			last: 100, words: []uint64{0}, max: 4, want: []uint32{101, 102, 103, 104},
		},
		{
			// hole run spans the 64-bit word boundary: word0 all unset (seqs 1..64),
			// word1 bit0 unset (seq 65), word1 bit1 set (seq 66 received) ends the
			// run. max is generous so the word boundary, not the cap, is what stops it.
			name: "spans word boundary",
			last: 0, words: []uint64{0, bits(1)}, max: 128, want: seqRange(1, 65),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := frontierMissingSeqs(tt.last, tt.words, tt.max)
			if !equalSeqs(got, tt.want) {
				t.Errorf("frontierMissingSeqs(%d,%v,%d) = %v, want %v", tt.last, tt.words, tt.max, got, tt.want)
			}
		})
	}
}

// TestHolRetxTrackerDue pins the per-seq rate limit: first send always due, a
// re-send inside the interval refused, a re-send past the interval allowed, and
// distinct seqs tracked independently.
func TestHolRetxTrackerDue(t *testing.T) {
	tr := newHolRetxTracker()
	base := time.Unix(0, 0)
	interval := 20 * time.Millisecond

	if !tr.Due(5, interval, base) {
		t.Fatal("first nudge of seq 5 must be due")
	}
	if tr.Due(5, interval, base.Add(5*time.Millisecond)) {
		t.Error("seq 5 re-nudged inside interval must NOT be due")
	}
	if tr.Due(5, interval, base.Add(19*time.Millisecond)) {
		t.Error("seq 5 just under interval must NOT be due")
	}
	if !tr.Due(5, interval, base.Add(25*time.Millisecond)) {
		t.Error("seq 5 past interval must be due again")
	}
	// A different seq is independent — always due on first sight.
	if !tr.Due(6, interval, base.Add(5*time.Millisecond)) {
		t.Error("distinct seq 6 must be due on first sight regardless of seq 5")
	}
}

// TestHolRetxTrackerPurge: Purge drops tracked seqs at/below the acked frontier
// so the map stays bounded, and a purged seq is due again immediately.
func TestHolRetxTrackerPurge(t *testing.T) {
	tr := newHolRetxTracker()
	base := time.Unix(0, 0)
	interval := time.Second

	tr.Due(10, interval, base)
	tr.Due(11, interval, base)
	tr.Due(12, interval, base)

	tr.Purge(11) // drop 10 and 11

	// 12 is still tracked -> not due inside interval.
	if tr.Due(12, interval, base.Add(time.Millisecond)) {
		t.Error("seq 12 should still be rate-limited after purging <=11")
	}
	// 10 was purged -> due again immediately.
	if !tr.Due(10, interval, base.Add(time.Millisecond)) {
		t.Error("purged seq 10 must be due again")
	}
}

// TestFastestLegLatency: lowest positive RTT among live, ready, non-standby legs;
// skips standby / not-ready / closed / unmeasured; 0 when none qualifies.
func TestFastestLegLatency(t *testing.T) {
	t.Run("lowest positive among ready", func(t *testing.T) {
		m := &routeMux{}
		m.growLegs(3)
		m.markLegReady(1)
		m.markLegReady(2)
		tps := []*transport.ManagedTransport{mockTP(200), mockTP(50), mockTP(300)}
		if got := m.fastestLegLatency(tps); got != 50 {
			t.Errorf("fastestLegLatency = %v, want 50", got)
		}
	})
	t.Run("skips standby fastest", func(t *testing.T) {
		m := &routeMux{}
		m.growLegs(3)
		m.markLegReady(1)
		m.markLegReady(2)
		m.setLegStandby(1, true) // fastest but parked
		tps := []*transport.ManagedTransport{mockTP(200), mockTP(50), mockTP(300)}
		if got := m.fastestLegLatency(tps); got != 200 {
			t.Errorf("fastestLegLatency = %v, want 200 (standby 50 skipped)", got)
		}
	})
	t.Run("skips not-ready aux leg", func(t *testing.T) {
		m := &routeMux{}
		m.growLegs(2) // leg 1 never marked ready
		tps := []*transport.ManagedTransport{mockTP(200), mockTP(20)}
		if got := m.fastestLegLatency(tps); got != 200 {
			t.Errorf("fastestLegLatency = %v, want 200 (leg1 not ready)", got)
		}
	})
	t.Run("no measurement -> 0", func(t *testing.T) {
		m := &routeMux{}
		m.growLegs(2)
		m.markLegReady(1)
		tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
		if got := m.fastestLegLatency(tps); got != 0 {
			t.Errorf("fastestLegLatency = %v, want 0", got)
		}
	})
}

// TestProactiveRetxSeqs is the sender-side decision, including the
// capability-gated fallback: with HoL retx OFF it is a clean no-op; with it ON
// it returns held frontier seqs, skips seqs not in the retx buffer, and per-seq
// rate-limits a re-nudge inside one interval.
func TestProactiveRetxSeqs(t *testing.T) {
	newMux := func(holOn bool) *routeMux {
		m := newRouteMux(nil, true) // sackEnabled true
		m.holRetxEnabled = holOn
		return m
	}
	// SACK: lastContiguous=100, bitmap word with bits 0..2 unset (101,102,103
	// missing) and bit3 set (104 received) — frontier run is 101,102,103.
	last := uint32(100)
	words := []uint64{bits(3)}
	now := time.Unix(0, 0)

	t.Run("capability off -> nil fallback", func(t *testing.T) {
		m := newMux(false)
		m.retxBuf.Store(101, []byte("a"))
		m.retxBuf.Store(102, []byte("b"))
		if got := m.proactiveRetxSeqs(last, words, 20, now); got != nil {
			t.Errorf("HoL disabled must return nil, got %v", got)
		}
	})

	t.Run("returns held frontier seqs", func(t *testing.T) {
		m := newMux(true)
		m.retxBuf.Store(101, []byte("a"))
		m.retxBuf.Store(102, []byte("b"))
		m.retxBuf.Store(103, []byte("c"))
		got := m.proactiveRetxSeqs(last, words, 20, now)
		if !equalSeqs(got, []uint32{101, 102, 103}) {
			t.Errorf("got %v, want [101 102 103]", got)
		}
	})

	t.Run("skips seqs not in retx buffer", func(t *testing.T) {
		m := newMux(true)
		// 102 already acked/purged -> not retransmitted; 101,103 held.
		m.retxBuf.Store(101, []byte("a"))
		m.retxBuf.Store(103, []byte("c"))
		got := m.proactiveRetxSeqs(last, words, 20, now)
		if !equalSeqs(got, []uint32{101, 103}) {
			t.Errorf("got %v, want [101 103]", got)
		}
	})

	t.Run("per-seq rate limit within interval", func(t *testing.T) {
		m := newMux(true)
		m.retxBuf.Store(101, []byte("a"))
		m.retxBuf.Store(102, []byte("b"))
		m.retxBuf.Store(103, []byte("c"))
		// fastestMs=20 -> interval 20ms.
		first := m.proactiveRetxSeqs(last, words, 20, now)
		if !equalSeqs(first, []uint32{101, 102, 103}) {
			t.Fatalf("first nudge got %v, want [101 102 103]", first)
		}
		// Immediately again: all inside interval -> nothing due.
		if again := m.proactiveRetxSeqs(last, words, 20, now.Add(5*time.Millisecond)); again != nil {
			t.Errorf("re-nudge inside interval must be nil, got %v", again)
		}
		// Past the interval -> due again.
		later := m.proactiveRetxSeqs(last, words, 20, now.Add(25*time.Millisecond))
		if !equalSeqs(later, []uint32{101, 102, 103}) {
			t.Errorf("re-nudge past interval got %v, want [101 102 103]", later)
		}
	})

	t.Run("empty bitmap -> nil (no stall)", func(t *testing.T) {
		m := newMux(true)
		m.retxBuf.Store(101, []byte("a"))
		if got := m.proactiveRetxSeqs(last, nil, 20, now); got != nil {
			t.Errorf("empty SACK bitmap must return nil, got %v", got)
		}
	})
}

// TestMergeSeqs pins the de-dup union used to fold a proactive nudge into the
// reactive SACK retransmit list so a shared seq is sent only once.
func TestMergeSeqs(t *testing.T) {
	tests := []struct {
		name string
		a, b []uint32
		want []uint32
	}{
		{"both empty", nil, nil, nil},
		{"a empty", nil, []uint32{1, 2}, []uint32{1, 2}},
		{"b empty", []uint32{3}, nil, []uint32{3}},
		{"disjoint", []uint32{1, 2}, []uint32{3, 4}, []uint32{1, 2, 3, 4}},
		{"overlap deduped", []uint32{1, 2, 3}, []uint32{2, 3, 5}, []uint32{1, 2, 3, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeSeqs(tt.a, tt.b); !equalSeqs(got, tt.want) {
				t.Errorf("mergeSeqs(%v,%v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// --- small test helpers ---

func seqRange(lo, hi uint32) []uint32 {
	out := make([]uint32, 0, hi-lo+1)
	for s := lo; s <= hi; s++ {
		out = append(out, s)
	}
	return out
}

func equalSeqs(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// compile-time assurance the routing capability bit is distinct from its
// neighbors (a bug here would silently alias two capabilities on the wire).
var _ = func() bool {
	if routing.CapHOLRetx == routing.CapPerFrameNoise || routing.CapHOLRetx == routing.CapSACK {
		panic("CapHOLRetx aliases another capability bit")
	}
	return true
}()
