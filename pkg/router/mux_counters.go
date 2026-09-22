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
	// ForwardFanoutEngaged/Released count noteForwardFanout(on=true/false):
	// the forward direction's fan-out-under-load latch (unidir.go) turning on
	// (MuxEventForwardFanout) or off (MuxEventForwardConfined).
	ForwardFanoutEngaged  uint64 `json:"forward_fanout_engaged"`
	ForwardFanoutReleased uint64 `json:"forward_fanout_released"`
}

// muxGlobalCounters holds the live atomics backing MuxCounters.
type muxGlobalCounters struct {
	tunnelPromotions      atomic.Uint64
	legRehomesSent        atomic.Uint64
	legRehomesReceived    atomic.Uint64
	legRehomesAcked       atomic.Uint64
	legRehomesFailed      atomic.Uint64
	forwardFanoutEngaged  atomic.Uint64
	forwardFanoutReleased atomic.Uint64
}

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
		ForwardFanoutEngaged:  c.forwardFanoutEngaged.Load(),
		ForwardFanoutReleased: c.forwardFanoutReleased.Load(),
	}
}

// MuxCounters implements Router.
func (r *router) MuxCounters() MuxCounters {
	return globalMuxCounters.snapshot()
}
