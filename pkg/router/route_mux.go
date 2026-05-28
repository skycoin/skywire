// Package router pkg/router/route_mux.go
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
}

type legCounters struct {
	sentBytes   uint64 // atomic
	sentPackets uint64 // atomic
	recvBytes   uint64 // atomic
	recvPackets uint64 // atomic
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
// payloadSize is the upcoming packet's payload byte count, used by
// WeightModeSizeThreshold to route large payloads to the wide-pipe
// leg. Pass 0 for handshake / control / retx packets whose size
// isn't a routing signal — the selector falls back to its normal
// schedule in that case.
//
// NOTE: not thread-safe, caller must hold the RouteGroup mu.
func (m *routeMux) selectTransport(tps []*transport.ManagedTransport, fwd []routing.Rule, payloadSize int) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}

	// SizeThreshold path: ask the selector for the size-aware
	// leg. Falls back to the regular schedule when payloadSize
	// is 0 (control packets) since the threshold can't be
	// meaningfully applied to packets whose size we don't know.
	if m.tpSelector != nil && m.tpSelector.Mode() == WeightModeSizeThreshold && payloadSize > 0 {
		idx := m.tpSelector.SelectForSize(payloadSize)
		if idx < len(tps) {
			tp := tps[idx]
			if tp != nil && !tp.IsClosed() {
				return tp, fwd[idx], idx, nil
			}
		}
	}

	// Use weighted selector if available
	if m.tpSelector != nil && m.tpSelector.Len() > 0 {
		idx := m.tpSelector.Select()
		if idx < len(tps) {
			tp := tps[idx]
			if tp != nil && !tp.IsClosed() {
				return tp, fwd[idx], idx, nil
			}
		}
	}

	// Fallback: round-robin with skip-dead
	n := uint32(len(tps)) //nolint:gosec
	start := atomic.AddUint32(&m.tpIndex, 1) - 1
	for i := uint32(0); i < n; i++ {
		idx := int((start + i) % n) //nolint:gosec
		tp := tps[idx]
		if tp != nil && !tp.IsClosed() {
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
	m.legMu.Unlock()
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
