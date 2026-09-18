// Package routersettings pkg/router/routersettings/catalog.go c2-net-routing
//
// The knob catalog. Every DEFAULT here is the constant pkg/router compiled
// with; pkg/router's TestCatalogDefaultsMatchConstants asserts each pair, so
// the two cannot drift. Names are lowercase dotted and grouped by the
// machinery they steer, matching `proxy settings`.
package routersettings

import "time"

// Knob handles. A use site reads its own handle, which costs one atomic load.
//
//nolint:gochecknoglobals // the catalog IS package state, by design
var (
	// Latency-band admission (route_group.go). An ACTIVE leg more than
	// BandDemoteRatio off the active-set median latency is demoted to warm
	// standby; it is re-admitted only inside the tighter BandAdmitRatio, and the
	// gap between the two is the hysteresis that stops a band-edge leg flapping.
	// The *Tight pair is the same hysteresis in capacity (aggregation) mode,
	// where the arrivals must stay near-in-order for the no-skip frontier.
	BandDemoteRatio      = RegisterRatio("band.demote_ratio", 3.0, 1.0, "how far off the active-set median latency an active leg may sit before it is demoted to warm standby")
	BandAdmitRatio       = RegisterRatio("band.admit_ratio", 2.5, 1.0, "how close to the median a warm-standby leg must measure before it is re-admitted (the hysteresis half of band.demote_ratio)")
	BandDemoteRatioTight = RegisterRatio("band.demote_ratio_tight", 2.0, 1.0, "band.demote_ratio while the mux is in capacity (aggregation) mode")
	BandAdmitRatioTight  = RegisterRatio("band.admit_ratio_tight", 1.6, 1.0, "band.admit_ratio while the mux is in capacity (aggregation) mode")
	BandGoodputGateFrac  = RegisterRatioRange("band.goodput_gate_frac", 0.15, 0, 1, "fraction of the best active leg's goodput that spares a leg from latency demotion (high latency it earned by queuing is not a bad route)")
	BandMinLegs          = RegisterMin("band.min_legs", KindCount, 3, 2, "legs carrying a measured latency before band admission runs at all")

	// Per-leg liveness and failover (route_group.go).
	LegLivenessInterval       = RegisterMin("leg.liveness_interval", KindDuration, int64(30*time.Second), int64(time.Second), "how often each multiplexed leg is probed end-to-end with a Ping the destination echoes")
	LegPongMissThreshold      = RegisterMin("leg.pong_miss_threshold", KindCount, 3, 1, "consecutive liveness probes with no echo after which a leg is treated as black-holing and dropped")
	LegDataProgressInterval   = RegisterMin("leg.data_progress_interval", KindDuration, int64(5*time.Second), int64(250*time.Millisecond), "how often the fast data-progress prune samples each leg's delivered bytes")
	LegDataStallGapAge        = RegisterMin("leg.data_stall_gap_age", KindDuration, int64(3*time.Second), int64(100*time.Millisecond), "how long a reorder frontier gap must stay open before the data-progress prune's STALLED path acts")
	LegBlackHoleMinTopBytes   = RegisterMin("leg.blackhole_min_top_bytes", KindBytes, 128*1024, 1024, "bytes the leading leg must have moved over one data-progress interval before a near-silent sibling may be judged a black hole")
	LegSoleBlackHoleSentFloor = RegisterMin("leg.sole_blackhole_sent_floor", KindBytes, 256, 1, "bytes a sole leg must have SENT before its zero delivery counts as a dead route")
	LegSoleBlackHoleTicks     = RegisterMin("leg.sole_blackhole_ticks", KindCount, 3, 1, "consecutive data-progress intervals a sole leg must deliver nothing before a replacement is dialed")
	LegStateResyncInterval    = RegisterMin("leg.state_resync_interval", KindDuration, int64(7*time.Second), int64(time.Second), "how often the active-set side re-asserts its COMPLETE standby/active set to the peer (CapLegState)")
	LegParkMinHold            = RegisterMin("leg.park_min_hold", KindDuration, int64(30*time.Second), int64(time.Second), "how long an adaptive park holds before a leg may be re-admitted")

	// The outclassed-leg gate (route_mux.go ruleProbeOnlyLegsLocked).
	LegStarveRatio     = RegisterRatio("leg.starve_ratio", 6.0, 1.0, "how many times the best active leg's delay basis a leg's own may exceed before it is cut to a probe per window")
	LegProbeBytes      = RegisterMin("leg.probe_bytes", KindBytes, 64*1024, 1024, "what a leg cut to probe-only may carry per window")
	LegProbeMinBasisMs = RegisterRatio("leg.probe_min_basis_ms", 250.0, 1.0, "absolute delay basis in ms under which no leg is ever cut to a probe, so two fast legs with a large ratio are left alone")
	LegProbeMinWindow  = RegisterMin("leg.probe_min_window", KindDuration, int64(250*time.Millisecond), int64(10*time.Millisecond), "floor on the probe window for a leg whose delay basis is short")
	LegDelivAlpha      = RegisterRatioRange("leg.deliv_alpha", 0.3, 0, 1, "weight of the newest sample in the per-leg SACK-proven delivery EWMA the goodput half of the gate reads")

	// RACK-TLP retransmit threshold (route_mux.go, rack_tlp.go, sack.go).
	RackReorderFactor       = RegisterRatio("rack.reorder_factor", 1.25, 1.0, "slowest active leg's RTT times this is the reordering tolerance before a sequence is presumed lost")
	RackFloor               = RegisterMin("rack.floor", KindDuration, int64(60*time.Millisecond), int64(time.Millisecond), "never retransmit sooner than this (anti-storm)")
	RackCeil                = RegisterMin("rack.ceil", KindDuration, int64(1500*time.Millisecond), int64(10*time.Millisecond), "never wait longer than this before presuming loss")
	RackDefaultNoRTT        = RegisterMin("rack.default_no_rtt", KindDuration, int64(300*time.Millisecond), int64(time.Millisecond), "retransmit threshold before any leg RTT has been measured")
	RackFactorMax           = RegisterRatio("rack.factor_max", 3.0, 1.0, "ceiling on the DSACK-driven widening of rack.reorder_factor")
	RackDSACKGrowStep       = RegisterMin("rack.dsack_grow_step", KindCount, 250, 1, "thousandths the reorder factor widens by per DSACK (a spurious retransmit observed)")
	RackDecayStep           = RegisterMin("rack.decay_step", KindCount, 25, 1, "thousandths the reorder factor narrows by per clean SACK, back toward the baseline")
	RackRetxMinAge          = RegisterMin("rack.retx_min_age", KindDuration, int64(750*time.Millisecond), int64(10*time.Millisecond), "fallback minimum age of an unacked sequence before retransmit when no RACK threshold applies")
	RackRetxBackoffMaxShift = RegisterMin("rack.retx_backoff_max_shift", KindCount, 3, 1, "how many doublings the per-sequence retransmit backoff may take")

	// The receiver's reorder buffer (route_mux.go, reorder.go, route_group.go).
	ReorderWindow        = RegisterMin("reorder.window", KindCount, 32768, 256, "how many out-of-order packets the receiver holds before the last-resort drop; also the sender's retransmit buffer depth")
	ReorderTimeout       = RegisterMin("reorder.timeout", KindDuration, int64(1500*time.Millisecond), int64(50*time.Millisecond), "how long a frontier gap may stay open before it counts as a stall (it is never skipped)")
	ReorderStallInterval = RegisterMin("reorder.stall_interval", KindDuration, int64(500*time.Millisecond), int64(20*time.Millisecond), "how often the receive side checks for a stuck frontier gap and SACKs it")

	// Per-leg send window (route_mux.go). ecf.window_margin multiplies the
	// SACK-proven delivery-per-RTT; the result is clamped between the floor and
	// the ceiling, and send.window_refresh_interval paces its growth.
	EcfMaxWindowBytes = RegisterMin("ecf.max_window_bytes", KindBytes, 8*1024*1024, 4096, "per-leg send window ceiling")
	EcfMinWindowBytes = RegisterMin("ecf.min_window_bytes", KindBytes, 128*1024, 1024, "per-leg send window floor")
	EcfWindowMargin   = RegisterRatio("ecf.window_margin", 2.0, 0, "multiplier on proven delivery-per-RTT that sets a leg's send window")
	SendWindowWaitMax = RegisterMin("send.window_wait_max", KindDuration, int64(250*time.Millisecond), int64(time.Millisecond), "how long a writer parks when every ready leg is at its window before sending anyway")
	SendWindowPoll    = RegisterMin("send.window_poll", KindDuration, int64(20*time.Millisecond), int64(time.Millisecond), "how often a parked writer re-checks, in case a wake-up was coalesced")
	// SendWindowRefreshInterval was excluded from the first live set because it
	// becomes a route group's ticker when the group is built. serviceKnobLoop
	// re-reads it each tick and resets the ticker on a change, so it is live now;
	// the 10 ms floor is what keeps a mistyped value from spinning the loop.
	SendWindowRefreshInterval = RegisterMin("send.window_refresh_interval", KindDuration, int64(100*time.Millisecond), int64(10*time.Millisecond), "how often each leg's send window is recomputed; a window can double per refresh, so this paces the ramp")

	// SACK feedback cadence (route_mux.go, route_group.go).
	SackMinInterval     = RegisterMin("sack.min_interval", KindDuration, int64(25*time.Millisecond), int64(time.Millisecond), "minimum spacing between receiver-side SACKs")
	SackDelayedAckDelay = RegisterMin("sack.delayed_ack_delay", KindDuration, int64(100*time.Millisecond), int64(time.Millisecond), "how long after clean in-order delivery the one-shot delayed ack fires")

	// Tail-loss probe (rack_tlp.go).
	TLPPTOFactor     = RegisterRatio("tlp.pto_factor", 2.0, 1.0, "probe timeout is this times the slowest active leg's RTT (RFC 8985)")
	TLPMinPTO        = RegisterMin("tlp.min_pto", KindDuration, int64(100*time.Millisecond), int64(time.Millisecond), "never probe sooner than this (anti-spurious)")
	TLPMaxPTO        = RegisterMin("tlp.max_pto", KindDuration, int64(2*time.Second), int64(10*time.Millisecond), "never wait longer than this before probing the tail")
	TLPMaxProbes     = RegisterMin("tlp.max_probes", KindCount, 2, 1, "consecutive tail probes per stall before deferring to the other recovery paths")
	TLPCheckInterval = RegisterMin("tlp.check_interval", KindDuration, int64(100*time.Millisecond), int64(10*time.Millisecond), "tail-loss-probe service-loop cadence")

	// Proactive head-of-line retransmit (hol_retx.go).
	HolRetxGapFloor    = RegisterMin("hol.gap_floor", KindDuration, int64(4*time.Millisecond), int64(time.Millisecond), "low floor on the frontier-gap age that triggers a proactive HoL retransmit")
	HolRetxRTTFactor   = RegisterRatio("hol.rtt_factor", 1.0, 0, "fastest live leg's RTT times this is the gap-age threshold for a proactive HoL retransmit")
	HolRetxMaxFill     = RegisterMin("hol.max_fill", KindCount, 4, 1, "how many contiguous missing sequences one proactive nudge retransmits")
	HolRetxPerSeqFloor = RegisterMin("hol.per_seq_floor", KindDuration, int64(4*time.Millisecond), int64(time.Millisecond), "low floor on the per-sequence re-nudge interval, so a stuck frontier is not resent in a storm")

	// Shared-bottleneck detection (bottleneck.go). sbd.enabled is the explicit
	// off switch the rig used to fake with a huge sbd.min_samples.
	SBDEnabled         = RegisterBool("sbd.enabled", true, "run shared-bottleneck detection at all; off stops both the sampling and the rulings")
	SBDMinSamples      = RegisterMin("sbd.min_samples", KindCount, 4, 1, "per-leg delay samples a shared-bottleneck verdict needs before it may group a leg")
	SBDSampleInterval  = Register("sbd.sample_interval", KindDuration, int64(50*time.Millisecond), "minimum spacing between two per-SACK delay samples folded into one leg's window")
	SBDWindowSamples   = RegisterMin("sbd.window_samples", KindCount, 8, 2, "moving-window depth per leg over which the delay summary statistics are computed")
	SBDSkewTol         = RegisterRatio("sbd.skew_tol", 0.5, 0, "maximum |skew_i - skew_j| for two legs to be judged co-bottlenecked")
	SBDCVTolFrac       = RegisterRatio("sbd.cv_tol_frac", 0.5, 0, "maximum relative difference in coefficient of variation for two legs to be judged co-bottlenecked")
	SBDFreqTol         = RegisterRatio("sbd.freq_tol", 0.4, 0, "maximum |freq_i - freq_j| (mean-crossing rate) for two legs to be judged co-bottlenecked")
	SBDTrialWindow     = Register("sbd.trial_window", KindDuration, int64(3*time.Second), "how long a shared-bottleneck park is held as a trial before the aggregate goodput is re-read")
	SBDTrialLoss       = RegisterRatioRange("sbd.trial_loss", 0.15, 0, 1, "fraction of aggregate goodput a park may cost before it is undone")
	SBDBackoff         = Register("sbd.backoff", KindDuration, int64(5*time.Minute), "how long a pair whose park trial failed is exempt from shared-bottleneck merging (doubles per repeat)")
	SBDMinEvidenceRate = RegisterMin("sbd.min_evidence_rate", KindBytes, 64*1024, 1024, "aggregate goodput a group must carry before a shared-bottleneck ruling may park a leg")
	SBDDemote          = RegisterBool("sbd.demote", false, "let a shared-bottleneck ruling PARK a leg; off means rulings are recorded as sbd_ruling mux events only")

	// FEC block geometry (fec_mux.go). K data frames plus R repair frames per
	// block recovers all K from any K of the K+R.
	FECEnabled = RegisterBool("fec.enabled", false, "advertise FEC on NEW mux route groups")
	FECK       = RegisterMin("fec.k", KindCount, 8, 1, "data frames per FEC block")
	FECR       = RegisterMin("fec.r", KindCount, 2, 1, "repair frames per FEC block")

	// ECF selector internals (transport_selector.go).
	EcfBeta               = RegisterRatio("ecf.beta", 0.25, 0, "hysteresis: a latched wait inflates the slow leg's delivery estimate by 1+this on the next pick")
	EcfDefaultFrameBytes  = RegisterMin("ecf.default_frame_bytes", KindBytes, 1024, 1, "in-flight increment charged for a frame whose size the caller did not supply")
	EcfRttAlpha           = RegisterRatio("ecf.rtt_alpha", 0.3, 0, "weight of the newest sample in the per-leg mean-RTT EWMA")
	EcfJitterAlpha        = RegisterRatio("ecf.jitter_alpha", 0.3, 0, "weight of the newest sample in the per-leg jitter (sigma) EWMA")
	EcfColdBootstrapBytes = RegisterMin("ecf.cold_bootstrap_bytes", KindBytes, 64*1024, 1024, "how much a leg of unknown capacity may carry before a spill is forced, so cold start fans out instead of hammering one leg")
	EcfCongestRttFactor   = RegisterRatio("ecf.congest_rtt_factor", 4.0, 1.0, "a leg counts as saturated once its mean RTT passes this multiple of its own baseline")
	EcfRttMinCreep        = RegisterRatioRange("ecf.rtt_min_creep", 0.02, 0, 1, "per-refresh fraction of the mean-baseline RTT gap by which a leg's baseline creeps upward")

	// The unidirectional flip controller (unidir.go).
	UnidirFlipInterval      = RegisterMin("unidir.flip_interval", KindDuration, int64(time.Second), int64(100*time.Millisecond), "flip-controller tick cadence")
	UnidirFlipRatio         = RegisterRatio("unidir.flip_ratio", 2.0, 1.0, "flip the direction-to-leg-class mapping when the heavy direction is at least this times the light one")
	UnidirFlipHysteresis    = RegisterMin("unidir.flip_hysteresis", KindCount, 3, 1, "consecutive qualifying ticks before flipping")
	UnidirFlipCooldownTicks = RegisterMin("unidir.flip_cooldown_ticks", KindCount, 3, 1, "ticks to hold after a flip before another")
	UnidirFlipMinGoodput    = RegisterMin("unidir.flip_min_goodput", KindBytes, 8192, 1, "bytes/sec floor under which the flip controller ignores the asymmetry as idle noise")

	// FORWARD-direction confinement (route_mux.go selectConfinedForward).
	ForwardSpill         = RegisterBool("forward.spill", false, "let a FORWARD frame leave its confined leg when that leg is at its send window; off means the writer waits")
	ForwardSwitchMargin  = RegisterRatioRange("forward.switch_margin", 0.2, 0, 1, "how much lower a challenger leg must measure before the forward direction moves to it")
	ForwardSwitchSamples = RegisterMin("forward.switch_samples", KindCount, 2, 1, "consecutive refreshes a challenger must clear forward.switch_margin for")

	// The TRANSIT write path (router_forward.go). A packet this visor only
	// relays used to be written to the next hop SYNCHRONOUSLY from the single
	// inbound packet loop, with a context that never cancels, so one peer that
	// stopped draining froze every route group on the visor for up to
	// managed_transport writeTimeout (1 minute). The write is now handed to a
	// bounded per-transport queue with one writer goroutine, and the write
	// itself carries forward.write_timeout as its deadline.
	ForwardWriteTimeout    = RegisterMin("forward.write_timeout", KindDuration, int64(2*time.Second), int64(50*time.Millisecond), "how long ONE transit (forward/intermediary) write may take before the frame is dropped; it is also the deadline charged to the underlying conn, so it caps how long a wedged peer can hold the transport write lock")
	ForwardQueueDepth      = RegisterMin("forward.queue_depth", KindCount, 1024, 1, "frames the per-next-hop transit queue holds before a further frame is dropped as forward_drop_queue_full (read when a transport first forwards; a change applies to queues created after it)")
	ForwardDropEventWindow = RegisterMin("forward.drop_event_window", KindDuration, int64(time.Minute), int64(time.Second), "how often ONE transport may record a forward_drops mux event, so a peer that black-holes a bulk transfer costs one line per window, not one per frame")

	// Route exclusion after an early death (dead_route_cache.go).
	DeadRouteHold    = RegisterMin("route.dead_hold", KindDuration, int64(60*time.Second), int64(time.Second), "how long a route that died young is kept out of the next diversify search")
	DeadRouteHoldMax = RegisterMin("route.dead_hold_max", KindDuration, int64(8*time.Minute), int64(time.Second), "ceiling on the doubling applied to that window on each repeat death")

	// Dial-time route ranking and candidate supply (router_dial.go,
	// dial_tunnel_legs.go, warm_route_pool.go, dead_route_cache.go). These are
	// read while a dial is CHOOSING a route, not while a group is sending — the
	// `route settings dial` view prints this section on its own.
	DialUnknownLatencyCostMs = RegisterScale("dial.unknown_latency_cost_ms", 1000.0, "what a hop with NO latency measurement costs a candidate's score, in ms; 0 drops the penalty")
	DialUnknownHopPenaltyMs  = RegisterScale("dial.unknown_hop_penalty_ms", 150.0, "what ONE unmeasured hop costs a partially measured path, in ms; 0 drops the penalty")
	DialTypePriorScale       = RegisterScale("dial.type_prior_scale", 1.0, "multiplier on the transport-TYPE ranking prior (the class penalty a hop carries until its link has a throughput measurement); 0 ranks on RTT alone")
	DialThroughputPriorScale = RegisterScale("dial.throughput_prior_scale", 1.0, "multiplier on the MEASURED-throughput ranking band; 0 ranks on RTT alone")
	DialCandidates           = RegisterMin("dial.candidates", KindCount, 3, 1, "floor on how many routes a mux dial asks the route finder for")
	DialCandidateHeadroom    = RegisterZeroable("dial.candidate_headroom", KindCount, 2, "extra routes requested on top of the mux degree so the disjoint pick can still reach its target; 0 asks for exactly the degree")
	DialForegroundMux        = RegisterMin("dial.foreground_mux", KindCount, 16, 1, "how many mux legs are established SYNCHRONOUSLY at dial time before the background self-heal fills the rest")
	DialTunnelLegs           = RegisterSigned("dial.tunnel_legs", KindCount, 0, -1, "legs an app tunnel is dialed with: 0 = exactly what the app asked for, -1 = the visor's mux width, n = n legs (per-app scopeable)")
	DeadRouteYoungAge        = RegisterMin("route.dead_young_age", KindDuration, int64(12*time.Second), int64(time.Second), "how soon after dial a route's death counts as evidence the route is dead rather than a normal teardown")
	WarmPlanTTL              = RegisterMin("warm.plan_ttl", KindDuration, int64(30*time.Second), int64(time.Second), "staleness bound on a cached disjoint route plan in the warm pool")
	WarmPlanBucketCap        = RegisterMin("warm.plan_bucket_cap", KindCount, 64, 1, "how many distinct disjoint plans the warm pool holds per exit")

	// Batched route setup (setup_batch_client.go, setup_batch.go, oracle_plan_cache.go).
	// A multi-route dial to one exit — a standby-pool fill, --tunnels N, the mux
	// leg self-heal — collects its siblings for setup.batch_window and sends them
	// as ONE request the setup node coalesces per hop. The batch form is used only
	// when the node advertised CapBatchRouteSetup; these knobs shape it, they do
	// not gate it.
	SetupBatchWindow       = RegisterMin("setup.batch_window", KindDuration, int64(40*time.Millisecond), int64(time.Millisecond), "how long a route setup waits to collect sibling dials to the same exit before sending them as one batched request")
	SetupBatchMax          = RegisterMin("setup.batch_max", KindCount, 16, 1, "most routes carried in one batched setup request; 1 disables batching and sends singles")
	SetupFillInflight      = RegisterMin("setup.fill_inflight", KindCount, 8, 1, "how many standby-pool tunnel dials run concurrently, which is also how many routes a fill can offer one batch")
	SetupPlanClaimTTL      = RegisterMin("setup.plan_claim_ttl", KindDuration, int64(20*time.Second), int64(time.Second), "how long a concurrent dial holds its claim on an oracle candidate path, so N dials in one fill take N DISTINCT intermediates")
	SetupFirstHopFilterMax = RegisterMin("setup.first_hop_filter_max", KindCount, 8, 1, "held first hops beyond which first-hop diversity stops being a filter and becomes a ranking term, so a deep pool can still grow over a reused first hop with a distinct intermediate")

	// Mux event history (mux_events.go). The per-group ring is what keeps a
	// chatty group from evicting another group's history: `mux info` reads the
	// group's OWN ring, not the shared one.
	MuxEventRingSize     = RegisterMin("mux.event_ring_size", KindCount, 256, 8, "how many mux events the router's shared ring keeps")
	MuxEventRingPerGroup = RegisterMin("mux.event_ring_per_group", KindCount, 64, 8, "how many of its own events each route group keeps, so a chatty group cannot evict another's history")
	MuxEventsPerGroup    = RegisterMin("mux.events_per_group", KindCount, 8, 1, "how many of a group's own events ride along in its mux info snapshot")

	// Negotiated capabilities. Each is advertised on NEW route groups only: a
	// group already running keeps the capabilities it was born with, so turning
	// one off never breaks a live session.
	MuxPerFrameNoise = RegisterBool("mux.per_frame_noise", true, "advertise CapPerFrameNoise (the inverse multiplexer's per-frame AEAD) on NEW route groups")
	MuxSACK          = RegisterBool("mux.sack", true, "advertise CapSACK (selective-acknowledgement retransmit) on NEW route groups")
	MuxHOLRetx       = RegisterBool("mux.hol_retx", true, "advertise CapHOLRetx (proactive head-of-line retransmit) on NEW route groups")
)
