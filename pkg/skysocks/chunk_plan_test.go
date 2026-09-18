package skysocks

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

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

// The plan's three numbers are knobs: a bench sweeping granularity moves them
// on the running client instead of rebuilding it. Unset, the plan is the one
// the constants describe (TestSettingDefaultsMatchConstants holds that).
func TestChunkPlanFollowsTheKnobs(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := &Client{rs: rangeSplitConfig{chunkSize: 4 << 20, concurrency: 8}}
	require.EqualValues(t, rsProbeChunkBytes, c.probeChunkBytes())

	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.ChunkProbeBytes: 1 << 20,
		skysettings.ChunkMinBytes:   2 << 20,
		skysettings.ChunkPerTunnel:  4,
	}))
	require.EqualValues(t, 1<<20, c.probeChunkBytes())
	// 40 MiB over 2 tunnels at 4 chunks each targets 5 MiB, capped by the
	// 4 MiB ceiling; the 2 MiB floor shows on the small object below.
	require.EqualValues(t, 4<<20, chunkTarget(40<<20, 2, 4<<20))
	require.EqualValues(t, 2<<20, chunkTarget(4<<20, 2, 4<<20), "the floor knob governs")
}

// TestPlanUploadChunkFollowsTheObject is the 2026-09-17 sweep as a table
// (bench/2026-09-16/2ca6cf7b3-sweep): the 10 MB cell wants ~2 MiB chunks, which
// at the fixed 4 MiB step it could not have (three chunks over two tunnels split
// 2+1), while 50 MB wants the ceiling and gets it.
func TestPlanUploadChunkFollowsTheObject(t *testing.T) {
	const ceiling = int64(defaultRSChunkSize) // 4 MiB, the upload.chunk_bytes default
	cases := []struct {
		name       string
		total      int64
		tunnels    int
		ceiling    int64
		wantSize   int64
		wantChunks int64
	}{
		// The 10 MB cell: 2.5 MB chunks, four of them, two per tunnel — the
		// granularity the sweep measured at 6.47 MB/s against 5.25 at 4 MiB.
		{"10MB over 2 tunnels", 10_000_000, 2, ceiling, 2_500_000, 4},
		// 50 MB: the target is 12.5 MB, the ceiling holds it at 4 MiB, and evening
		// the remainder turns 12 chunks and a runt into 12 equal ones.
		{"50MB over 2 tunnels", 50_000_000, 2, ceiling, 4_166_667, 12},
		{"100MB over 2 tunnels", 100_000_000, 2, ceiling, 4_166_667, 24},
		// A 4 MiB object is exactly upload.stripe_min_bytes, so it IS striped: the
		// 1 MiB floor is what stops it being cut finer than a chunk's prelude pays.
		{"4MiB over 2 tunnels floors at 1MiB", 4 << 20, 2, ceiling, 1 << 20, 4},
		{"4MiB over 1 tunnel", 4 << 20, 1, ceiling, 2 << 20, 2},
		// One tunnel, 10 MB: the target is half the object, so the ceiling binds.
		{"10MB over 1 tunnel", 10_000_000, 1, ceiling, 3_333_334, 3},
		// The knob as a CEILING: set to 1 MiB it caps the plan, it never raises it.
		{"a 1MiB ceiling caps the plan", 10_000_000, 2, 1 << 20, 1_000_000, 10},
	}
	for _, tc := range cases {
		size := planUploadChunk(tc.total, tc.tunnels, tc.ceiling)
		if size != tc.wantSize {
			t.Errorf("%s: planUploadChunk = %d, want %d", tc.name, size, tc.wantSize)
			continue
		}
		if size > tc.ceiling {
			t.Errorf("%s: planned chunk %d exceeds the %d ceiling", tc.name, size, tc.ceiling)
		}
		n := numChunks(tc.total, size)
		if n != tc.wantChunks {
			t.Errorf("%s: %d chunks of %d, want %d", tc.name, n, size, tc.wantChunks)
		}
		// The chunks cover the object and the last one is no runt.
		if last := tc.total - (n-1)*size; last <= 0 || last > size || last < size/2 {
			t.Errorf("%s: last chunk %d beside %d", tc.name, last, size)
		}
	}
}

// TestPlanUploadChunkWithoutALength: a body whose length is unknown has nothing
// to plan against, and the ceiling is then the chunk — the behavior every
// upload had before the plan existed.
func TestPlanUploadChunkWithoutALength(t *testing.T) {
	if got := planUploadChunk(0, 2, 4<<20); got != 4<<20 {
		t.Errorf("planUploadChunk with no total = %d, want the %d ceiling", got, 4<<20)
	}
}
