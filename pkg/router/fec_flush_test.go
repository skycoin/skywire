package router

import (
	"testing"
	"time"
)

// TestFECHasPartialBlock: hasPartialBlock is true exactly while a block is
// mid-fill (1..K-1 frames), false on an empty block and false once Add completes
// a block (Add resets filled to 0).
func TestFECHasPartialBlock(t *testing.T) {
	const k, r = 8, 2
	s := newFECStriper(k, r)
	if s == nil {
		t.Fatal("nil striper")
	}
	if s.hasPartialBlock() {
		t.Fatal("empty striper should have no partial block")
	}
	for seq := 0; seq < k-1; seq++ {
		s.Add(uint32(seq), []byte{byte(seq)})
		if !s.hasPartialBlock() {
			t.Fatalf("after %d/%d frames a partial block should be pending", seq+1, k)
		}
	}
	// The K-th frame completes the block; Add resets → no partial block.
	if rf := s.Add(uint32(k-1), []byte{byte(k - 1)}); len(rf) != r {
		t.Fatalf("completing frame should emit %d repair frames, got %d", r, len(rf))
	}
	if s.hasPartialBlock() {
		t.Fatal("a completed block must leave no partial block")
	}
}

// TestFECShouldFlush: the pure idle-flush policy.
func TestFECShouldFlush(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	idle := 50 * time.Millisecond

	cases := []struct {
		name    string
		now     time.Time
		last    time.Time
		idle    time.Duration
		pending bool
		want    bool
	}{
		{"no partial block", base.Add(time.Second), base, idle, false, false},
		{"partial but not yet idle", base.Add(10 * time.Millisecond), base, idle, true, false},
		{"partial and idle exactly", base.Add(idle), base, idle, true, true},
		{"partial and long idle", base.Add(time.Second), base, idle, true, true},
		{"idle disabled", base.Add(time.Second), base, 0, true, false},
	}
	for _, c := range cases {
		if got := fecShouldFlush(c.now, c.last, c.idle, c.pending); got != c.want {
			t.Fatalf("%s: fecShouldFlush=%v want %v", c.name, got, c.want)
		}
	}
}
