// Package router pkg/router/route_mux.go c2-net-routing
package router

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// LegStats is a snapshot of per-mux-leg traffic counters at a point
// in time. One entry per active route in the rg's tps[] order.
//
// Counters are cumulative since route-group creation (atomic uint64);
// callers wanting rates take two snapshots and divide by elapsed time.
// Used by 'cli proxy mux-info' to show where bandwidth is going across
// the mux'd routes — the missing piece for verifying that adding more
// routes actually aggregates throughput rather than just trading share.
type LegStats struct {
	// Index in the rg's tps[] slice. Stable for the rg's lifetime
	// (transports are appended, never re-ordered) so the index is
	// also a stable identifier for runtime add/remove operations.
	Index int
	// SentBytes / SentPackets are what THIS leg carried outbound.
	SentBytes   uint64
	SentPackets uint64
	// RecvBytes / RecvPackets are what THIS leg carried inbound.
	// Resolved from the reverse-rule's KeyRouteID at packet-handle
	// time, so it reflects what actually arrived on this transport,
	// not what the peer said it sent.
	RecvBytes   uint64
	RecvPackets uint64
	// Retransmits is how many SACK retransmit packets THIS leg has
	// carried. A high retransmits:sentPackets ratio marks a lossy leg —
	// the signal a routing policy needs to shed lossy intermediates and
	// the scheduler needs to deweight them.
	Retransmits uint64
	// GoodputUpBps / GoodputDownBps are this leg's recent goodput split by
	// direction — the EWMA of the SENT (up) and RECV (down) byte deltas per
	// second over the telemetry refresh window (~1s for the status page).
	// Distinct from the cumulative SentBytes/RecvBytes counters above: they
	// are the RATE each direction is moving now, not the lifetime total. 0
	// until a second sample lands. Sampled in snapshotLegs.
	GoodputUpBps   float64
	GoodputDownBps float64
	// GoodputBps is the combined (up+down) recent goodput, retained as the
	// sum of GoodputUpBps+GoodputDownBps for back-compat with callers that
	// want a single figure.
	GoodputBps float64
}

const (
	// goodputEWMAAlpha weights the newest goodput sample in the per-leg
	// bytes/sec EWMA (snapshotLegs). Higher = more responsive, noisier.
	goodputEWMAAlpha = 0.4
	// goodputMinSampleNano is the minimum window between goodput samples: two
	// observers refreshing closer than this reuse the stored rate rather than
	// dividing a tiny byte delta by a tiny interval into a spurious spike.
	goodputMinSampleNano = int64(250 * time.Millisecond)
	// capacityColdFloorFrac is the floor share a just-promoted active leg gets
	// under WeightModeCapacity, as a fraction of the fastest active leg's weight.
	// Big enough that a fresh leg carries a measurable trickle to prove its
	// goodput and ramp; small enough that a persistently slow leg stays near it
	// and can't open a large reorder gap. See rebuildWeights.
	capacityColdFloorFrac = 0.15
)

type legCounters struct {
	sentBytes   uint64 // atomic
	sentPackets uint64 // atomic
	recvBytes   uint64 // atomic
	recvPackets uint64 // atomic
	retransmits uint64 // atomic
	// lastTotalBytes snapshots sentBytes+recvBytes at the previous
	// capacity-weight rebuild; the delta since then is this leg's
	// recent throughput, used by WeightModeCapacity. Touched only
	// under legMu in rebuildWeights, so it needs no atomic.
	lastTotalBytes uint64
	// Goodput-rate sampling (bytes/sec EWMA over the observer's refresh
	// window), maintained by snapshotLegs — NOT the data path and NOT the
	// capacity rebuild. lastRateSentBytes/lastRateRecvBytes/lastRateNano
	// snapshot the sent counter, recv counter and wall clock at the previous
	// sample; each direction's delta over the elapsed window, EWMA-smoothed,
	// is goodputUpBps (sent/sec) / goodputDownBps (recv/sec). Kept separate
	// from lastTotalBytes (which the capacity rebuild resets on its own
	// cadence) so the two samplers never disturb each other. Touched under
	// legMu (snapshotLegs upgrades to a write lock for the sample), so no
	// atomic needed.
	lastRateSentBytes uint64
	lastRateRecvBytes uint64
	lastRateNano      int64
	goodputUpBps      float64
	goodputDownBps    float64
	// ECF (WeightModeECF) per-leg state, maintained by rebuildWeights' ECF
	// branch (under legMu, off the data path). ecfLastSentBytes snapshots the
	// sent counter at the previous ECF refresh so the delta over the refresh
	// window is this leg's send rate (kept separate from lastTotalBytes /
	// lastRateSentBytes so the ECF sampler never disturbs the capacity or
	// telemetry samplers). ecfRttMs / ecfJitterMs are the EWMA'd mean RTT and
	// jitter (sigma) the ECF predicate consumes.
	ecfLastSentBytes uint64
	ecfRttMs         float64
	ecfJitterMs      float64
}

