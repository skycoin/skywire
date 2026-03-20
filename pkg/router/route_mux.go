// Package router pkg/router/route_mux.go
package router

import (
	"sync/atomic"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

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
// NOTE: not thread-safe, caller must hold the RouteGroup mu.
func (m *routeMux) selectTransport(tps []*transport.ManagedTransport, fwd []routing.Rule) (*transport.ManagedTransport, routing.Rule, error) {
	if len(tps) == 0 {
		return nil, nil, ErrNoTransports
	}
	if len(fwd) == 0 {
		return nil, nil, ErrNoRules
	}

	// Use weighted selector if available
	if m.tpSelector != nil && m.tpSelector.Len() > 0 {
		idx := m.tpSelector.Select()
		if idx < len(tps) {
			tp := tps[idx]
			if tp != nil && !tp.IsClosed() {
				return tp, fwd[idx], nil
			}
		}
	}

	// Fallback: round-robin with skip-dead
	n := uint32(len(tps)) //nolint:gosec
	start := atomic.AddUint32(&m.tpIndex, 1) - 1
	for i := uint32(0); i < n; i++ {
		idx := (start + i) % n
		tp := tps[idx]
		if tp != nil && !tp.IsClosed() {
			return tp, fwd[idx], nil
		}
	}
	return nil, nil, ErrNoSuitableTransport
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
