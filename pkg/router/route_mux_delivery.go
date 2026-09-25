// Package router pkg/router/route_mux_delivery.go c2-net-routing
package router

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
)

// perFrameSealOverhead is the AEAD tag the per-frame seal appends to every mux
// frame. Both noise cipher suites in use (AES-GCM, ChaCha20-Poly1305) carry a
// 16-byte tag.
const perFrameSealOverhead = 16

// frameOverhead is what a sequenced data frame spends on top of the application
// payload: the sequence number, plus the AEAD tag once per-frame noise is wired.
func (m *routeMux) frameOverhead() int {
	overhead := routing.SeqSize
	if m.seal != nil {
		overhead += perFrameSealOverhead
	}
	if m.deliveryCRC {
		overhead += routing.DeliveryCRCSize
	}
	return overhead
}

// wrapPayload creates a sequenced data packet and optionally stores it for
// retransmission, tagged with the TRANSPORT UUID of the leg it is about to be
// sent on (uuid.Nil = unknown) so the demote-time flush can target only the
// stranded leg's sequences. The tag is the transport identity, not the leg
// index — indices shift on slice compaction.
// Returns the packet and the sequence number used.
func (m *routeMux) wrapPayload(routeID routing.RouteID, data []byte, tpID uuid.UUID) (routing.Packet, uint32, error) {
	// Reject an oversized frame BEFORE taking a sequence number. A seq consumed by
	// a frame that never goes out is a permanent hole: the receiver's no-skip
	// reorder buffer waits on it forever. RouteGroup.Write segments to
	// maxWritePayload so this is a guard, not a path.
	if len(data)+m.frameOverhead() > math.MaxUint16 {
		return nil, 0, routing.ErrPayloadTooBig
	}
	seq := atomic.AddUint32(&m.writeSeq, 1) - 1
	// Delivery check: stamp the CRC32C of (seq ‖ payload) BEFORE the seal, so the
	// trailer rides inside the ciphertext and a retransmit or FEC reconstruction
	// of this seq reproduces identical bytes. The receiver verifies it when this
	// frame is DELIVERED in order (see deliverData / delivery_crc.go).
	if m.deliveryCRC {
		data = stampDeliveryCRC(seq, data)
	}
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
		m.retxBuf.Store(seq, data, tpID) //nolint:errcheck
	}

	// FEC: feed the on-wire (post-seal) payload to the striper. A completed block
	// queues R repair frames for the send loop (RouteGroup.write) to schedule on a
	// fast leg. Inert unless CapFEC was negotiated.
	m.fecOnSend(seq, data)

	// Stamp the TLP idle timer: new data just went out, so the tail-loss probe
	// clock restarts. A probe only fires once this stays quiet for a PTO.
	m.lastSendNano.Store(time.Now().UnixNano())

	return packet, seq, nil
}