// routeMux encapsulates route multiplexing state and logic.
// It is composed into RouteGroup as an optional field (nil when mux is not negotiated).
// This separates mux concerns (sequencing, reordering, SACK, transport selection)
// from the core route-layer connection managed by RouteGroup.
type routeMux struct {
	logger *logging.Logger

	// Sequence numbering for outgoing packets
	writeSeq uint32 // atomic: next outgoing sequence number
	tpIndex  uint32 // atomic: round-robin fallback index for transport selection

	// lastSACKNano rate-limits receiver-side SACK feedback. Cross-leg
	// reordering from latency skew makes nearly every packet arrive
	// out-of-order, so firing a SACK per out-of-order packet would spawn a
	// goroutine and emit a control packet thousands of times per second.
	// atomic: UnixNano of the last SACK we sent.
	lastSACKNano int64

	// Incoming packet reordering
	reorderBuf *reorderBuffer

	// Adaptive transport weighting based on latency
	tpSelector *transportSelector

	// SACK retransmission
	sackEnabled bool         // true when both peers advertised CapSACK
	sackTracker *sackTracker // receiver: tracks received seqs for SACK generation
	retxBuf     *retxBuffer  // sender: holds unACKed packets for retransmission

	// Per-frame noise (inverse-mux). When CapPerFrameNoise is negotiated the
	// RouteGroup installs these: seal AEAD-encrypts each outgoing frame under
	// its sequence-nonce (in wrapPayload, before retx storage so retransmits
	// resend the sealed frame verbatim); open AEAD-decrypts an incoming frame
	// under its sequence-nonce (in deliverData, before the reorder buffer).
	// Both nil for stream-noise/plain groups (no-op). Set once at handshake
	// completion, read on the data path; a plain word write/read is safe under
	// the RouteGroup's ordering (set-before-first-data, mu-guarded install).
	seal func(seq uint32, plaintext []byte) []byte
	open func(seq uint32, ciphertext []byte) ([]byte, error)

	// Per-leg traffic counters parallel to the rg's tps[] / fwd[] /
	// rvs[] slices. Mutated atomically. Read via Snapshot().
	// legMu guards the slice itself (extended on AppendRoute), not
	// the individual counters.
	legMu sync.RWMutex
	legs  []*legCounters
	// ready[i] reports whether leg i may be SELECTED for sending. The
	// primary leg (0) is ready from the start; an aux leg only becomes
	// ready once we have received a packet (data or handshake) on it,
	// which proves the peer finished registering its rule. Without this,
	// the selector could steer the first writes onto an aux leg the peer
	// has not set up yet — those packets are dropped, the reorder buffer
	// stalls on the missing sequence, and the stream hangs until it is
	// closed (the mux>=2 "0 bytes / close code 0" bug). Guarded by legMu.
	ready []bool
	// standby[i] marks leg i as a WARM STANDBY: its rules stay installed and
	// the keepalive/liveness loops keep it alive, but it is never SELECTED for
	// sending (folded into legReadyAt). A demoted leg still receives on its
	// reverse rule — standby is a send-side decision. Promoting a standby leg
	// is instant (clear the flag) with no route setup, vs the drop→re-dial
	// tear-and-rebuild. Parallel to ready[]; grown/compacted in lockstep.
	// Guarded by legMu. See docs/warm_standby_legs_rfc.md.
	standby []bool

	// ecfLastRebuildNano is the wall-clock (UnixNano) of the previous ECF-state
	// refresh, used to turn each leg's sent-byte delta into a bytes/sec rate.
	// Touched only under legMu in rebuildWeights' ECF branch.
	ecfLastRebuildNano int64

	// standbyNewLegs makes every NEWLY-grown aux leg (index > 0) enter the
	// warm-standby pool instead of going straight into the active send set.
	// The primary leg (index 0) is never affected. Set only when a promoting
	// rotation engine is wired (SetRotation), so the engine's paced,
	// goodput-gated growActive/promote path admits legs one per tick as its
	// throughput signal warrants — instead of every dialed leg going hot the
	// instant it receives its first inbound packet (markLegReady), which
	// floods the active set faster than the reactive stall gate can demote and
	// churns the group into collapse. When false (no rotation engine) new legs
	// stay active-on-add so a non-adaptive group never strands aux legs in
	// standby. Guarded by legMu.
	standbyNewLegs bool
}

