package skysocks

import "testing"

// TestChunkTargetFollowsObject proves the target chunk size is the object over
// two chunks per active tunnel, clamped to [1 MiB, ceiling].
func TestChunkTargetFollowsObject(t *testing.T) {
	const ceiling = defaultRSChunkSize // 4 MiB
	cases := []struct {
		name    string
		total   int64
		tunnels int
		want    int64
	}{
		// The 10 MB composition cell: 2 tunnels → 10e6/4 = 2.5 MB.
		{"10MB over 2 tunnels", 10_000_000, 2, 2_500_000},
		// A 100 MB object wants 12.5 MB chunks; the ceiling holds it at 4 MiB,
		// so large objects behave exactly as before.
		{"100MB over 2 tunnels", 100_000_000, 2, ceiling},
		// A small object floors at 1 MiB rather than producing sub-MiB chunks
		// whose prelude would cost more than the parallelism.
		{"3MB over 2 tunnels", 3_000_000, 2, rsMinChunkBytes},
		// More tunnels, finer chunks — down to the same floor.
		{"10MB over 4 tunnels", 10_000_000, 4, 1_250_000},
		{"10MB over 8 tunnels", 10_000_000, 8, rsMinChunkBytes},
		// Degenerate tunnel counts never divide by zero.
		{"zero tunnels reads as one", 10_000_000, 0, ceiling},
	}
	for _, tc := range cases {
		if got := chunkTarget(tc.total, tc.tunnels, ceiling); got != tc.want {
			t.Errorf("%s: chunkTarget = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestChunkTargetHonoursCeiling proves RANGE_CHUNK_KIB (the configured chunk
// size) is a CEILING the plan never exceeds, and that a zero ceiling falls back
// to the shipped default rather than to zero.
func TestChunkTargetHonoursCeiling(t *testing.T) {
	if got := chunkTarget(100_000_000, 2, 1<<20); got != 1<<20 {
		t.Errorf("chunkTarget with a 1 MiB ceiling = %d, want %d", got, 1<<20)
	}
	if got := chunkTarget(100_000_000, 2, 0); got != defaultRSChunkSize {
		t.Errorf("chunkTarget with no ceiling = %d, want the default %d", got, defaultRSChunkSize)
	}
}

// TestEvenChunkSizeLeavesNoRunt proves the remainder divides into equal chunks
// rather than ending in a fractional one.
func TestEvenChunkSizeLeavesNoRunt(t *testing.T) {
	remaining, target := int64(7_902_848), int64(2_500_000)
	size := evenChunkSize(remaining, target)
	if size < target*3/4 || size > target {
		t.Fatalf("evenChunkSize = %d, want a value at or just under the %d target", size, target)
	}
	n := (remaining + size - 1) / size
	last := remaining - (n-1)*size
	if last < size/2 {
		t.Errorf("last chunk %d is a runt beside %d", last, size)
	}
	if n != 4 {
		t.Errorf("chunk count = %d, want 4", n)
	}
}

// TestPlanChunkSizeTenMegabyteObject walks the whole plan for the 10 MB cell on
// a client with no tunnels registered (activeTunnels reads as 1): the object is
// a probe chunk plus equal planned chunks, with nothing left over.
func TestPlanChunkSizeTenMegabyteObject(t *testing.T) {
	c := &Client{rs: defaultRangeSplitConfig()}
	const total = int64(10_000_000)
	chunk0 := c.probeChunkBytes()
	if chunk0 != rsProbeChunkBytes {
		t.Fatalf("probeChunkBytes = %d, want %d", chunk0, int64(rsProbeChunkBytes))
	}
	size := c.planChunkSize(total, chunk0)
	if size > c.rs.chunkSize {
		t.Fatalf("planned chunk %d exceeds the %d ceiling", size, c.rs.chunkSize)
	}
	covered := chunk0
	for start := chunk0; start < total; start += size {
		end := start + size - 1
		if end >= total {
			end = total - 1
		}
		covered += end - start + 1
	}
	if covered != total {
		t.Errorf("chunks cover %d bytes of a %d-byte object", covered, total)
	}
}

// TestProbeChunkHonoursLowerCeiling proves an operator who caps the chunk size
// below the probe size gets the capped value as the probe too.
func TestProbeChunkHonoursLowerCeiling(t *testing.T) {
	c := &Client{rs: defaultRangeSplitConfig()}
	c.rs.chunkSize = 512 << 10
	if got := c.probeChunkBytes(); got != 512<<10 {
		t.Errorf("probeChunkBytes = %d, want the %d ceiling", got, 512<<10)
	}
}
