// Package skysocks pkg/skysocks/chunk_plan.go — chunk granularity for a
// range-split download and for a striped upload (planUploadChunk, at the foot
// of this file, is the same arithmetic without the probe chunk).
//
// A fixed 4 MiB chunk is right for a 100 MB object and wrong for a 10 MB one:
// three chunks over two tunnels split 2+1, so one tunnel carries two thirds of
// the file and the download finishes at that tunnel's rate. Measured live
// 2026-09-16 (bench/2026-09-16/67dddb8be/mux-compose-T2xL2), two tunnels of two
// legs each beat the best single route by x1.52 at 100 MB and x1.65 at 50 MB but
// LOST at 10 MB (x0.79) — the composition's start-up cost, of which granularity
// is one part.
//
// The plan here sizes a chunk from the object instead: roughly two chunks per
// active tunnel, floored at 1 MiB (below that the per-chunk prelude dominates)
// and capped by the configured chunk size, which stays the operator's CEILING
// (RANGE_CHUNK_KIB / the app's --range-chunk arg), never a fixed step.
package skysocks

const (
	// rsMinChunkBytes is the smallest chunk the planner will produce. Each
	// chunk costs a stream open, a SOCKS5 handshake and a ranged GET (one exit
	// round trip, pipelined); under ~1 MiB that prelude starts to cost more
	// than the parallelism it buys.
	rsMinChunkBytes = 1 << 20
	// rsChunksPerTunnel is how many chunks the planner aims to give each ACTIVE
	// tunnel. Two, not one: a second chunk per tunnel keeps a tunnel busy while
	// the previous chunk's tail drains, and gives the in-order writer something
	// to hand the browser without waiting on the slowest tunnel's only chunk.
	rsChunksPerTunnel = 2
	// rsProbeChunkBytes is what chunk0 — which doubles as the size probe, since
	// the total is only known from its Content-Range — asks for. It is also the
	// no-split threshold: an object this size or smaller arrives whole on
	// stream0 and is never split.
	//
	// It is deliberately SMALLER than the default chunk size. chunk0 is the one
	// chunk whose size cannot follow the object (it is requested before the
	// total is known), so a large probe is a large un-plannable lump: at a 4 MiB
	// probe a 10 MB object puts 42% of itself on one stream no matter how the
	// rest is planned.
	rsProbeChunkBytes = 2 << 20
)

// probeChunkBytes is the byte count chunk0's range asks for: the chunk.probe_bytes
// knob (rsProbeChunkBytes by default), or the chunk ceiling in force when the
// operator has capped it lower.
func (c *Client) probeChunkBytes() int64 {
	n := setChunkProbeBytes()
	if ceil := c.rsChunkSize(); ceil > 0 && ceil < n {
		n = ceil
	}
	return n
}

// activeTunnels is how many tunnels can carry a chunk right now — the held
// sessions minus the standby pool. Never less than 1.
func (c *Client) activeTunnels() int {
	held, standby, _, _, _ := c.StandbyPoolState()
	if n := held - standby; n > 0 {
		return n
	}
	return 1
}

// chunkTarget is the planner's target chunk size for an object of total bytes
// spread over `tunnels` active tunnels: total/(chunk.per_tunnel×tunnels),
// clamped to [chunk.min_bytes, ceiling]. The two knobs default to
// rsChunksPerTunnel and rsMinChunkBytes.
func chunkTarget(total int64, tunnels int, ceiling int64) int64 {
	if ceiling <= 0 {
		ceiling = defaultRSChunkSize
	}
	if tunnels < 1 {
		tunnels = 1
	}
	per := setChunkPerTunnel()
	if per < 1 {
		per = rsChunksPerTunnel
	}
	size := total / int64(per*tunnels)
	if floor := setChunkMinBytes(); size < floor {
		size = floor
	}
	if size > ceiling {
		size = ceiling
	}
	return size
}

// evenChunkSize rounds target up so the remaining bytes divide into EQUAL
// chunks: ceil(remaining/target) chunks of ceil(remaining/n) bytes. Without it
// the last chunk is a runt — a 7.9 MB remainder at a 2.5 MB target ends in a
// 0.4 MB chunk that pays a full prelude for a tenth of a chunk's work — and a
// runt on the slowest tunnel is the one everything else waits for.
func evenChunkSize(remaining, target int64) int64 {
	if remaining <= 0 || target <= 0 {
		return target
	}
	n := (remaining + target - 1) / target
	if n < 1 {
		n = 1
	}
	return (remaining + n - 1) / n
}

// planChunkSize is the chunk size for the part of an object that follows
// chunk0: the per-object target for the current active tunnel count, rounded to
// divide the remainder evenly.
func (c *Client) planChunkSize(total, chunk0Len int64) int64 {
	return evenChunkSize(total-chunk0Len, chunkTarget(total, c.activeTunnels(), c.rsChunkSize()))
}

// planUploadChunk is the chunk size a STRIPED UPLOAD cuts one object with. It is
// the download plan without the probe: an upload knows the total from
// Content-Length before it sends a byte, so every chunk follows the object
// rather than all but the first.
//
// Measured live 2026-09-17 (bench/2026-09-16/2ca6cf7b3-sweep/sweep): at the fixed
// 4 MiB chunk a 10 MB upload over two tunnels ran 5.25 MB/s — half to two thirds
// of the single-route reference — because three chunks over two tunnels split
// 2+1 and the object finished at one tunnel's rate. At 2 MiB the same upload ran
// 6.47 (+23 %). A 50 MB upload wants the ceiling either way (9.84 at 4 MiB, 8.80
// at 2 MiB), which is what a ceiling-clamped target gives it. upload.concurrency
// moved neither cell (10 MB 5.02 at 2 vs 5.38 at 8; 50 MB 9.84 vs 8.87), so the
// granularity is the object's business and not the depth in flight.
//
// ceiling is upload.chunk_bytes, which stays the operator's CEILING: the planned
// size never exceeds it, and an operator who caps it lower gets the cap.
func planUploadChunk(total int64, tunnels int, ceiling int64) int64 {
	if total <= 0 {
		return ceiling
	}
	return evenChunkSize(total, chunkTarget(total, tunnels, ceiling))
}
