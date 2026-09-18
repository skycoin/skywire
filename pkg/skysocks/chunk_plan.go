// Package skysocks pkg/skysocks/chunk_plan.go — chunk granularity for a
// range-split download.
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

// probeChunkBytes is the byte count chunk0's range asks for: rsProbeChunkBytes,
// or the configured chunk size when the operator has capped it lower.
func (c *Client) probeChunkBytes() int64 {
	n := int64(rsProbeChunkBytes)
	if c.rs.chunkSize > 0 && c.rs.chunkSize < n {
		n = c.rs.chunkSize
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
// spread over `tunnels` active tunnels: total/(rsChunksPerTunnel×tunnels),
// clamped to [rsMinChunkBytes, ceiling].
func chunkTarget(total int64, tunnels int, ceiling int64) int64 {
	if ceiling <= 0 {
		ceiling = defaultRSChunkSize
	}
	if tunnels < 1 {
		tunnels = 1
	}
	size := total / int64(rsChunksPerTunnel*tunnels)
	if size < rsMinChunkBytes {
		size = rsMinChunkBytes
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
	return evenChunkSize(total-chunk0Len, chunkTarget(total, c.activeTunnels(), c.rs.chunkSize))
}