// reorderWindow bounds how far the receiver's reorder buffer will hold
// out-of-order packets waiting for a gap to fill before it force-flushes.
//
// The underlying leg transports (stcpr / squicr / sudph) are RELIABLE and
// ordered, so a gap across mux legs is not loss — it is latency SKEW: the
// "missing" sequence is simply in flight on a slower leg and arrives within
// the inter-leg skew window. The buffer must therefore HOLD the gap until it
// fills, never skip it: skipping delivers an out-of-order hole that corrupts
// the noise/TLS byte stream riding the mux (the old maxGap=64 force-flush was
// the root cause of mux>1 wedging under load — the fast leg routinely ran 64
// ahead of a ~tens-of-ms-slower leg, triggering a destructive flush every
// time). Sized to absorb the bandwidth-delay-product of a realistic skew
// (hundreds of ms at multi-MB/s aggregate) so normal mux>1 operation never
// flushes. A flush at this cap is a last-resort OOM guard for a genuinely
// stalled/dead leg — which per-leg liveness prunes, after which the peer
// retransmits that leg's unacked sequences on the surviving legs.
// Sized to the aggregate bandwidth-delay-product per the MPTCP receive-buffer
// requirement B >= 2*sum(BW)*RTT_max: at the ~500 Mbps gigabit target with a
// ~350 ms slowest-active-leg RTT that is ~46 MB ~= 32Ki packets, so the old 2Ki
// (~2.8 MB) window was ~16x too small — it collapsed throughput under wide-mux
// skew and hit the OOM backstop. This is the CAP, not steady occupancy: normal
// skew buffers only a handful; only a very lagged/dead leg approaches it, and at
// the cap the buffer now DROPS excess (never skips) while the leg-dataprogress
// prune + SACK retransmit refill the frontier in order. TODO: make adaptive to
// the measured RTT_max of the active set instead of a flat gigabit-sized cap.
const reorderWindow = 32768

// newRouteMux creates a new routeMux instance with all sub-components initialized.
func newRouteMux(logger *logging.Logger, sackEnabled bool) *routeMux {
	m := &routeMux{
		logger:      logger,
		reorderBuf:  newReorderBuffer(reorderWindow),
		tpSelector:  newTransportSelector(),
		sackEnabled: sackEnabled,
		sackTracker: newSACKTracker(),
		// Sender-side retx window kept in step with the receiver's reorder
		// window so a genuinely-lost sequence is still held for retransmit
		// while the receiver is holding the gap open for it.
		retxBuf: newRetxBuffer(reorderWindow),
	}
	return m
}

// selectTransport picks the next transport/rule pair for sending.
// Uses latency-weighted selection when data is available, falls back to round-robin.
// Returns the index in tps[] alongside the tp/rule so the caller can
// record per-leg byte counts after a successful write.
//
// payload is the upcoming packet's payload bytes (or nil for
// handshake / control / retx paths that don't have a meaningful
// payload). Used by WeightModeSizeThreshold, WeightModeSticky5Tuple,
// WeightModeLatencyAdaptive, and WeightModeDSCPPriority — they
// inspect the payload directly. Other modes ignore it and fall
// back to the schedule-based pick.
//
// NOTE: not thread-safe, caller must hold the RouteGroup mu.
func (m *routeMux) selectTransport(tps []*transport.ManagedTransport, fwd []routing.Rule, payload []byte) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}

	// Payload-inspecting modes: ask the selector for a leg
	// derived from the bytes. Empty payload (handshake / retx)
	// falls through to the schedule-based pick.
	if m.tpSelector != nil && len(payload) > 0 {
		switch m.tpSelector.Mode() {
		case WeightModeSizeThreshold,
			WeightModeSticky5Tuple,
			WeightModeLatencyAdaptive,
			WeightModeDSCPPriority,
			WeightModeECF:
			idx := m.tpSelector.SelectForPayload(payload)
			if idx < len(tps) {
				tp := tps[idx]
				if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
					return tp, fwd[idx], idx, nil
				}
			}
		}
	}

	// Use weighted selector if available
	if m.tpSelector != nil && m.tpSelector.Len() > 0 {
		idx := m.tpSelector.Select()
		if idx < len(tps) {
			tp := tps[idx]
			if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
				return tp, fwd[idx], idx, nil
			}
		}
	}

	// Fallback: round-robin with skip-dead and skip-not-ready. An aux leg
	// the peer has not confirmed yet is skipped so we never send the first
	// packets onto a route whose rule the peer has not registered; the
	// primary leg (0) is always ready, so this loop always finds it.
	n := uint32(len(tps)) //nolint:gosec
	start := atomic.AddUint32(&m.tpIndex, 1) - 1
	for i := uint32(0); i < n; i++ {
		idx := int((start + i) % n) //nolint:gosec
		tp := tps[idx]
		if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
			return tp, fwd[idx], idx, nil
		}
	}

	// EMERGENCY FAILOVER: no ACTIVE leg is selectable — every active leg is
	// dead/not-ready. Rather than fail the send (a dead connection), fall through
	// to any alive, ready WARM-STANDBY leg. The 512-deep standby reserve exists
	// precisely so the connection survives the instant its active set is lost,
	// with ZERO promote latency — a parked leg keeps its rules installed and its
	// transport alive, so it can carry a packet immediately. This is what makes
	// the warm reserve a real "switch in at a moment's notice" pool instead of
	// something that only helps on the next 20s rotation tick. The leg-death
	// trigger + rotation tick restore a proper active set right after; this just
	// guarantees no gap. legSelectableIgnoringStandby is legReadyAt WITHOUT the
	// standby exclusion (a parked leg that was active is ready), so it never
	// picks a leg the peer has not confirmed.
	for i := uint32(0); i < n; i++ {
		idx := int((start + i) % n) //nolint:gosec
		tp := tps[idx]
		if tp != nil && !tp.IsClosed() && m.legSelectableIgnoringStandby(idx) {
			return tp, fwd[idx], idx, nil
		}
	}
	return nil, nil, -1, ErrNoSuitableTransport
}

