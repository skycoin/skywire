// Package router pkg/router/route_group_sack.go c2-net-routing
package router

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// sendSACK sends a SACK packet with the current receiver state.
func (rg *RouteGroup) sendSACK() error {
	if rg.mux == nil || !rg.mux.sackEnabled {
		return nil
	}

	rg.mu.Lock()
	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		rg.mu.Unlock()
		return nil
	}
	// The primary leg carries the SACK (control channel) unless it has gone
	// silent inbound while another leg has not — see routeMux.sackLeg.
	i := rg.mux.sackLeg(routersettings.SackLegSilence.Duration(), time.Now().UnixNano())
	if i >= len(rg.tps) || i >= len(rg.fwd) || rg.tps[i] == nil || rg.fwd[i] == nil {
		i = 0
	}
	tp := rg.tps[i]
	rule := rg.fwd[i]
	rg.mu.Unlock()

	if tp == nil {
		return nil
	}

	lastContig, words := rg.mux.generateSACK()
	// Attach a DSACK (duplicate-SACK) when the receiver saw a repeated sequence,
	// so the sender can widen its RACK reorder window (it retransmitted too
	// eagerly). Backward compatible: a peer without the field reads only the
	// bitmap. No duplicate pending → a plain SACK, unchanged from before.
	var packet routing.Packet
	if dsackSeq, ok := rg.mux.takeDSACK(); ok {
		packet = routing.MakeSACKPacketWithDSACK(rule.NextRouteID(), lastContig, words, dsackSeq)
	} else {
		packet = routing.MakeSACKPacket(rule.NextRouteID(), lastContig, words)
	}
	err := rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID())
	if err != nil {
		rg.mux.sackSendErrors.Add(1)
		return err
	}
	// Receiver-side feedback accounting: a wedged frontier with sacks_sent
	// climbing and the peer's retx_sent flat means our SACKs are not reaching
	// the sender (or are reaching it with nothing left to resend).
	rg.mux.sacksSent.Add(1)
	rg.mux.lastSACKSentNano.Store(time.Now().UnixNano())
	return nil
}

// handleSACKPacket processes a received SACK and retransmits missing packets.
func (rg *RouteGroup) handleSACKPacket(packet routing.Packet) error {
	if rg.mux == nil || !rg.mux.sackEnabled {
		return nil
	}

	lastContig := packet.SACKLastContiguousSeq()
	words := packet.SACKWords()
	dsackSeq, hasDSACK := packet.SACKDSACK()

	// Sender-side feedback accounting, recorded BEFORE any retransmit decision:
	// "did the peer's SACK even arrive" is the first fork of a wedge diagnosis,
	// and it must be answerable when the answer is "yes, but there was nothing
	// to resend" (the early return below).
	rg.mux.sacksRecv.Add(1)
	rg.mux.lastSACKRecvNano.Store(time.Now().UnixNano())

	// Normal reactive retransmit: holes overdue past the RACK threshold. Also
	// advances the ack-progress edge (resets the TLP probe budget) and adapts the
	// reorder factor from the DSACK signal (widen on a duplicate, decay otherwise).
	retxSeqs := rg.mux.onSACKReceived(lastContig, words, dsackSeq, hasDSACK)
	rg.mux.retxReqSACK.Add(uint64(len(retxSeqs)))

	// Proactive HoL retransmit (CapHOLRetx): the frontier-blocking seq (and the
	// next few contiguous holes) retransmitted NOW on the fastest leg, bypassing
	// retxMinAge, per-seq rate-limited to one nudge per fast-leg RTT. No-op (nil)
	// when HoL retx was not negotiated, so a peer without CapHOLRetx keeps the
	// reactive-only path above. Merged with retxSeqs and de-duplicated so the same
	// frontier seq is never sent twice for one SACK.
	if rg.mux.holRetxEnabled {
		rg.mu.Lock()
		fastMs := rg.mux.fastestLegLatency(rg.tps)
		legRTT := rg.mux.legLatencyByTp(rg.tps)
		rg.mu.Unlock()
		if due := rg.mux.proactiveRetxSeqs(lastContig, words, fastMs, legRTT, time.Now()); len(due) > 0 {
			rg.mux.retxReqHOL.Add(uint64(len(due)))
			retxSeqs = mergeSeqs(retxSeqs, due)
		}
	}

	if len(retxSeqs) == 0 {
		return nil
	}

	rg.logger.Debugf("SACK: retransmitting %d packets", len(retxSeqs))

	return rg.resendSeqs(retxSeqs)
}

