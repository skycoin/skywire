// Package skysocks pkg/skysocks/tunnel_depth.go — the per-tunnel queue depth,
// sized from the bandwidth-delay product.
//
// chunk.per_tunnel (2) and upload.concurrency (4) are FIXED queue depths,
// chosen when every tunnel was assumed alike. They are not: the campaign rig
// runs a 40 ms direct tunnel beside a 470 ms Sydney one, and a depth right for
// the first leaves the second empty for most of every round trip while a depth
// right for the second buffers megabytes on the first.
//
// The depth that is right for both is bittorrent's pipelining rule: keep a peer
// with enough outstanding requests to cover one round trip of ITS OWN rate, and
// no more. That is the bandwidth-delay product measured in chunks — one chunk on
// a fast near tunnel, four on a slow far one — and the meter already holds both
// terms, the proven capacity per direction and the smoothed RTT.
//
// It is OFF by default (chunk.depth_dynamic, upload.depth_dynamic), so a client
// that sets nothing keeps the fixed depths byte for byte. The operator turns it
// on live and the default moves only once a rig row says it should.
package skysocks

import (
	"math"
	"time"
)

const (
	// tunnelDepthMargin is added to a tunnel's RTT in the depth: the round trip
	// is not the only delay between asking for a chunk and its first byte (the
	// exit's own open, the origin's first read), and a depth computed from RTT
	// alone leaves the tunnel briefly empty at every chunk boundary.
	tunnelDepthMargin = 50 * time.Millisecond
	// rsDepthMin / rsDepthMax clamp the dynamic depth. The floor is today's
	// fixed depth, so a dynamic run can only ever ADD queue, never take the
	// pipeline below what the fixed build ran.
	rsDepthMin = rsChunksPerTunnel
	rsDepthMax = 8
)

// bdpDepth is how many chunks of chunkBytes it takes to keep a tunnel of
// rateBps busy across delay: ceil(rate x delay / chunkBytes), clamped. delay is
// the FULL delay term — the caller adds tunnel.depth_margin to the RTT — so
// this stays the plain bandwidth-delay product and the table reads as one.
//
// An unmeasured tunnel (no rate, no delay) gets the floor, which is the fixed
// depth it would have had anyway.
func bdpDepth(rateBps float64, delay time.Duration, chunkBytes int64, lo, hi int) int {
	if lo < 1 {
		lo = 1
	}
	if hi < lo {
		hi = lo
	}
	if rateBps <= 0 || delay <= 0 || chunkBytes <= 0 {
		return lo
	}
	n := int(math.Ceil(rateBps * delay.Seconds() / float64(chunkBytes)))
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// tunnelDepth is the per-tunnel queue depth in force for chunks of chunkBytes,
// or fallback when the dynamic depth is switched off or nothing is measured.
//
// One number governs a set of tunnels (the chunk planner sizes one chunk for
// the object; the upload's slot gate multiplies one depth by the live count),
// so the aggregate is the MEAN of the per-tunnel depths: the total work in
// flight is then the sum of what each tunnel's own bandwidth-delay product
// asks for, which is the quantity that actually has to be right. Rounded up,
// so a mixed set is never left with less queue than its slowest member needs.
func (c *Client) tunnelDepth(chunkBytes int64, up bool, fallback int) int {
	if up {
		if !setUploadDepthDynamic() {
			return fallback
		}
	} else if !setChunkDepthDynamic() {
		return fallback
	}
	sessions, meters := c.snubSnapshot()
	now := time.Now()
	margin := setTunnelDepthMargin()
	lo, hi := setChunkDepthMin(), setChunkDepthMax()
	sum, n := 0, 0
	for i, s := range sessions {
		m := meters[i]
		if s == nil || s.IsClosed() || m == nil || c.IsStandby(s) {
			continue
		}
		rate, _ := m.capacityDir(now, up)
		rtt, ok := m.rtt()
		if rate <= 0 || !ok {
			continue
		}
		sum += bdpDepth(rate, time.Duration(rtt*float64(time.Millisecond))+margin, chunkBytes, lo, hi)
		n++
	}
	if n == 0 {
		return fallback
	}
	return (sum + n - 1) / n
}
