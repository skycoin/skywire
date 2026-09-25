// Package router pkg/router/route_group_stats.go c2-net-routing
package router

import (
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// MuxStats returns a point-in-time snapshot of the rg's per-leg
// counters paired with each leg's transport identity.
func (rg *RouteGroup) MuxStats() MuxInfo {
	info := MuxInfo{Desc: rg.desc, FarEndPK: rg.farEndPK(), Events: rg.ownEvents.lastN(rg.knInt(routersettings.MuxEventsPerGroup))}
	info.AppName = rg.AppName()
	info.KnobApp = rg.knobs().App()
	rg.mu.Lock()
	if rg.mux != nil {
		info.MuxEnabled = true
		info.SACKEnabled = rg.mux.sackEnabled
		info.HOLRetxEnabled = rg.mux.holRetxEnabled
		info.Distribution = rg.mux.distributionMode().String()
		info.ReorderPending = rg.mux.reorderPending()
		info.ReorderGapAge = rg.mux.gapAge()
		info.WriteSeq = rg.mux.writeSeqValue()
		info.FECEnabled = rg.mux.fecEnabled
		info.FECRepairBytesSent = rg.mux.fecRepairBytesSent.Load()
		info.FECRepairBytesRecv = rg.mux.fecRepairBytesRecv.Load()
		info.FECReconstructs = rg.mux.fecReconstructs.Load()
		info.Directional, info.Flipped = rg.mux.dirState()
		if info.Directional {
			info.FlipPinned = flipPinString(rg.mux.flipPinMode())
		}
	}
	info.PerFrameNoise = rg.perFrameNoiseActive
	info.TunnelRole = rg.tunnelRole
	info.LegReserve = rg.legReserve
	if !rg.createdAt.IsZero() {
		info.AgeMS = float64(time.Since(rg.createdAt)) / float64(time.Millisecond)
	}
	tpsCopy := append([]*transport.ManagedTransport(nil), rg.tps...)
	rg.mu.Unlock()

	var stats []LegStats
	if rg.mux != nil {
		stats = rg.mux.snapshotLegs()
	}

	dstPK := rg.desc.DstPK()
	info.Legs = make([]MuxLeg, 0, len(tpsCopy))
	for i, tp := range tpsCopy {
		leg := MuxLeg{}
		if i < len(stats) {
			leg.LegStats = stats[i]
		} else {
			leg.Index = i
		}
		if tp != nil {
			leg.TransportID = tp.Entry.ID.String()
			leg.TpType = string(tp.Entry.Type)
			leg.RemotePK = tp.Remote().String()
			leg.Source = rg.poolLegSource(tp.Entry.ID)
			leg.CapacityPriorBps = throughputPrior(tp)
			leg.LatencyMS = tp.GetLatency()
			// TRUE end-to-end route latency (all hops), from the leg-liveness
			// pong — distinct from the first-hop transport RTT above.
			leg.RouteLatencyMS = rg.legEndToEndLatencyMs(tp.Entry.ID)
			if rg.mux != nil {
				leg.AckDelayMS = rg.mux.ackDelayMsTp(tp.Entry.ID)
				if rg.mux.tpSelector != nil {
					leg.InflightBytes, leg.WindowBytes = rg.mux.tpSelector.LegWindow(i)
				}
			}
			// Direct = this leg's first hop reaches the route group's FAR
			// endpoint itself, i.e. a 1-hop route; otherwise it is relayed
			// (multihop). The far endpoint is whichever descriptor end is not
			// this visor: for a client-initiated route group (e.g. skysocks-
			// client → exit) the local visor is the descriptor's Dst and the far
			// peer is its Src, so comparing only against DstPK mislabels a
			// genuinely direct leg as multihop. A leg's first-hop transport
			// remote can never be this visor's own PK (self-loops are excluded),
			// so matching EITHER descriptor end means it reached the far one
			// directly — orientation-independent. A relayed leg's first hop lands
			// on an intermediate (neither Src nor Dst), so this stays false.
			leg.Direct = tp.Remote() == dstPK || tp.Remote() == rg.desc.SrcPK()
			leg.Alive = !tp.IsClosed()
			// Full forward route for this leg (all hops, full PKs, per-hop
			// transport type). Per-hop latency: hop 0 is the owned transport
			// RTT; a single-intermediate leg's far hop is derived from
			// route−transport RTT. legHopsFor takes rg.mu itself (unlocked here).
			if hops := rg.legHopsFor(tp.Entry.ID); len(hops) > 0 {
				leg.Hops = make([]RouteHopInfo, len(hops))
				for hi, h := range hops {
					leg.Hops[hi] = RouteHopInfo{
						TpID:   h.TpID.String(),
						From:   h.From.String(),
						To:     h.To.String(),
						TpType: string(transport.TypeFromTransportID(h.TpID, h.From, h.To)),
					}
				}
				leg.Hops[0].LatencyMS = leg.LatencyMS
				// For a DIRECT (1-hop) leg the whole route IS this single
				// transport hop, so its live end-to-end route latency and the
				// hop's transport RTT are the same physical measurement. Prefer
				// the E2E value (RouteLatencyMS) — it is the EWMA-smoothed
				// leg-liveness pong sampled every legLivenessInterval (30s),
				// whereas leg.LatencyMS is tp.GetLatency(): the RAW last sample
				// of the 60s transport-ping loop (SetLatency overwrites Avg, it
				// is not smoothed), so a single spike sticks for up to a minute
				// and the tree's left route-rtt and right transport-rtt disagree.
				// Mirrors snapshotLegs' E2E-preferred latency. Multihop legs keep
				// the near-edge transport RTT on hop 0.
				if leg.Direct && leg.RouteLatencyMS > 0 {
					leg.Hops[0].LatencyMS = leg.RouteLatencyMS
				} else if len(leg.Hops) == 2 && leg.RouteLatencyMS > leg.LatencyMS {
					leg.Hops[1].LatencyMS = leg.RouteLatencyMS - leg.LatencyMS
				}
			}
		}
		if rg.mux != nil {
			leg.Standby = rg.mux.isLegStandby(i)
		}
		info.Legs = append(info.Legs, leg)
	}
	if rg.mux != nil {
		info.Recovery = rg.recoverySnapshot(info.Legs)
	}
	return info
}

// recoverySnapshot reads the loss-recovery counters into their display shape.
// legs supplies RetxSent (the per-leg retransmit counters already gathered by
// MuxStats, so the leg slice is walked once rather than twice). Pure atomic
// loads plus one pass under the retx buffer's own mutex — safe to call from the
// telemetry path while the data path runs.
func (rg *RouteGroup) recoverySnapshot(legs []MuxLeg) *MuxRecovery {
	m := rg.mux
	if m == nil {
		return nil
	}
	held, minSeq, maxSeq := m.retxStats()
	rec := &MuxRecovery{
		WriteSeq:           m.writeSeqValue(),
		RetxHeld:           held,
		RetxMinSeq:         minSeq,
		RetxMaxSeq:         maxSeq,
		RetxSkippedMissing: m.retxSkippedMissing.Load(),
		RetxReqSACK:        m.retxReqSACK.Load(),
		RetxReqHOL:         m.retxReqHOL.Load(),
		RetxReqFlush:       m.retxReqFlush.Load(),
		RetxDeferredYoung:  m.retxDeferredYoung.Load(),
		RetxSendErrors:     m.retxSendErrors.Load(),
		SendWindowWaits:    m.sendWindowWaits.Load(),
		SendWindowTimeouts: m.sendWindowTimeouts.Load(),
		TLPProbes:          m.tlpProbes.Load(),
		SACKsRecv:          m.sacksRecv.Load(),
		LastSACKRecvMsAgo:  msSinceNano(&m.lastSACKRecvNano),
		LastSACKRecvContig: atomic.LoadUint32(&m.lastAckedContig),
		ReorderNextSeq:     m.reorderNextSeq(),
		ReorderPending:     m.reorderPending(),
		ReorderDrops:       m.reorderDrops.Load(),
		GapAgeMS:           float64(m.gapAge()) / float64(time.Millisecond),
		SACKsSent:          m.sacksSent.Load(),
		SACKSendErrors:     m.sackSendErrors.Load(),
		LastSACKSentMsAgo:  msSinceNano(&m.lastSACKSentNano),
		WedgeTicks:         rg.reorderWedgeTicks.Load(),
		Wedges:             rg.reorderWedges.Load(),
		LongestWedgeMS:     rg.reorderWedgeLongestMs.Load(),
	}
	for _, leg := range legs {
		rec.RetxSent += leg.Retransmits
	}
	return rec
}

// MuxInfo is a point-in-time snapshot of one route group's
// multiplexing state — per-leg byte/packet counters plus the
// transport identity each leg maps to. Returned by Router.MuxInfo
// for 'cli proxy mux-info'.
type MuxInfo struct {
	// Desc identifies the route group.
	Desc routing.RouteDescriptor
	// FarEndPK is the OTHER end of Desc — the peer this group's chains reach,
	// never this visor. The setup node hands EACH edge the descriptor that
	// points AT that edge, so Desc.DstPK() is the LOCAL visor on BOTH sides
	// (which is why LocalAddr is desc.Dst() and RemoteAddr is desc.Src());
	// reading DstPK as the "destination"/"exit" therefore printed this visor's
	// own key as the far end of every dialed group. Consumers render this
	// instead of deriving an end from Desc.
	FarEndPK cipher.PubKey
	// MuxEnabled is false for non-mux'd rg's; the per-leg counters
	// are still populated for the single transport in that case.
	MuxEnabled bool
	// SACKEnabled is true when the peers negotiated SACK retx.
	SACKEnabled bool
	// HOLRetxEnabled is true when the peers negotiated CapHOLRetx (proactive
	// head-of-line retransmit — the frontier-blocking seq is fast-retransmitted on
	// the fastest leg after ~one fast-leg RTT instead of the reactive waits).
	HOLRetxEnabled bool
	// PerFrameNoise is true when both edges negotiated CapPerFrameNoise and the
	// mux is sealing/opening each DATA frame under its own sequence-nonce (the
	// inverse-multiplexer path, network.EncryptConn bypassed). False means the
	// group runs the classic stream-noise wrap.
	PerFrameNoise bool
	// AppName is the app that DIALED this route group — the key its session's
	// shape and move history are held under. Distinct from KnobApp below,
	// which is only ever set when that app has an override of its own.
	AppName string
	// KnobApp names the app whose `route settings --app <name>` override set this
	// group resolved against; empty means it runs the visor-wide values. It is
	// how a paired subject/reference run proves the two clients really did get
	// different knobs.
	KnobApp string
	// Directional is true when unidirectional send selection (CapUniDir) is
	// active on this mux: each direction rides a disjoint leg class instead of
	// both striping every leg. Flipped reports the current direction->leg-class
	// mapping. DEFAULT (Flipped=false): this end sends its FORWARD/upload on the
	// DIRECT (1-hop) leg and its REVERSE/download on the MULTIHOP mux legs.
	// FLIPPED=true: the mapping is swapped (the heavy direction took the mux).
	// With the per-leg Direct flag, a reader can tell which class carries which
	// direction without log-grepping. Both false on a non-directional mux.
	Directional bool
	Flipped     bool
	// FlipPinned is the operator's MANUAL direction pin on a directional group:
	// "auto" (the flip controller is in charge — the default), "default" or
	// "flipped" (the mapping is pinned to that state and the controller is
	// dormant until released via mode auto). Empty on a non-directional mux.
	FlipPinned string
	// Distribution names how the mux spreads outbound packets across its legs
	// (the transportSelector weight mode: "auto", "round-robin", "weighted",
	// "capacity", "latency-adaptive", "sticky:5tuple", "size-threshold",
	// "dscp-priority"). Empty for a non-mux group.
	Distribution string
	// ReorderPending is the number of received packets currently buffered
	// out-of-order (head-of-line-blocking depth); ReorderGapAge is how long the
	// current reorder frontier gap has stayed open (0 when contiguous). A rising
	// pending count with a growing gap age is a stalled/black-holing leg.
	ReorderPending int
	ReorderGapAge  time.Duration
	// WriteSeq is the total DATA frames this mux has emitted outbound — a cheap
	// aggregate send-progress counter across all legs.
	WriteSeq uint32
	// FEC telemetry (see routeMux fec* counters). FECEnabled is true when both
	// peers negotiated CapFEC. FECRepairBytesSent/Recv are cumulative repair-frame
	// bytes — the TRUE FEC overhead, separable from data and retransmit.
	// FECReconstructs counts frontier frames recovered from repair.
	FECEnabled         bool
	FECRepairBytesSent uint64
	FECRepairBytesRecv uint64
	FECReconstructs    uint64
	// Legs is in tps[] order. One entry per active mux leg.
	Legs []MuxLeg
	// Events is this group's most recent leg/group changes with their reasons,
	// oldest first (the last muxEventsPerGroup of the router's ring). It puts
	// the churn next to the legs, so a `proxy mux info --json` that shows a leg
	// missing also shows who took it and why. The full ring is
	// diag.mux_events in `visor state`.
	Events []MuxEvent
	// Recovery is the loss-recovery view (sender retransmit machinery +
	// receiver reorder frontier). Nil for a group with no mux.
	Recovery *MuxRecovery
	// TunnelRole is the DIALING app's label for this route group — "active"
	// (it carries streams) or "standby" (held open and measured, ready to take
	// over). Empty for any group that is not one of a multi-tunnel app's
	// tunnels, and ALWAYS empty on the accepting end: an exit knows how many
	// tunnels a client holds but not which of them are in standby, so read the
	// role on the local end.
	// AgeMS is how long this route group has existed. For a pooled STANDBY
	// tunnel it is its AUDITION age — how long it has been held open, pinged
	// and measured without carrying a stream.
	AgeMS      float64
	TunnelRole string
	// DisjointRoutes is how many routes to this group.s far end share no
	// intermediate and no first hop (router.disjointRouteBound). Set by the
	// router on the snapshots it hands out; 0 when not yet counted.
	DisjointRoutes int
	// LegReserve is a standby group a leg split built: a chain the pool may
	// compose back in as a leg, but not a tunnel the app can promote.
	LegReserve bool
	// Shape is the SESSION's measured multiplexing shape — every ACTIVE
	// tunnel this app holds to this exit with its live leg count, written
	// "<k>x<n>" when the tunnels are the same width and "n1,n2,…" when they
	// are not. "4x1" is pure stream level, "1x4" pure packet level, "2x2"
	// the default. ShapeTarget is the shape the session is being held at and
	// ShapeSource where that target came from: "auto" (tunnel.count tunnels
	// at pool.active_width legs, today's rule) or "mux.shape" when the knob
	// names one. The target is ADVISORY — reported, not yet converged toward
	// (docs/design/mux-shape-axis.md).
	//
	// All three are empty on any group that is not an active tunnel: a
	// standby is the pool rather than the shape, and an accepting visor
	// cannot see which of a client's tunnels are in standby.
	Shape       string
	ShapeTarget string
	ShapeSource string
	// ShapeTunnels is the k the target asks for — the number of ACTIVE tunnels
	// the session is converging to. 0 under mux.shape=auto, where the count is
	// the app's own tunnel.count and nothing here overrides it. It is published
	// so the dialing app can promote toward the shape rather than toward
	// tunnel.count (docs/design/mux-shape-axis.md step 6).
	ShapeTunnels int
	// LastMove is the most recent shape move this session made and MoveCounts
	// the running tally, keyed by the four move names (pool_leg_taken,
	// pool_leg_released, tunnel_promoted, tunnel_parked). They make a shape
	// history readable without walking the event ring. Both nil on a session
	// that has never moved.
	LastMove   *MuxShapeMove
	MoveCounts map[string]uint64
}

// MuxRecovery is a route group's LOSS-RECOVERY state: the sender-side
// retransmit machinery and the receiver-side reorder frontier, side by side.
//
// It exists because a reorder WEDGE is a two-ended failure with a one-ended
// witness. Observed live: a pinned two-hop proxy session went dark for two
// minutes while the EXIT logged "reorder-WEDGE: frontier stuck at seq=56 …
// sender retransmit not refilling the gap"; our end — the sender — had nothing
// to show at all, so the two candidate causes were indistinguishable. Either
// the exit's SACKs never reached us (SACKsRecv flat, LastSACKRecvMsAgo growing)
// or they reached us naming a sequence the retx buffer had already evicted
// (RetxSkippedMissing climbing, and RetxMinSeq above the frontier the peer is
// stuck on). Every field here is a cheap atomic read; nothing is added to a
// hot path.
type MuxRecovery struct {
	// Sender side. WriteSeq is the next outgoing DATA sequence; RetxHeld /
	// RetxMinSeq / RetxMaxSeq are the unacknowledged window still retransmittable
	// (a peer's stuck frontier BELOW RetxMinSeq can never be healed); RetxSent is
	// retransmit packets put on the wire (summed across legs);
	// RetxSkippedMissing counts resend requests for sequences no longer held;
	// RetxSendErrors counts resends that failed to reach any leg; TLPProbes counts
	// tail-loss probes; SACKsRecv / LastSACKRecvMsAgo / LastSACKRecvContig are the
	// inbound feedback (ms-ago is -1 when no SACK has ever arrived).
	WriteSeq           uint32 `json:"write_seq"`
	RetxHeld           int    `json:"retx_held"`
	RetxMinSeq         uint32 `json:"retx_min_seq"`
	RetxMaxSeq         uint32 `json:"retx_max_seq"`
	RetxSent           uint64 `json:"retx_sent"`
	RetxSkippedMissing uint64 `json:"retx_skipped_missing"`
	RetxReqSACK        uint64 `json:"retx_req_sack"`
	RetxReqHOL         uint64 `json:"retx_req_hol"`
	RetxReqFlush       uint64 `json:"retx_req_flush"`
	// RetxDeferredYoung counts the holes a SACK named that were NOT resent
	// because the frame is still younger than the delay basis of the leg it
	// rode — the duplicate bytes kept off the wire.
	RetxDeferredYoung uint64 `json:"retx_deferred_young"`
	RetxSendErrors    uint64 `json:"retx_send_errors"`
	// SendWindowWaits / SendWindowTimeouts: writers parked because every ready
	// leg was at its per-leg send window, and the parks that gave up after
	// sendWindowWaitMax and sent anyway.
	SendWindowWaits    uint64  `json:"send_window_waits"`
	SendWindowTimeouts uint64  `json:"send_window_timeouts"`
	TLPProbes          uint64  `json:"tlp_probes"`
	SACKsRecv          uint64  `json:"sacks_recv"`
	LastSACKRecvMsAgo  float64 `json:"last_sack_recv_ms_ago"`
	LastSACKRecvContig uint32  `json:"last_sack_recv_contig"`
	// Receiver side. ReorderNextSeq is the frontier this end waits on,
	// ReorderPending how many packets are dammed behind it, GapAgeMS how long it
	// has been stuck. SACKsSent / SACKSendErrors / LastSACKSentMsAgo are the
	// outbound feedback (ms-ago -1 when none ever sent). WedgeTicks is the
	// CURRENT wedge's stall-tick count (0 = not wedged), Wedges how many have
	// cleared on this group, LongestWedgeMS the worst one seen. ReorderDrops
	// counts arrivals the reorder buffer discarded because the gap already held
	// a full window — they are deliberately NOT SACKed, so each one is owed a
	// retransmit.
	ReorderNextSeq    uint32  `json:"reorder_next_seq"`
	ReorderPending    int     `json:"reorder_pending"`
	ReorderDrops      uint64  `json:"reorder_drops"`
	GapAgeMS          float64 `json:"gap_age_ms"`
	SACKsSent         uint64  `json:"sacks_sent"`
	SACKSendErrors    uint64  `json:"sack_send_errors"`
	LastSACKSentMsAgo float64 `json:"last_sack_sent_ms_ago"`
	WedgeTicks        int64   `json:"wedge_ticks"`
	Wedges            uint64  `json:"wedges"`
	LongestWedgeMS    int64   `json:"longest_wedge_ms"`
}

// MuxLeg pairs the per-leg counters with the transport identity.
// The transport's own Latency / SentBytes / RecvBytes counters are
// the *transport-level* totals (across all rg's that share the
// transport); the LegStats here are *rg-scoped* (only what this
// rg sent through this leg).
type MuxLeg struct {
	LegStats
	TransportID string `json:"transport_id"`
	TpType      string `json:"tp_type"`
	RemotePK    string `json:"remote_pk"`
	// Source names where a leg that is NOT this group's own dial came from —
	// "standby :4, re-homed in place" for a leg the pool arbiter took
	// (pool_arbiter.go). Empty for an ordinary dialed leg.
	Source string `json:"source,omitempty"`
	// LatencyMS is the FIRST-HOP transport RTT in ms (the same value
	// 'tp ls' shows). For a multihop leg this is only the near edge, NOT
	// the whole path — use RouteLatencyMS for the end-to-end route.
	LatencyMS float64 `json:"latency_ms"`
	// RouteLatencyMS is the leg's TRUE end-to-end route latency in ms —
	// the EWMA-smoothed round-trip of the per-leg liveness pong across
	// ALL hops (from legE2ELatency). This is the number a routing policy
	// judges a leg by; it can differ sharply from LatencyMS on a multihop
	// leg. Zero until the first pong lands.
	RouteLatencyMS float64 `json:"route_latency_ms"`
	// AckDelayMS is this leg's own EWMA send→ack delay in ms (0 = no sample):
	// the loaded feedback delay its retransmit threshold is judged by
	// (rackThresholdFor), distinct from the idle RTTs above.
	AckDelayMS float64 `json:"ack_delay_ms"`
	// InflightBytes / WindowBytes: the leg's real unacknowledged bytes and the
	// per-leg send window they are bounded by (0 when not a predictive mode).
	InflightBytes float64 `json:"inflight_bytes"`
	WindowBytes   float64 `json:"window_bytes"`
	// Direct is true when this leg's first hop goes straight to the route
	// group's destination (a 1-hop/direct route); false means the leg is
	// multihop (relayed through one or more intermediates). Lets a viewer
	// tell the direct leg from the multihop legs at a glance.
	Direct bool `json:"direct"`
	// Alive is false once the leg's transport is closed. Standby is true
	// when the leg is a WARM STANDBY: rules kept alive but not selected
	// for sending (see docs/warm_standby_legs_rfc.md). Together they are
	// the leg's gate_state for the per-leg telemetry harness.
	Alive   bool `json:"alive"`
	Standby bool `json:"standby"`
	// Hops is this leg's FULL forward route — every hop from this visor to
	// the destination, with full (untruncated) From/To PKs and per-hop
	// transport type. Per-hop LatencyMS is filled where known (first hop
	// owned; single-intermediate far hop derived from route−transport RTT).
	// Empty when the route path wasn't recorded (legacy/accepted routes).
	Hops []RouteHopInfo `json:"hops,omitempty"`
	// CapacityPriorBps is the pool arbiter's PRIOR throughput estimate for
	// this leg's transport (throughputPrior: the transport's live measured
	// rate if it has one, else its catalog entry) — the number a standby
	// tunnel is ranked by BEFORE it is measured. Distinct from GoodputBps,
	// which is the leg's own live EWMA once it has carried traffic.
	CapacityPriorBps float64 `json:"capacity_prior_bps,omitempty"`
}