// mergeSeqs returns the union of two seq lists, de-duplicated, so a proactive
// HoL retransmit and a reactive SACK retransmit that name the same seq in one
// SACK resend it only once. Order is not significant to resendSeqs (each seq is
// selected onto the fastest leg independently), so a simple set union suffices.
func mergeSeqs(a, b []uint32) []uint32 {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	seen := make(map[uint32]struct{}, len(a)+len(b))
	out := make([]uint32, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// resendSeqs retransmits the given held sequences on the FASTEST live,
// non-standby leg, mirroring the SACK-driven retransmit path exactly (same wire
// format via MakeSequencedDataPacket + the same per-leg sent/retransmit
// accounting). Fastest-leg selection heals a head-of-line-blocking gap on a fast
// path rather than re-sending it down the slow leg that stalled it — see
// routeMux.selectFastestTransport. Sequences no longer in the retx buffer are
// skipped (already acknowledged and purged). Returns the transport-selection
// error when no active leg is available so the caller decides whether that is
// fatal (SACK path) or a safe no-op (demote flush — leg 0 is never standby).
//
// Shared by handleSACKPacket (receiver asked for these) and the demote-time
// forced retx flush (rotationServiceFn self-issues the whole in-flight window).
func (rg *RouteGroup) resendSeqs(seqs []uint32) error {
	for _, seq := range seqs {
		data := rg.mux.getRetxPayload(seq)
		if data == nil {
			// The buffer no longer holds this sequence, so nothing we do can
			// refill the receiver's gap with it. Counted (retx_skipped_missing)
			// because a wedge where the receiver keeps naming a seq and this keeps
			// climbing is a DIFFERENT failure from one where the SACKs never
			// arrive — and from the sender the two look identical otherwise.
			rg.mux.retxSkippedMissing.Add(1)
			continue
		}

		// The leg this sequence was last sent on is the one that failed to
		// deliver it, so it is the one leg the retry must not use (see
		// routeMux.selectRetxTransport).
		avoid := rg.mux.retxLastTp(seq)

		rg.mu.Lock()
		tp, rule, leg, err := rg.nextRetxTransport(avoid)
		rg.mu.Unlock()
		if err != nil {
			rg.mux.retxSendErrors.Add(1)
			return err
		}

		retxPacket, err := routing.MakeSequencedDataPacket(rule.NextRouteID(), seq, data)
		if err != nil {
			rg.mux.retxSendErrors.Add(1)
			return err
		}

		if err := rg.writePacket(context.Background(), tp, retxPacket, rule.KeyRouteID()); err != nil {
			rg.mux.retxSendErrors.Add(1)
			rg.logger.WithError(err).Warnf("failed to retransmit seq %d", seq)
		} else if leg >= 0 {
			// Count retransmits against the leg that carried them
			// (which may differ from the original send leg — the
			// retx selector picks fresh, mirroring the data path).
			rg.mux.recordSent(leg, uint64(retxPacket.Size()))
			rg.mux.recordRetransmit(leg) // separate loss signal for leg health
			// Re-tag the sequence's last-send transport so a later demote
			// flush attributes it to where it now rides, not where it started.
			if tp != nil {
				rg.mux.retxSetTp(seq, tp.Entry.ID)
			}
			// A frame just went out (incl. a TLP probe) — restart the TLP idle
			// timer so the next probe waits a fresh PTO rather than back-to-back.
			rg.mux.lastSendNano.Store(time.Now().UnixNano())
		}
	}
	return nil
}

// scheduleDelayedAck arms the one-shot delayed-ack timer; a no-op while one
// is already outstanding, so bulk in-order traffic costs at most one extra
// SACK per delayedAckDelay. The fire path re-checks the shared SACK rate
// limit, so a gap-SACK that went out meanwhile suppresses it.
func (rg *RouteGroup) scheduleDelayedAck() {
	if !atomic.CompareAndSwapInt32(&rg.delayedAckArmed, 0, 1) {
		return
	}
	time.AfterFunc(rg.knDur(routersettings.SackDelayedAckDelay), func() {
		atomic.StoreInt32(&rg.delayedAckArmed, 0)
		if rg.isClosed() {
			return
		}
		if rg.mux != nil && rg.mux.shouldSendSACK() {
			_ = rg.sendSACK() //nolint:errcheck
		}
	})
}

// delayedAckDelay is how long after clean in-order delivery the one-shot
// delayed ack fires (see the scheduleDelayedAck call site). Long enough to
// coalesce a burst into one SACK, short against every sender timer it must
// beat (TLP's PTO, the RACK threshold).
const delayedAckDelayDefault = 100 * time.Millisecond