// selectFastestTransport picks the live, ready, non-standby leg with the LOWEST
// measured latency — the pick for the RETRANSMIT path, independent of the
// group's configured distribution mode.
//
// A retransmitted segment is one the receiver's reorder buffer is head-of-line
// blocked on: every later segment that already arrived on a fast leg is being
// withheld until this gap fills. Healing that gap on the FASTEST leg (rather
// than the normal spray/weight pick, which might re-send it down the very slow
// leg that stalled it) advances the delivery window in one fast RTT instead of
// waiting out the reorder timeout. This is what keeps a slow leg from dragging
// the whole stream: it still carries its share of new data, but its stragglers
// are rescued on a fast path. Falls back to the first ready leg when no leg has
// a latency measurement yet.
func (m *routeMux) selectFastestTransport(tps []*transport.ManagedTransport, fwd []routing.Rule) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}
	bestIdx, firstReady := -1, -1
	bestLat := -1.0
	for idx, tp := range tps {
		if tp == nil || tp.IsClosed() || !m.legReadyAt(idx) {
			continue
		}
		if firstReady < 0 {
			firstReady = idx
		}
		lat := tp.GetLatency()
		if lat <= 0 {
			continue // unknown latency — only a last resort
		}
		if bestLat < 0 || lat < bestLat {
			bestLat, bestIdx = lat, idx
		}
	}
	if bestIdx < 0 {
		bestIdx = firstReady
	}
	if bestIdx < 0 {
		return nil, nil, -1, ErrNoSuitableTransport
	}
	return tps[bestIdx], fwd[bestIdx], bestIdx, nil
}

// growLegs extends the per-leg counter slice to cover at least n
// legs. Called when transports are appended to the rg (initial setup
// + AppendRoute). Idempotent — extending past the current size is a
// no-op for legs that already exist.
func (m *routeMux) growLegs(n int) {
	m.legMu.Lock()
	for len(m.legs) < n {
		m.legs = append(m.legs, &legCounters{})
	}
	for len(m.ready) < n {
		// The primary leg (index 0) is ready immediately; aux legs start
		// not-ready and are marked ready on the first inbound packet.
		m.ready = append(m.ready, len(m.ready) == 0)
	}
	for len(m.standby) < n {
		// The primary leg (index 0, the first append) is always active. Aux
		// legs enter warm standby when standbyNewLegs is set (a promoting
		// rotation engine is wired), so the engine promotes them one per tick
		// as its goodput signal warrants instead of all going hot at once.
		m.standby = append(m.standby, m.standbyNewLegs && len(m.standby) > 0)
	}
	m.legMu.Unlock()
}

// SetStandbyNewLegs controls whether newly-grown aux legs enter warm standby on
// add (see the standbyNewLegs field). Called by the route group when a promoting
// rotation engine is wired, before aux legs are appended. Idempotent.
func (m *routeMux) SetStandbyNewLegs(v bool) {
	m.legMu.Lock()
	m.standbyNewLegs = v
	m.legMu.Unlock()
}