// deliverData inserts a received sequenced packet into the reorder buffer
// and returns any payloads that are now deliverable in order.
// Also tracks the sequence for SACK generation.
//
// leg is the arrival leg index (the transport this frame came in on). A seq seen
// here for the FIRST time credits that leg's payloadBytes with the frame's app
// payload — so per-leg payloadBytes attributes the transfer's unique payload to
// the legs that actually carried it, retransmits/duplicates excluded. Pass a
// negative leg to skip attribution (callers/tests without a leg context).
func (m *routeMux) deliverData(leg int, seq uint32, data []byte) (delivered [][]byte, gapDetected bool) {
	// FEC: record the on-wire (pre-open) payload so a sibling in this block can be
	// reconstructed if it is late/lost on a slow leg. Inert unless CapFEC
	// negotiated. Must run BEFORE open — reconstruction reproduces the on-wire
	// frame, which is then opened via the same path below.
	m.fecOnRecvData(seq, data)

	// Per-frame AEAD: open the frame under its sequence-nonce before it enters
	// the reorder buffer. A frame that fails to open (tamper, or a stale
	// duplicate whose seq the peer reused after a rekey) is dropped, exactly as
	// a corrupt packet would be — never delivered. SACK/reorder then treat it as
	// not-yet-received and it is retransmitted if genuinely missing.
	if m.open != nil {
		pt, err := m.open(seq, data)
		if err != nil {
			// Count the AEAD failure router-wide. DatagramRouteGroup has had its
			// own tally since per-frame noise landed; this is the same event on
			// the STREAM route group path, so `visor state --select mux` reports
			// per-frame authentication failures for every kind of group.
			noteAEADFailure()
			if m.logger != nil {
				m.logger.WithError(err).Tracef("per-frame open failed for seq %d; dropping", seq)
			}
			return nil, false
		}
		data = pt
	}

	// Attribute UNIQUE payload to the arrival leg: credit this leg only the FIRST
	// time a seq arrives, so a retransmit of a seq already seen on another leg is
	// not double-counted and the per-leg payloadBytes sum equals the transfer
	// size. Already-delivered is seq < reorderBuf.NextSeq (no seq-0 ambiguity);
	// already-buffered-out-of-order is the received set. Checked BEFORE
	// RecordReceived/Insert record this seq.
	if leg >= 0 {
		isNew := true
		if m.reorderBuf != nil && seq < m.reorderBuf.NextSeq() {
			isNew = false // already delivered
		} else if m.sackTracker != nil && m.sackTracker.alreadyBuffered(seq) {
			isNew = false // already buffered out of order
		}
		if isNew {
			m.recordPayload(leg, uint64(len(data)))
		} else {
			m.recordDup(leg, uint64(len(data)))
		}
	}

	// Insert FIRST, then track for SACK generation — and only if the packet was
	// actually buffered. At maxGap the reorder buffer DROPS the packet; recording
	// it as received (the old order) made the next SACK set its bit, the sender
	// purged it from the retransmit buffer, and the no-skip frontier then wedged
	// forever on a sequence nobody could resend. A dropped seq stays unrecorded,
	// so the SACK reports it missing and the sender retransmits it.
	// The reorder buffer only ever releases a CONTIGUOUS run from its frontier,
	// so the first frame it delivers below carries the frontier sequence read
	// here and frame i carries startSeq+i — the binding the delivery CRC checks.
	startSeq := m.reorderBuf.NextSeq()

	var dropped bool
	delivered, dropped = m.reorderBuf.InsertOrDrop(seq, data)
	if dropped {
		m.reorderDrops.Add(1)
		if m.logger != nil {
			m.logger.Debugf("reorder buffer full: dropped seq %d (not SACKed, sender will retransmit)", seq)
		}
	}

	if m.sackEnabled && m.sackTracker != nil {
		if dropped {
			// The frontier is gap-blocked and this arrival was discarded: still ask
			// for a SACK so the sender resends the sequence the frontier waits on.
			gapDetected = true
		} else {
			gapDetected = m.sackTracker.RecordReceived(seq)
		}
	}

	// Sync SACK tracker with reorder buffer delivery state
	if m.sackEnabled && m.sackTracker != nil {
		m.sackTracker.AdvanceContiguous(m.reorderBuf.NextSeq())
	}

	// FEC: if the frontier is now gap-blocked but this frame completed a block's
	// K-of-(K+R) quorum, reconstruct the stuck frontier frame(s) and append them
	// to the delivered run so they reach the app in order — no wait on the slow
	// leg. Inert unless CapFEC negotiated.
	if m.fecEnabled {
		if extra := m.fecTryAdvance(); len(extra) > 0 {
			delivered = append(delivered, extra...)
		}
	}

	// Delivery check, LAST: everything above may still add to the in-order run,
	// and the point of this check is the bytes that actually reach the app.
	if m.deliveryCRC && len(delivered) > 0 {
		delivered = m.verifyDelivered(startSeq, delivered)
	}

	return delivered, gapDetected
}

// verifyDelivered checks and strips the delivery CRC of an in-order run whose
// first frame carries startSeq, returning only the frames that matched. A
// mismatched frame is DROPPED — corrupted or misordered bytes are never handed
// to the app — counted router-wide, and logged once per group at warn.
//
// The run is filtered in place: out only ever trails the read index.
func (m *routeMux) verifyDelivered(startSeq uint32, delivered [][]byte) [][]byte {
	out := delivered[:0]
	for i, frame := range delivered {
		seq := startSeq + uint32(i)
		payload, ok := checkDeliveryCRC(seq, frame)
		if !ok {
			noteDeliveryCRCFailure()
			port := m.groupPort
			m.crcWarnOnce.Do(func() {
				if m.logger != nil {
					m.logger.Warnf("delivery CRC mismatch on group port %d at seq %d: dropping the frame "+
						"(further mismatches on this group are counted, not logged)", port, seq)
				}
			})
			continue
		}
		out = append(out, payload)
	}
	return out
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

// reorderNextSeq returns the sequence number the receive-side reorder buffer is
// waiting on (the frontier). When a gap is stuck this is the missing seq whose
// leg has stalled — the key datum for diagnosing a reorder wedge.
func (m *routeMux) reorderNextSeq() uint32 {
	if m.reorderBuf == nil {
		return 0
	}
	return m.reorderBuf.NextSeq()
}

// writeSeqValue returns the count of DATA frames this mux has emitted (the next
// outgoing sequence number). A cheap aggregate outbound-progress counter.
func (m *routeMux) writeSeqValue() uint32 {
	return atomic.LoadUint32(&m.writeSeq)
}
