// Package router pkg/router/route_mux.go c2-net-routing
package router

import (
	"sync"
	"sync/atomic"

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
}

type legCounters struct {
	sentBytes   uint64 // atomic
	sentPackets uint64 // atomic
	recvBytes   uint64 // atomic
	recvPackets uint64 // atomic
	retransmits uint64 // atomic
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

	// Incoming packet reordering
	reorderBuf *reorderBuffer

	// Adaptive transport weighting based on latency
	tpSelector *transportSelector

	// SACK retransmission
	sackEnabled bool         // true when both peers advertised CapSACK
	sackTracker *sackTracker // receiver: tracks received seqs for SACK generation
	retxBuf     *retxBuffer  // sender: holds unACKed packets for retransmission

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
}

// newRouteMux creates a new routeMux instance with all sub-components initialized.
func newRouteMux(logger *logging.Logger, sackEnabled bool) *routeMux {
	m := &routeMux{
		logger:      logger,
		reorderBuf:  newReorderBuffer(64),
		tpSelector:  newTransportSelector(),
		sackEnabled: sackEnabled,
		sackTracker: newSACKTracker(),
		retxBuf:     newRetxBuffer(128),
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
			WeightModeDSCPPriority:
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
	return nil, nil, -1, ErrNoSuitableTransport
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
	if idx >= len(m.ready) {
		return idx == 0
	}
	return m.ready[idx]
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

// snapshotLegs returns a stable copy of the current per-leg counters.
// Atomic loads, no locking against in-flight increments — the
// snapshot is point-in-time and the underlying counters keep moving.
func (m *routeMux) snapshotLegs() []LegStats {
	m.legMu.RLock()
	out := make([]LegStats, len(m.legs))
	for i, c := range m.legs {
		out[i] = LegStats{
			Index:       i,
			SentBytes:   atomic.LoadUint64(&c.sentBytes),
			SentPackets: atomic.LoadUint64(&c.sentPackets),
			RecvBytes:   atomic.LoadUint64(&c.recvBytes),
			RecvPackets: atomic.LoadUint64(&c.recvPackets),
			Retransmits: atomic.LoadUint64(&c.retransmits),
		}
	}
	m.legMu.RUnlock()
	return out
}

// wrapPayload creates a sequenced data packet and optionally stores it for retransmission.
// Returns the packet and the sequence number used.
func (m *routeMux) wrapPayload(routeID routing.RouteID, data []byte) (routing.Packet, uint32, error) {
	seq := atomic.AddUint32(&m.writeSeq, 1) - 1
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

// generateSACK returns the current SACK state for sending to the peer.
func (m *routeMux) generateSACK() (lastContig uint32, bitmap uint64) {
	if !m.sackEnabled || m.sackTracker == nil {
		return 0, 0
	}
	return m.sackTracker.GenerateSACK()
}

// processSACK processes a received SACK and returns sequences that need retransmission.
func (m *routeMux) processSACK(lastContig uint32, bitmap uint64) []uint32 {
	if !m.sackEnabled || m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.ProcessSACK(lastContig, bitmap)
}

// getRetxPayload retrieves a stored payload for retransmission.
func (m *routeMux) getRetxPayload(seq uint32) []byte {
	if m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.Get(seq)
}

// rebuildWeights updates transport selection weights based on current latency.
func (m *routeMux) rebuildWeights(tps []*transport.ManagedTransport) {
	if m.tpSelector != nil {
		m.tpSelector.Rebuild(tps)
	}
}