// removeLegs drops the given ORIGINAL leg indices from legs[] and ready[] so
// they stay aligned with the rg's compacted tps[]/fwd[]/rvs[] after a leg is
// removed (RemoveMuxRouteByTransport / pruneDeadTransports). It rebuilds both
// slices skipping the dropped indices (order-independent), then re-asserts the
// leg-0-always-ready invariant — leg 0 may have been promoted from an aux when
// a primary transport is pruned. Without this lockstep compaction the arrays
// desync from tps[]: readiness and per-leg accounting attach to the wrong leg,
// which can flip a live leg to not-ready (the mux>=2 hang — see the ready[]
// note above) or mis-attribute bytes after an index is reused. The counterpart
// to growLegs.
func (m *routeMux) removeLegs(indices ...int) {
	if len(indices) == 0 {
		return
	}
	drop := make(map[int]bool, len(indices))
	for _, i := range indices {
		drop[i] = true
	}
	m.legMu.Lock()
	if len(m.legs) > 0 {
		kept := make([]*legCounters, 0, len(m.legs))
		for i, c := range m.legs {
			if !drop[i] {
				kept = append(kept, c)
			}
		}
		m.legs = kept
	}
	if len(m.ready) > 0 {
		kept := make([]bool, 0, len(m.ready))
		for i, r := range m.ready {
			if !drop[i] {
				kept = append(kept, r)
			}
		}
		m.ready = kept
		if len(m.ready) > 0 {
			m.ready[0] = true // the (possibly newly-promoted) primary is always ready
		}
	}
	if len(m.standby) > 0 {
		kept := make([]bool, 0, len(m.standby))
		for i, s := range m.standby {
			if !drop[i] {
				kept = append(kept, s)
			}
		}
		m.standby = kept
		if len(m.standby) > 0 {
			m.standby[0] = false // the primary leg is never standby
		}
	}
	m.legMu.Unlock()
}

// markLegReady records that leg idx has carried inbound traffic, so it
// is now safe to select for sending. Idempotent and bounds-checked.
func (m *routeMux) markLegReady(idx int) {
	if idx < 0 {
		return
	}
	m.legMu.Lock()
	if idx < len(m.ready) {
		m.ready[idx] = true
	}
	m.legMu.Unlock()
}

// legReadyAt reports whether leg idx may be selected for sending.
// Out-of-range indices and the never-grown case report not-ready, except
// the primary leg (0) which is always ready so a group with no readiness
// info still sends on its primary route.
func (m *routeMux) legReadyAt(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	// A warm-standby leg is never selected for sending, regardless of
	// readiness — its rules stay installed but it carries no forward traffic.
	if idx < len(m.standby) && m.standby[idx] {
		return false
	}
	if idx >= len(m.ready) {
		return idx == 0
	}
	return m.ready[idx]
}

// legSelectableIgnoringStandby reports whether leg idx may carry a packet as an
// EMERGENCY FAILOVER target — the same readiness gate as legReadyAt but WITHOUT
// the warm-standby exclusion. A parked leg keeps its rules installed and its
// transport alive, and was ready (peer-confirmed) before it was parked, so it
// can carry traffic the instant no active leg is available. selectTransport uses
// this only as a last resort, after every active leg has been found dead/not-
// ready, so the connection never dies while ANY leg in the group is alive.
func (m *routeMux) legSelectableIgnoringStandby(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.ready) {
		return idx == 0
	}
	return m.ready[idx]
}

// setLegStandby marks (or clears) leg idx as a warm standby: kept alive but
// not selected for sending. Bounds-checked; the primary leg (0) cannot be put
// on standby (a group must always have a selectable send leg). Clearing the
// flag PROMOTES the leg back to active instantly, with no route setup.
func (m *routeMux) setLegStandby(idx int, standby bool) {
	if idx <= 0 {
		return // leg 0 is never standby
	}
	m.legMu.Lock()
	if idx < len(m.standby) {
		m.standby[idx] = standby
	}
	m.legMu.Unlock()
}

// isLegStandby reports whether leg idx is a warm standby. Bounds-checked.
func (m *routeMux) isLegStandby(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	return idx < len(m.standby) && m.standby[idx]
}

// recordSent atomically increments the sent-bytes/packets counters
// for leg idx. Bounds-checked; out-of-range indices are silently
// dropped (defensive: a leg can be removed between selectTransport
// and the actual write returning).
func (m *routeMux) recordSent(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].sentBytes, n)
		atomic.AddUint64(&m.legs[idx].sentPackets, 1)
	}
	m.legMu.RUnlock()
}

// recordRecv atomically increments the recv-bytes/packets counters
// for leg idx. Same bounds-check semantics as recordSent.
func (m *routeMux) recordRecv(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].recvBytes, n)
		atomic.AddUint64(&m.legs[idx].recvPackets, 1)
	}
	m.legMu.RUnlock()
}

// recordRetransmit atomically increments the retransmit counter for leg
// idx (the leg that carried a SACK retransmit). The retransmitted bytes
// are still recorded via recordSent; this is the separate loss signal.
func (m *routeMux) recordRetransmit(idx int) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].retransmits, 1)
	}
	m.legMu.RUnlock()
}

