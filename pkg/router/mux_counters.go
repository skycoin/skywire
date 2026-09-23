// Package router mux_counters.go: cumulative, process-lifetime tallies of
// mux state transitions whose only other record is the bounded MuxEvent
// ring (mux_events.go), which drops old entries under sustained churn.
// Observability only — nothing here is read by control flow, and adding a
// count never changes what the router decides.
package router

import "sync/atomic"

// MuxCounters is a snapshot of the whole-router mux counters, surfaced via
// `visor state --select mux` (StateSnapshot.MuxCounters) and `proxy mux
// info`.
type MuxCounters struct {
	// TunnelPromotions counts standby-tunnel promotions to active (a pool
	// tunnel flip) — every MuxEventTunnelPromoted recorded by noteTunnelEvent.
	TunnelPromotions uint64 `json:"tunnel_promotions"`
	// LegRehomesSent/Acked count this visor's OWN outbound leg re-homes
	// (leg_rehome.go rehomeChain): Sent when the request lands on the wire,
	// Acked when the peer's ack completes the in-place move. LegRehomesFailed
	// adds the initiator's own timeout/refusal outcomes to the acceptor's
	// refusals counted below — it does not cover a failure AFTER a peer's ack
	// (adopting the leg locally), which is rarer and separately logged as a
	// MuxEvent. LegRehomesReceived counts an incoming re-home REQUEST this
	// visor accepted or refused (leg_rehome.go acceptRehome).
	LegRehomesSent     uint64 `json:"leg_rehomes_sent"`
	LegRehomesReceived uint64 `json:"leg_rehomes_received"`
	LegRehomesAcked    uint64 `json:"leg_rehomes_acked"`
	LegRehomesFailed   uint64 `json:"leg_rehomes_failed"`
	// LegSplitsSent/Received/Acked/Failed count the REVERSE move
	// (leg_split.go): a leg handed back OUT of a group into a standalone
	// standby group of its own, instead of its transport being closed.
	// Sent/Acked/Failed are this visor's own outbound splits, Received an
	// incoming split request it accepted or refused.
	LegSplitsSent     uint64 `json:"leg_splits_sent"`
	LegSplitsReceived uint64 `json:"leg_splits_received"`
	LegSplitsAcked    uint64 `json:"leg_splits_acked"`
	LegSplitsFailed   uint64 `json:"leg_splits_failed"`
	// ForwardFanoutEngaged/Released count noteForwardFanout(on=true/false):
	// the forward direction's fan-out-under-load latch (unidir.go) turning on
	// (MuxEventForwardFanout) or off (MuxEventForwardConfined).
	ForwardFanoutEngaged  uint64 `json:"forward_fanout_engaged"`
	ForwardFanoutReleased uint64 `json:"forward_fanout_released"`
	// AEADFailures counts per-frame AEAD (CapPerFrameNoise) open failures
	// across ALL route groups — the stream RouteGroup's mux path and the
	// datagram group's, which also keeps its own per-group tally. A nonzero
	// value is tampering, a stale duplicate after a rekey, or wire corruption
	// the transport did not catch; the frame is always dropped, never delivered.
	AEADFailures uint64 `json:"aead_failures"`
	// DeliveryCRCFailures counts frames dropped at DELIVERY because their
	// CapDeliveryCRC trailer did not match the CRC32C of (seq ‖ payload) — see
	// delivery_crc.go. Unlike AEADFailures this one is the reassembly check: it
	// catches a reorder/flush defect that would otherwise only surface as an
	// application hash failure. It must be 0 on a healthy mux.
	DeliveryCRCFailures uint64 `json:"delivery_crc_failures"`
}

// muxGlobalCounters holds the live atomics backing MuxCounters.
type muxGlobalCounters struct {
	tunnelPromotions      atomic.Uint64
	legRehomesSent        atomic.Uint64
	legRehomesReceived    atomic.Uint64
	legRehomesAcked       atomic.Uint64
	legRehomesFailed      atomic.Uint64
	legSplitsSent         atomic.Uint64
	legSplitsReceived     atomic.Uint64
	legSplitsAcked        atomic.Uint64
	legSplitsFailed       atomic.Uint64
	forwardFanoutEngaged  atomic.Uint64
	forwardFanoutReleased atomic.Uint64
	aeadFailures          atomic.Uint64
	deliveryCRCFailures   atomic.Uint64
}

// noteAEADFailure records one per-frame AEAD open failure, on any kind of route
// group. Called from the drop path itself, so the count and the drop cannot
// disagree.
func noteAEADFailure() { globalMuxCounters.aeadFailures.Add(1) }

// noteDeliveryCRCFailure records one frame dropped at delivery for a CRC
// mismatch (see routeMux.verifyDelivered).
func noteDeliveryCRCFailure() { globalMuxCounters.deliveryCRCFailures.Add(1) }

// globalMuxCounters is process-wide: the events it tallies (tunnel
// promotion, leg re-home, forward fan-out) are router-wide phenomena, not
// scoped to one route group, matching how MuxEvents() already exposes a
// single whole-router ring rather than a per-group one.
var globalMuxCounters muxGlobalCounters

func (c *muxGlobalCounters) snapshot() MuxCounters {
	return MuxCounters{
		TunnelPromotions:      c.tunnelPromotions.Load(),
		LegRehomesSent:        c.legRehomesSent.Load(),
		LegRehomesReceived:    c.legRehomesReceived.Load(),
		LegRehomesAcked:       c.legRehomesAcked.Load(),
		LegRehomesFailed:      c.legRehomesFailed.Load(),
		LegSplitsSent:         c.legSplitsSent.Load(),
		LegSplitsReceived:     c.legSplitsReceived.Load(),
		LegSplitsAcked:        c.legSplitsAcked.Load(),
		LegSplitsFailed:       c.legSplitsFailed.Load(),
		ForwardFanoutEngaged:  c.forwardFanoutEngaged.Load(),
		ForwardFanoutReleased: c.forwardFanoutReleased.Load(),
		AEADFailures:          c.aeadFailures.Load(),
		DeliveryCRCFailures:   c.deliveryCRCFailures.Load(),
	}
}

// MuxCountersSnapshot reads the process-wide mux counters without a router
// instance. The counters are global (see globalMuxCounters), and the emulator
// bench asserts the integrity ones cell by cell.
func MuxCountersSnapshot() MuxCounters { return globalMuxCounters.snapshot() }

// MuxCounters implements Router.
func (r *router) MuxCounters() MuxCounters {
	return globalMuxCounters.snapshot()
}