// retransmitsAt returns leg idx's cumulative retransmit count (0 if out of
// range), for snapshotLegs / LegInfo without a full Snapshot allocation.
func (m *routeMux) retransmitsAt(idx int) uint64 {
	if idx < 0 {
		return 0
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx < len(m.legs) {
		return atomic.LoadUint64(&m.legs[idx].retransmits)
	}
	return 0
}

// snapshotLegs returns a stable copy of the current per-leg counters and, as a
// side effect, samples each leg's goodput RATE (bytes/sec EWMA) over the window
// since the previous snapshot. The byte/packet counters are point-in-time
// atomic loads; the rate is maintained under legMu (a write lock) so concurrent
// observers don't corrupt the per-leg sample state. Called at telemetry/UI
// cadence (the status page's ~1s push, CLI mux-info), never on the data path.
func (m *routeMux) snapshotLegs() []LegStats {
	now := time.Now().UnixNano()
	m.legMu.Lock()
	out := make([]LegStats, len(m.legs))
	for i, c := range m.legs {
		sent := atomic.LoadUint64(&c.sentBytes)
		recv := atomic.LoadUint64(&c.recvBytes)
		m.sampleGoodput(c, sent, recv, now)
		out[i] = LegStats{
			Index:          i,
			SentBytes:      sent,
			SentPackets:    atomic.LoadUint64(&c.sentPackets),
			RecvBytes:      recv,
			RecvPackets:    atomic.LoadUint64(&c.recvPackets),
			Retransmits:    atomic.LoadUint64(&c.retransmits),
			GoodputUpBps:   c.goodputUpBps,
			GoodputDownBps: c.goodputDownBps,
			GoodputBps:     c.goodputUpBps + c.goodputDownBps,
		}
	}
	m.legMu.Unlock()
	return out
}

// sampleGoodput updates leg c's per-direction goodput EWMAs from the sent and
// recv byte counters observed at wall-clock now (UnixNano). Caller holds legMu
// for writing. The first observation only seeds the baseline (no rate emitted).
// To keep the metric stable when several observers interleave, samples closer
// together than goodputMinSampleNano are skipped and the stored rates are left
// unchanged.
func (m *routeMux) sampleGoodput(c *legCounters, sent, recv uint64, now int64) {
	if c.lastRateNano == 0 {
		c.lastRateSentBytes = sent
		c.lastRateRecvBytes = recv
		c.lastRateNano = now
		return
	}
	elapsed := now - c.lastRateNano
	if elapsed < goodputMinSampleNano {
		return
	}
	secs := float64(elapsed) / float64(time.Second)
	c.goodputUpBps = ewmaRate(c.goodputUpBps, byteDelta(sent, c.lastRateSentBytes), secs)
	c.goodputDownBps = ewmaRate(c.goodputDownBps, byteDelta(recv, c.lastRateRecvBytes), secs)
	c.lastRateSentBytes = sent
	c.lastRateRecvBytes = recv
	c.lastRateNano = now
}

// byteDelta is cur-prev, clamped at 0 so a counter reset (route rebuild) yields
// no negative rate.
func byteDelta(cur, prev uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return 0
}

// ewmaRate folds a byte delta over secs seconds into the running bytes/sec EWMA
// (goodputEWMAAlpha weights the newest sample). A zero prior seeds directly.
func ewmaRate(prev float64, delta uint64, secs float64) float64 {
	sample := float64(delta) / secs
	if prev == 0 {
		return sample
	}
	return goodputEWMAAlpha*sample + (1-goodputEWMAAlpha)*prev
}

// wrapPayload creates a sequenced data packet and optionally stores it for retransmission.
// Returns the packet and the sequence number used.
func (m *routeMux) wrapPayload(routeID routing.RouteID, data []byte) (routing.Packet, uint32, error) {
	seq := atomic.AddUint32(&m.writeSeq, 1) - 1
	// Per-frame AEAD: seal the app payload under seq as the nonce. The sealed
	// bytes are what go on the wire AND into the retx buffer, so a SACK
	// retransmit resends the identical sealed frame (same seq ⇒ same nonce ⇒
	// same ciphertext), and the receiver opens it independently, out of order.
	if m.seal != nil {
		data = m.seal(seq, data)
	}
	packet, err := routing.MakeSequencedDataPacket(routeID, seq, data)
	if err != nil {
		return nil, 0, err
	}

	// Store for retransmission before sending
	if m.sackEnabled && m.retxBuf != nil {
		m.retxBuf.Store(seq, data) //nolint:errcheck
	}

	return packet, seq, nil
}

// deliverData inserts a received sequenced packet into the reorder buffer
// and returns any payloads that are now deliverable in order.
// Also tracks the sequence for SACK generation.
func (m *routeMux) deliverData(seq uint32, data []byte) (delivered [][]byte, gapDetected bool) {
	// Per-frame AEAD: open the frame under its sequence-nonce before it enters
	// the reorder buffer. A frame that fails to open (tamper, or a stale
	// duplicate whose seq the peer reused after a rekey) is dropped, exactly as
	// a corrupt packet would be — never delivered. SACK/reorder then treat it as
	// not-yet-received and it is retransmitted if genuinely missing.
	if m.open != nil {
		pt, err := m.open(seq, data)
		if err != nil {
			if m.logger != nil {
				m.logger.WithError(err).Tracef("per-frame open failed for seq %d; dropping", seq)
			}
			return nil, false
		}
		data = pt
	}

	// Track for SACK generation
	if m.sackEnabled && m.sackTracker != nil {
		gapDetected = m.sackTracker.RecordReceived(seq)
	}

	delivered = m.reorderBuf.Insert(seq, data)

	// Sync SACK tracker with reorder buffer delivery state
	if m.sackEnabled && m.sackTracker != nil {
		m.sackTracker.AdvanceContiguous(m.reorderBuf.NextSeq())
	}

	return delivered, gapDetected
}

// gapAge exposes the reorder buffer's current frontier-gap age (0 if the stream
// is contiguous). Used by the route group's fast data-progress prune.
func (m *routeMux) gapAge() time.Duration {
	if m.reorderBuf == nil {
		return 0
	}
	return m.reorderBuf.GapAge()
}

// reorderPending reports how many packets are currently buffered out-of-order
// on the receive side (0 when the stream is contiguous). A climbing value while
// a gap stays open is the head-of-line-blocking signal for a stalled leg.
func (m *routeMux) reorderPending() int {
	if m.reorderBuf == nil {
		return 0
	}
	return m.reorderBuf.Pending()
}

// writeSeqValue returns the count of DATA frames this mux has emitted (the next
// outgoing sequence number). A cheap aggregate outbound-progress counter.
func (m *routeMux) writeSeqValue() uint32 {
	return atomic.LoadUint32(&m.writeSeq)
}

// distributionMode returns the selector's current weight mode (how packets are
// spread across the legs). WeightModeAuto when the selector is absent.
func (m *routeMux) distributionMode() WeightMode {
	if m.tpSelector == nil {
		return WeightModeAuto
	}
	return m.tpSelector.Mode()
}

// sackMinInterval is the minimum spacing between receiver-side SACKs. It is
// well under retxMinAge so a genuine loss is still signaled several times
// before the sender's retransmit timer fires, while collapsing the flood of
// per-packet SACKs that latency-skew reordering would otherwise produce.
const sackMinInterval = 25 * time.Millisecond

// shouldSendSACK reports whether enough time has elapsed since the last SACK to
// send another, rate-limiting SACK feedback under heavy cross-leg reordering.
// Concurrency-safe: only the goroutine that wins the CAS returns true.
func (m *routeMux) shouldSendSACK() bool {
	now := time.Now().UnixNano()
	prev := atomic.LoadInt64(&m.lastSACKNano)
	if now-prev < int64(sackMinInterval) {
		return false
	}
	return atomic.CompareAndSwapInt64(&m.lastSACKNano, prev, now)
}

// generateSACK returns the current SACK state for sending to the peer:
// the last contiguous sequence plus a full-window received bitmap.
func (m *routeMux) generateSACK() (lastContig uint32, words []uint64) {
	if !m.sackEnabled || m.sackTracker == nil {
		return 0, nil
	}
	return m.sackTracker.GenerateSACK()
}

// processSACK processes a received SACK and returns sequences that need retransmission.
func (m *routeMux) processSACK(lastContig uint32, words []uint64) []uint32 {
	if !m.sackEnabled || m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.ProcessSACK(lastContig, words)
}

// getRetxPayload retrieves a stored payload for retransmission.
func (m *routeMux) getRetxPayload(seq uint32) []byte {
	if m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.Get(seq)
}

// heldRetxSeqs returns every sequence currently held unACKed in the sender's
// retx buffer, ascending. Nil when SACK/retx is not in play. Used by the
// demote-time forced retx flush to re-send a parked leg's in-flight range onto
// an active leg (see RouteGroup.rotationServiceFn).
func (m *routeMux) heldRetxSeqs() []uint32 {
	if !m.sackEnabled || m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.Seqs()
}

// rebuildWeights updates transport selection weights based on current latency.
func (m *routeMux) rebuildWeights(tps []*transport.ManagedTransport) {
	if m.tpSelector == nil {
		return
	}
	// Capacity mode: feed the selector each leg's throughput since
	// the last rebuild (bytes sent+recv delta) so it can weight the
	// schedule toward the legs actually moving data. Computed here
	// (not in the selector) because the mux owns the per-leg byte
	// counters. The delta resets each rebuild, so the weights track
	// RECENT throughput, not lifetime totals (which would entrench
	// whichever leg carried first).
	if m.tpSelector.Mode() == WeightModeCapacity {
		m.legMu.Lock()
		weights := make([]float64, len(m.legs))
		var maxW float64
		for i, lc := range m.legs {
			if lc == nil {
				continue
			}
			total := atomic.LoadUint64(&lc.sentBytes) + atomic.LoadUint64(&lc.recvBytes)
			delta := total - lc.lastTotalBytes
			lc.lastTotalBytes = total
			// A warm-standby leg carries no send traffic — it must get zero
			// weight so the scheduler never steers a packet onto a parked leg
			// (which the receiver isn't expecting on that route and would stall
			// the reorder frontier on). Keep sampling its byte counter above so a
			// later promotion starts from a fresh delta, not a stale backlog.
			if i < len(m.standby) && m.standby[i] {
				weights[i] = 0
				continue
			}
			weights[i] = float64(delta)
			if weights[i] > maxW {
				maxW = weights[i]
			}
		}
		// Cold-leg floor (the weighted-RAMP): a just-promoted active leg has moved
		// ~no bytes yet, so its raw delta is ~0 — under pure capacity weighting it
		// would get ~no traffic and thus never accumulate the goodput it needs to
		// earn a real share (a starvation deadlock). Give every active, non-standby
		// leg a floor share = capacityColdFloorFrac of the fastest active leg, so a
		// fresh leg carries a THIN trickle, measures its goodput, and ramps up as
		// its delta grows — while a genuinely slow leg stays near the floor and can
		// never open a big reorder gap. Skipped when the whole group is idle
		// (maxW == 0) so an idle mux doesn't manufacture phantom weight.
		if maxW > 0 {
			floor := maxW * capacityColdFloorFrac
			for i, lc := range m.legs {
				if lc == nil || (i < len(m.standby) && m.standby[i]) {
					continue
				}
				if weights[i] < floor {
					weights[i] = floor
				}
			}
		}
		m.legMu.Unlock()
		m.tpSelector.SetCapacityWeights(weights)
	}
	// ECF mode: build the per-leg {rate, RTT, jitter, ready, BDP} snapshot the
	// predictive scheduler reasons over. Rate is the sent-byte delta over the
	// refresh window (computed here, not from snapshotLegs, so it works even
	// when nothing is observing the telemetry page). RTT is the leg's first-hop
	// transport latency (tp.GetLatency(), ms) — the end-to-end route latency
	// would be more accurate but is not reachable from the mux; noted as a
	// follow-up. Jitter is an EWMA of |RTT-mean|, the ECF sigma margin.
	if m.tpSelector.Mode() == WeightModeECF {
		m.legMu.Lock()
		now := time.Now().UnixNano()
		var elapsed float64
		if m.ecfLastRebuildNano != 0 {
			elapsed = float64(now-m.ecfLastRebuildNano) / float64(time.Second)
		}
		states := make([]ecfLegState, len(m.legs))
		for i, lc := range m.legs {
			if lc == nil {
				continue
			}
			// Send rate over the refresh window (bytes/sec).
			sent := atomic.LoadUint64(&lc.sentBytes)
			var rate float64
			if elapsed > 0 {
				rate = float64(byteDelta(sent, lc.ecfLastSentBytes)) / elapsed
			}
			lc.ecfLastSentBytes = sent
			// RTT EWMA + jitter (sigma) EWMA.
			var rttMs float64
			if i < len(tps) && tps[i] != nil {
				rttMs = tps[i].GetLatency()
			}
			if rttMs > 0 {
				if lc.ecfRttMs == 0 {
					lc.ecfRttMs = rttMs
				} else {
					dev := rttMs - lc.ecfRttMs
					if dev < 0 {
						dev = -dev
					}
					lc.ecfJitterMs = ecfJitterAlpha*dev + (1-ecfJitterAlpha)*lc.ecfJitterMs
					lc.ecfRttMs = ecfRttAlpha*rttMs + (1-ecfRttAlpha)*lc.ecfRttMs
				}
			}
			ready := true
			if i < len(m.standby) && m.standby[i] {
				ready = false
			}
			if i < len(m.ready) && !m.ready[i] {
				ready = false
			}
			states[i] = ecfLegState{
				rttMs:     lc.ecfRttMs,
				jitterMs:  lc.ecfJitterMs,
				rateBps:   rate,
				cwndBytes: rate * lc.ecfRttMs / 1000.0,
				ready:     ready,
			}
		}
		m.ecfLastRebuildNano = now
		m.legMu.Unlock()
		m.tpSelector.SetECFState(states)
	}
	m.tpSelector.Rebuild(tps)
}
