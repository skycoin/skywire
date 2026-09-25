// Package router pkg/router/route_group_legs.go c2-net-routing
package router

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// SetLegChangeHook attaches a leg-change callback to the route
// group. Called once at construction time (from saveRouteGroupRules)
// by the dialing side; accept-side route groups are unaffected
// because they don't run policies.
func (rg *RouteGroup) SetLegChangeHook(hook LegChangeHook, info DialInfo) {
	rg.mu.Lock()
	rg.legChangeHook = hook
	rg.legChangeInfo = info
	rg.mu.Unlock()
}

// SetRotation attaches the rotation hook + apply-add callback +
// tick interval. Called once at construction time when the
// policy's decide_route returned a non-zero
// RotationIntervalSeconds. The actual rotation goroutine starts
// via startOffServiceLoops; calling SetRotation after that point
// is racy and unsupported.
//
// applyAdd is the router-side callback that dials one more aux
// forward leg with the policy's ExcludeHops as the disjoint-
// intermediate filter. It runs in the rotation goroutine's
// own context so a slow setup-node dial doesn't block other
// route groups' rotation. applyAddForward is the FORWARD-ONLY
// analog (appendRouteAsymmetric addFwd=true/addRev=false), dialed
// for a RotationAction.AddForwardLeg; nil disables forward widening.
func (rg *RouteGroup) SetRotation(hook RotationHook, applyAdd, applyAddForward func(excludeHops []string), interval time.Duration) {
	rg.mu.Lock()
	rg.rotationHook = hook
	rg.rotationApplyAdd = applyAdd
	rg.rotationApplyAddForward = applyAddForward
	rg.rotationInterval = interval
	// A promoting rotation engine is wired: route new aux legs through the warm
	// standby pool on add so the engine admits them one per tick, goodput-gated,
	// instead of every dialed leg going hot at once. Propagate to the mux if it
	// already exists; otherwise mux creation in handleHandshake applies it.
	promoting := interval > 0 && hook != nil
	rg.standbyNewLegs = promoting
	if promoting && rg.mux != nil {
		rg.mux.SetStandbyNewLegs(true)
	}
	rg.mu.Unlock()
	// Start the rotation goroutine now (startOffServiceLoops fired
	// during initial setup before SetRotation was called, so the
	// conditional in startOffServiceLoops saw zero values and didn't
	// spawn this loop). Safe to call here because the loop checks
	// the same close channels (rg.closed / rg.remoteClosed) that
	// startOffServiceLoops' loops do — a Close() racing with
	// SetRotation just terminates the loop immediately.
	if interval > 0 && hook != nil {
		go rg.servicePacketLoop("rotation", interval, rg.rotationServiceFn, rg.rotateNow)
	}
}

// SetSelfHeal wires the background degree-restoration callback for a
// multiplexed route group. When a leg is pruned and the live degree drops
// below target, the route group dials replacement aux legs (via applyAdd)
// without blocking traffic — surviving legs carry the load meanwhile. target
// is the requested mux degree (legs the group should maintain). A target of
// 0 or 1 disables self-heal (a single-leg group has nothing to spread to).
func (rg *RouteGroup) SetSelfHeal(applyAdd func(excludeHops []string), target int) {
	if target > 1 && !rg.poolWideningAllowed() {
		target = 1 // a standby tunnel holds one leg, whatever the width says
	}
	rg.mu.Lock()
	rg.selfHealAdd = applyAdd
	rg.selfHealTarget = target
	rg.mu.Unlock()
}

// setSelfHealTarget updates ONLY the self-heal degree, leaving the applyAdd
// callback in place. The rotation loop calls this each tick for an adaptive
// group (see SelfHealTargeter) so a runtime mux retune — lowering the warm-
// standby reserve or the active width over the mux-control RPC — re-caps the
// live self-heal target instead of letting maybeSelfHeal keep re-dialing back
// toward the (larger) dial-time value.
func (rg *RouteGroup) setSelfHealTarget(target int) {
	if target > 1 && !rg.poolWideningAllowed() {
		target = 1 // a standby tunnel holds one leg, whatever the width says
	}
	rg.mu.Lock()
	rg.selfHealTarget = target
	rg.mu.Unlock()
}

// aliveLegCount returns the number of live legs (non-nil, unclosed
// transports). Caller must NOT hold rg.mu.
func (rg *RouteGroup) aliveLegCount() int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	n := 0
	for _, tp := range rg.tps {
		if tp != nil && !tp.IsClosed() {
			n++
		}
	}
	return n
}

// signalRotate wakes the rotation loop to run its on_tick controller NOW, out of
// band from the periodic interval — called on a leg death so drop-recovery
// promotes a warm standby into the active set immediately instead of on the next
// interval. Non-blocking (buffered-1 + default): a death during an in-progress
// tick coalesces into a single extra run, and a nil channel (a route group built
// without rotation) is a no-op. The selector's emergency standby fallback keeps
// traffic flowing in the meantime; this restores a proper active set right after.
func (rg *RouteGroup) signalRotate() {
	select {
	case rg.rotateNow <- struct{}{}:
	default:
	}
}

// maybeSelfHeal restores the multiplexed degree after a leg drop. If the live
// leg count fell below target and no replacement is already in flight, it
// dials replacement aux legs in the background until the degree is restored
// (or a bounded number of attempts is exhausted, so a destination with no
// remaining disjoint paths can't loop forever). Non-blocking: surviving legs
// keep carrying traffic while replacements set up. healInFlight bounds this to
// one concurrent heal so a flapping leg can't spawn a storm of setup dials.
//
// Must NOT be called while holding rg.mu.
// healReplaceSoleLeg dials ONE replacement leg for a group whose sole active
// route is black-holing (see the sole-leg reaping in legDataProgressServiceFn).
// Unlike maybeSelfHeal it is NOT gated on target>1 — a --mux 1 client must be
// able to escape a dead route — but it still adds only a single leg and is
// bounded by healInFlight so a persistently-bad route can't spawn a dial storm.
// Once the second leg is up the ordinary black-hole prune retires the dead one.
func (rg *RouteGroup) healReplaceSoleLeg() {
	rg.mu.Lock()
	add := rg.selfHealAdd
	rg.mu.Unlock()
	if add == nil || rg.isClosed() {
		return
	}
	if !rg.healInFlight.CompareAndSwap(false, true) {
		return // a heal is already dialing
	}
	defer rg.healInFlight.Store(false)
	if rg.logger != nil {
		rg.logger.Info("sole-leg black-hole: dialing a replacement route")
	}
	add(nil) // one setup-node dial; appends one fresh leg on success
}

func (rg *RouteGroup) maybeSelfHeal() {
	rg.mu.Lock()
	add := rg.selfHealAdd
	target := rg.selfHealTarget
	rg.mu.Unlock()
	// A STANDBY tunnel is single-leg by construction: widening one spends a
	// second chain to the exit on a tunnel carrying nothing (pool_arbiter.go).
	// The top-up is an ACTIVE-tunnel power; a group with no role at all is
	// unaffected.
	if !rg.poolWideningAllowed() {
		return
	}
	if add == nil || target <= 1 || rg.isClosed() {
		return
	}
	if rg.aliveLegCount() >= target {
		return
	}
	if !rg.healInFlight.CompareAndSwap(false, true) {
		return // a replacement is already dialing
	}
	go func() {
		defer rg.healInFlight.Store(false)
		// Each add(nil) blocks ~one setup-node dial and, on success, appends one
		// leg. Re-check the live count between attempts and stop as soon as the
		// degree is restored or the group closes.
		//
		// NO-PROGRESS BACKOFF: with the standby pool uncapped (target ~513), the
		// achievable degree is bounded by the destination's disjoint-intermediate
		// set, which is usually far below target. Once the pool is filled to what
		// the topology offers, every further add fails ("failure code 1: transport
		// already in the group" / "setup-node dial: context deadline exceeded")
		// and re-dialing target-more times would hammer the setup node for minutes
		// (the observed storm). So compare the live count before/after each add:
		// after selfHealNoProgressLimit consecutive adds that grow the degree by
		// nothing, the disjoint set is exhausted for now — settle at the degree we
		// have and stop. A later leg death (which frees an intermediate) or newly-
		// online transports (which open fresh disjoint paths) re-trigger this and
		// the pool grows again, so the target is never a hard cap — the fill just
		// tracks the topology instead of storming past it.
		noProgress := 0
		for attempt := 0; attempt < target+1; attempt++ {
			before := rg.aliveLegCount()
			if rg.isClosed() || before >= target {
				return
			}
			if rg.logger != nil {
				rg.logger.WithField("alive", before).
					WithField("target", target).
					Debug("Mux self-heal: dialing replacement leg to restore degree")
			}
			add(nil)
			if rg.aliveLegCount() > before {
				noProgress = 0
				continue
			}
			noProgress++
			if noProgress >= selfHealNoProgressLimit {
				if rg.logger != nil {
					rg.logger.WithField("alive", rg.aliveLegCount()).
						WithField("target", target).
						Debug("Mux self-heal: no disjoint path available right now; settling at current degree")
				}
				return
			}
		}
	}()
}

// snapshotLegs builds a []LegInfo from the current rg.tps. Caller
// must hold rg.mu. Used by the leg-change fire path to give the
// policy script a snapshot it can iterate.
func (rg *RouteGroup) snapshotLegs() []LegInfo {
	// Intermediate-PK chain shared by all legs of this route group:
	// the forward path's hop To-PKs minus the final destination. The
	// primary leg (index 0) reflects the originally-calculated path;
	// aux legs added by rotation use disjoint intermediates of their
	// own, but the route group only records the primary forwardHops,
	// so this is a best-effort hint the policy can use to build an
	// ExcludeHops set. Empty for a direct (0-intermediate) route.
	var sharedHops []string
	if len(rg.forwardHops) > 1 {
		sharedHops = make([]string, 0, len(rg.forwardHops)-1)
		for _, h := range rg.forwardHops[:len(rg.forwardHops)-1] {
			sharedHops = append(sharedHops, h.To.String())
		}
	}

	// The unidirectional flip state is a per-GROUP property (same for every leg);
	// snapshot it once. It tells the download-sizing controller which leg class
	// (direct vs multihop) currently carries the download, since a leg's send-side
	// direction is an assignment the flip controller can swap. Zero (false) for a
	// non-directional group.
	var flipped bool
	if rg.mux != nil {
		_, flipped = rg.mux.dirState()
	}

	legs := make([]LegInfo, 0, len(rg.tps))
	for i, tp := range rg.tps {
		l := LegInfo{Index: i, Alive: false}
		if tp != nil {
			l.Kind = string(tp.Entry.Type)
			l.TransportID = tp.Entry.ID.String()
			l.Alive = !tp.IsClosed()
			// Prefer the measured END-TO-END per-leg latency (whole route) over
			// the first-hop transport RTT — it is the leg-quality signal the
			// policy's slowest-leg eviction should judge. Falls back to the
			// transport RTT until the first liveness pong lands.
			if e2e := rg.legEndToEndLatencyMs(tp.Entry.ID); e2e > 0 {
				l.LatencyMs = int(e2e)
			} else if stats := tp.GetLatencyStats(); stats.Avg > 0 {
				l.LatencyMs = int(stats.Avg)
			}
			if bw := tp.GetBandwidth(); bw != nil {
				l.SentBytes = bw.SentBytes
				l.RecvBytes = bw.RecvBytes
			}
			if rg.mux != nil {
				l.Retransmits = rg.mux.retransmitsAt(i)
				l.Standby = rg.mux.isLegStandby(i)
				// Directional groups only: true for the forward-only DIRECT (1-hop)
				// leg, false for the multihop reverse/download legs. Always false for
				// a non-directional (symmetric) mux (dstPK/srcPK stay zero), so the
				// reverse-active floor is a no-op there.
				l.Direct = rg.mux.legIsDirectTp(tp)
				l.Flipped = flipped
			}
			l.Hops = sharedHops
		}
		legs = append(legs, l)
	}
	// Prune per-leg latency EWMA entries for transports no longer in the group
	// (rotation retires transport IDs that never return), keeping the map
	// bounded to the live legs. Caller holds rg.mu; rg.mu → legLivenessMu order.
	rg.legLivenessMu.Lock()
	if len(rg.legE2ELatency) > len(rg.tps) {
		live := make(map[uuid.UUID]struct{}, len(rg.tps))
		for _, tp := range rg.tps {
			if tp != nil {
				live[tp.Entry.ID] = struct{}{}
			}
		}
		for id := range rg.legE2ELatency {
			if _, ok := live[id]; !ok {
				delete(rg.legE2ELatency, id)
				delete(rg.legOWD, id)
				delete(rg.legRTTWin, id)
			}
		}
	}
	rg.legLivenessMu.Unlock()
	return legs
}

// legEndToEndLatencyMs returns the EWMA-smoothed end-to-end round-trip latency
// for the leg on transport tpID (0 if none measured yet). Takes legLivenessMu;
// callers may hold rg.mu (the rg.mu → legLivenessMu order is respected).
func (rg *RouteGroup) legEndToEndLatencyMs(tpID uuid.UUID) float64 {
	rg.legLivenessMu.Lock()
	defer rg.legLivenessMu.Unlock()
	return rg.legE2ELatency[tpID]
}

// legBandLatencyMs is the latency the latency-band logic reads for a leg: the
// measured end-to-end EWMA when available, else the first-hop transport RTT as a
// fallback (the SAME preference the mux-info/visor-state build uses, see the
// LegInfo.LatencyMs assignment). Without the fallback, enforceLatencyBand read a
// raw legE2ELatency of 0 for any leg whose liveness pong had not yet landed (aux
// legs frequently) — so it dropped below bandMinLegs and never parked an
// out-of-band leg, even though visor-state showed a populated latency via the
// fallback. Returns 0 only when neither signal exists (leave the leg untouched).
func (rg *RouteGroup) legBandLatencyMs(tp *transport.ManagedTransport) float64 {
	if tp == nil {
		return 0
	}
	if e2e := rg.legEndToEndLatencyMs(tp.Entry.ID); e2e > 0 {
		return e2e
	}
	if stats := tp.GetLatencyStats(); stats.Avg > 0 {
		return stats.Avg
	}
	return 0
}

// legBandMinLatencyMs is the LOAD-ROBUST latency the latency band judges a leg
// on: the minimum raw end-to-end sample over the last legRTTMinWindow. It
// returns windowed=true only when the window actually holds a sample; otherwise
// it falls back to legBandLatencyMs (the EWMA, else the first-hop RTT) so a leg
// whose pongs have not landed yet is treated exactly as before.
//
// Queuing delay only ever ADDS to a path's latency, so the window minimum is the
// leg's real latency with the load stripped out. The latest sample is not: on a
// busy leg the in-band pong queues behind bulk data and reads 37 → 136 → 771 →
// 955 → 435 ms within one transfer (measured live 2026-09-16), which put the leg
// out of band and back in on alternate ticks — and with the 30s minimum park
// hold (#4968) that is a park/promote every 30s for the whole transfer.
func (rg *RouteGroup) legBandMinLatencyMs(tp *transport.ManagedTransport) (ms float64, windowed bool) {
	if tp == nil {
		return 0, false
	}
	rg.legLivenessMu.Lock()
	minMs := rg.legRTTWin[tp.Entry.ID].minMs(time.Now())
	rg.legLivenessMu.Unlock()
	if minMs > 0 {
		return minMs, true
	}
	return rg.legBandLatencyMs(tp), false
}

// bandStatLabel renders the statistic a band decision was taken on, for the leg
// event's reason string — so an operator reading the event can tell a park taken
// on a windowed minimum from one taken on the fallback single sample.
func bandStatLabel(ms float64, windowed bool) string {
	if windowed {
		return fmt.Sprintf("min-RTT %.0f ms over %s", ms, legRTTMinWindow)
	}
	return fmt.Sprintf("%.0f ms", ms)
}

// fireLegChange calls the policy's OnLegChange hook (if any) and
// applies the returned DistributionConfig. Must NOT be called
// while holding rg.mu — applyDistribution acquires it.
//
// Called from leg-mutation paths: appendForwardLeg, appendRules
// (added events), and the leg-prune path inside the read loop
// (dropped events).
func (rg *RouteGroup) fireLegChange(event string, legIdx int) {
	hook := rg.legChangeHook
	info := rg.legChangeInfo
	if hook == nil {
		return
	}
	rg.mu.Lock()
	legs := rg.snapshotLegs()
	rg.mu.Unlock()
	change := LegChange{Event: event, LegIndex: legIdx}
	cfg := hook.OnLegChange(info, legs, change)
	if cfg.Mode == DistributionUnset {
		return
	}
	if rg.logger != nil {
		rg.logger.
			WithField("event", event).
			WithField("leg_index", legIdx).
			WithField("new_distribution", cfg.Mode.String()).
			Debug("Routing policy on_leg_change reconfigured distribution.")
	}
	rg.applyDistribution(cfg)
}

// applyDistribution configures the route group's transport
// selector from a DistributionConfig (typically supplied by a
// routing-policy script via DialAdjustment.Distribution). No-op
// when mux is unset or Mode is DistributionUnset.
//
// Idempotent — calling with the same config is cheap. Called
// after establishMuxRoutes so the distribution applies to every
// leg, including ones added by the mux loop.
func (rg *RouteGroup) applyDistribution(cfg DistributionConfig) {
	if cfg.Mode == DistributionUnset {
		return
	}
	if rg.mux == nil || rg.mux.tpSelector == nil {
		// Policy asked for a distribution but the route group is
		// single-leg (peer didn't negotiate CapMux, or mux loop
		// failed). Distribution is meaningless without multiple
		// legs; log at debug so operators can correlate "I set
		// a distribution and nothing happened" with the actual
		// reason.
		if rg.logger != nil {
			rg.logger.
				WithField("distribution", cfg.Mode.String()).
				Debug("Route group distribution skipped: not mux-enabled (single leg).")
		}
		return
	}
	var wm WeightMode
	switch cfg.Mode {
	case DistributionRoundRobin:
		wm = WeightModeEqual
	case DistributionAuto:
		// "auto" now resolves to ECF (completion-aware) rather than the old
		// latency-weighted schedule: latency-weighting over-assigns a low-latency
		// but low-bandwidth leg, which lags and stalls the no-skip reorder
		// frontier, collapsing the mux below single-leg rate. ECF only spills once
		// the fast leg is saturated, so multi-leg is >= single-leg and aggregates
		// as the fast leg fills. WeightModeAuto remains available for explicit use.
		wm = WeightModeECF
	case DistributionWeighted:
		wm = WeightModeExplicit
		rg.mux.tpSelector.SetExplicitWeights(cfg.Weights)
	case DistributionSizeThreshold:
		wm = WeightModeSizeThreshold
		rg.mux.tpSelector.SetSizeThreshold(cfg.SizeThreshold)
	case DistributionSticky5Tuple:
		wm = WeightModeSticky5Tuple
	case DistributionLatencyAdaptive:
		wm = WeightModeLatencyAdaptive
	case DistributionCapacity:
		wm = WeightModeCapacity
	case DistributionECF:
		wm = WeightModeECF
	case DistributionOTIAS:
		wm = WeightModeOTIAS
	case DistributionSTMS:
		wm = WeightModeSTMS
	case DistributionDSCPPriority:
		wm = WeightModeDSCPPriority
		rg.mux.tpSelector.SetDSCPThreshold(cfg.DSCPThreshold)
	default:
		return
	}
	rg.mu.Lock()
	rg.mux.tpSelector.SetMode(wm)
	rg.mux.tpSelector.Rebuild(rg.tps)
	legs := len(rg.tps)
	rg.mu.Unlock()
	// Weighted mode silently substitutes weight=1 for missing
	// entries when len(weights) < legs and ignores trailing
	// weights when len(weights) > legs. Surface the mismatch
	// at warn so operators can correlate "my schedule isn't
	// what I asked for" with a script/leg-count discrepancy.
	if rg.logger != nil && cfg.Mode == DistributionWeighted && len(cfg.Weights) != legs {
		rg.logger.
			WithField("weights_len", len(cfg.Weights)).
			WithField("legs", legs).
			Warn("Routing policy weighted distribution: weight count != leg count. " +
				"Missing weights default to 1; trailing weights are ignored.")
	}
	if rg.logger != nil {
		entry := rg.logger.
			WithField("distribution", cfg.Mode.String()).
			WithField("legs", legs)
		if cfg.Mode == DistributionWeighted {
			entry = entry.WithField("weights", cfg.Weights)
		}
		if cfg.Mode == DistributionSizeThreshold {
			entry = entry.WithField("size_threshold", cfg.SizeThreshold)
		}
		entry.Debug("Route group distribution applied.")
	}
}

// nextTransport selects the next transport/rule pair. When mux is enabled and
// multiple transports exist, uses latency-weighted selection. Falls back to
// round-robin when latency data is unavailable, and to index 0 for single
// transport (legacy behavior).
//
// payload is the upcoming packet's payload bytes (or nil for
// control / retx paths). Used by payload-inspecting modes
// (size-threshold, sticky:5tuple, latency-adaptive,
// dscp-priority); other modes ignore it.
//
// Returns the leg index (position in rg.tps) so the caller can record
// per-leg byte counts after a successful write; -1 for the
// single-transport / legacy path.
// NOTE: not thread-safe, caller must hold rg.mu.
func (rg *RouteGroup) nextTransport(payload []byte) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(rg.tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(rg.fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}
	if len(rg.tps) != len(rg.fwd) {
		return nil, nil, -1, ErrRuleTransportMismatch
	}

	if rg.mux != nil && len(rg.tps) > 1 {
		return rg.mux.selectTransport(rg.tps, rg.fwd, payload)
	}

	if rg.tps[0] == nil {
		return nil, nil, -1, ErrBadTransport
	}
	return rg.tps[0], rg.fwd[0], 0, nil
}

// nextFastestTransport is nextTransport's retransmit-path variant: it routes to
// the lowest-latency live leg (see routeMux.selectFastestTransport) so a
// head-of-line-blocking gap is healed on a fast leg, not re-sent down the slow
// leg that stalled it. Single-leg groups behave identically to nextTransport.
//
// The one exception is a FORWARD-confined direction: its retransmits go back on
// the confined leg, like its data. A retransmit that leaves on another leg is
// the same split the confinement exists to stop — it puts the very sequence the
// peer's no-skip frontier is waiting on behind a different leg's queue, and it
// is why rows that looked confined on the data path still showed a fifth of
// their bytes on the wrong leg.
func (rg *RouteGroup) nextFastestTransport() (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(rg.tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(rg.fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}
	if len(rg.tps) != len(rg.fwd) {
		return nil, nil, -1, ErrRuleTransportMismatch
	}
	if rg.mux != nil && len(rg.tps) > 1 {
		if directional, wantDirect, dstPK, srcPK := rg.mux.dirConfig(); directional && rg.mux.forwardSender() {
			if tp, rule, idx, ok := rg.mux.selectConfinedForward(rg.tps, rg.fwd, wantDirect, dstPK, srcPK); ok {
				return tp, rule, idx, nil
			}
		}
		return rg.mux.selectFastestTransport(rg.tps, rg.fwd)
	}
	if rg.tps[0] == nil {
		return nil, nil, -1, ErrBadTransport
	}
	return rg.tps[0], rg.fwd[0], 0, nil
}

// nextRetxTransport is nextFastestTransport for the RETRANSMIT path: the same
// pick, minus the leg the sequence was last sent on. Caller holds rg.mu.
func (rg *RouteGroup) nextRetxTransport(avoid uuid.UUID) (*transport.ManagedTransport, routing.Rule, int, error) {
	if avoid == uuid.Nil || rg.mux == nil || len(rg.tps) < 2 {
		return rg.nextFastestTransport()
	}
	if len(rg.fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}
	if len(rg.tps) != len(rg.fwd) {
		return nil, nil, -1, ErrRuleTransportMismatch
	}
	// Directional confinement pins the forward direction to one leg by design;
	// a retransmit must not break out of it.
	if directional, wantDirect, dstPK, srcPK := rg.mux.dirConfig(); directional && rg.mux.forwardSender() {
		if tp, rule, idx, ok := rg.mux.selectConfinedForward(rg.tps, rg.fwd, wantDirect, dstPK, srcPK); ok {
			return tp, rule, idx, nil
		}
	}
	return rg.mux.selectRetxTransport(rg.tps, rg.fwd, avoid)
}

// dropLegsByIndex closes the transports at the policy-supplied
// indices (re-mapped to current live legs) then triggers a prune
// so the slice compacts and OnLegChange fires. Bounded: never
// drops the last alive leg — the prune itself enforces that
// invariant, but we skip the close too so we don't briefly
// orphan a route group with no transports.
//
// Indices come from the policy's view of the leg slice at tick
// time; the snapshot the policy saw might be stale by the time
// we mutate (concurrent prune from keep-alive loop), so we treat
// out-of-range indices as no-ops rather than errors.
func (rg *RouteGroup) dropLegsByIndex(indices []int) {
	rg.mu.Lock()
	aliveCount := 0
	for _, tp := range rg.tps {
		if tp != nil && !tp.IsClosed() {
			aliveCount++
		}
	}
	closed := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx < 0 || idx >= len(rg.tps) {
			continue
		}
		tp := rg.tps[idx]
		if tp == nil || tp.IsClosed() {
			continue
		}
		if aliveCount <= 1 {
			// Refuse to drop the last alive leg — pruneDeadTransports
			// also refuses, but skipping the close avoids the brief
			// window where the leg is closed but not yet pruned.
			break
		}
		rg.noteLegEvent(MuxEventLegDropped, "policy rotation: on_tick asked to drop this leg", MuxByPolicy,
			idx, len(rg.tps), tp, rg.legHopsLocked(tp.Entry.ID))
		_ = tp.Close() //nolint:errcheck
		aliveCount--
		closed = append(closed, idx)
	}
	droppedIdx := rg.pruneDeadTransports()
	rg.mu.Unlock()
	for _, idx := range droppedIdx {
		rg.fireLegChange("dropped", idx)
	}
	if len(droppedIdx) > 0 {
		rg.signalRotate()
		rg.maybeSelfHeal()
	}
	if rg.logger != nil && len(closed) > 0 {
		rg.logger.
			WithField("dropped_indices", closed).
			Debug("Rotation hook dropped legs.")
	}
}

// selectDataStalledLegs picks the ACTIVE legs to fast-prune: while the receiver
// is stuck on a reorder gap (gapStuck) and the group as a whole is moving data,
// any active leg delivering less than 1/16 of the FASTEST active leg's bytes is
// black-holing its share (dropped to a trickle) rather than merely being a
// slower path — the fragile (e.g. webrtc-under-load) leg that taxes the whole
// mux via SACK retransmits. It never selects so many that fewer than one active
// leg would remain. Pure (no rg state / locks) so it is unit-tested directly.
// soleLegBlackHoled reports whether a group's ONLY active leg is a data
// black-hole: a request was sent (cumulative sent past the floor) but no payload
// ever came back (cumulative unique payload delivered is zero). Pure so it is
// unit-tested directly; the caller applies the consecutive-interval hysteresis
// and the dial.
func soleLegBlackHoled(activeCnt int, sent, payload uint64) bool {
	return activeCnt == 1 && sent > uint64(routersettings.LegSoleBlackHoleSentFloor.Bytes()) && payload == 0 //nolint:gosec
}

// stalledLegAction decides what happens to data-stalled legs.
//
// An IDLE leg is not a STALLED leg. This detector runs on the RECEIVE side and
// judges a leg by the payload IT delivered, but the receiver does not choose
// which leg carries what — the PEER does. A leg the peer simply did not stripe
// onto reads exactly like a leg that swallowed its share, and the ONE signal
// that separates them is the reorder frontier: a leg that dropped assigned
// sequences leaves a gap the receiver is stuck on. So a PARK requires gapStuck.
// Without a gap the group is losing nothing and a park is pure damage — it
// mirrors to the peer over CapLegState and the peer's own resync then keeps the
// leg out of the set for the traffic that follows.
//
// With the frontier STUCK the pre-existing behavior stands: park rather than
// remove, because removing a leg the receiver is HoL-blocked behind orphans its
// in-flight sequences (the retransmits land on a torn-down rule) and turns a
// transient stall into a permanent wedge. Removal stays reserved for a genuine
// goodput black-hole caught in ADAPTIVE mode with a HEALTHY frontier, where
// there is no critical in-flight to orphan and churning the leg is the intent.
func stalledLegAction(manual, gapStuck bool) legStallAction {
	switch {
	case gapStuck:
		return legStallPark
	case manual:
		return legStallIgnore // pinned set, healthy frontier: nothing to act on
	default:
		return legStallRemove
	}
}

func selectDataStalledLegs(legs []legRecvDelta, gapStuck bool) []uuid.UUID {
	return selectDataStalledLegsWith(nil, legs, gapStuck)
}

// selectDataStalledLegsWith is selectDataStalledLegs resolved against one route
// group's knob view; the pure form above reads the visor-wide values.
func selectDataStalledLegsWith(kn *routersettings.View, legs []legRecvDelta, gapStuck bool) []uuid.UUID {
	var agg, top uint64
	active := 0
	for _, l := range legs {
		agg += l.delta
		if l.standby {
			continue
		}
		active++
		if l.delta > top {
			top = l.delta
		}
	}
	// Need the group to be moving data, at least two active legs (never risk the
	// last one on a heuristic), and a real leader to measure the laggards against.
	if agg == 0 || active < 2 || top == 0 {
		return nil
	}
	// Pick the laggard threshold by how the group is doing:
	//   - Frontier STUCK: the receiver is stalled on a missing seq, so shed any
	//     leg well below the leader (< top/16) to recover the survivors' rate.
	//   - Frontier HEALTHY: only shed a PURE goodput black-hole — a leg delivering
	//     < 1/64 of a clearly-moving leader. It is dead weight (normal latency, so
	//     the band demotion misses it) that still draws its round-robin share under
	//     an even spread and burns retransmits; drop it without waiting for a full
	//     stall. Gated on a substantial leader so top/64 is a real floor.
	var threshold uint64
	if gapStuck {
		threshold = top / 16
	} else {
		if top < uint64(kn.Bytes(routersettings.LegBlackHoleMinTopBytes)) { //nolint:gosec
			return nil
		}
		threshold = top / 64
	}
	var dead []uuid.UUID
	for _, l := range legs {
		if l.standby || l.delta > threshold {
			continue
		}
		dead = append(dead, l.id)
	}
	// Keep at least one active leg even if several trickle at once.
	if drop := len(dead); drop > 0 && active-drop < 1 {
		dead = dead[:active-1]
	}
	return dead
}

// fastClusterAnchor returns the anchor latency of the FAST cluster in a sorted
// (ascending) latency slice: the fastest latency whose band window
// [anchor, demoteRatio*anchor] contains the most legs. The active stripe set is
// held within [anchor, demoteRatio*anchor].
//
// Why not the median: with a small, slow-skewed active set (e.g. 185, 257, 456,
// 809 ms) the median lands HIGH (456), so the slow legs look "in band" (<2x the
// median) while the genuinely-fast 185 ms leg looks like the outlier — the band
// then keeps the slow legs and HoL-throttles the aggregate BELOW a single fast
// leg (the measured A1 failure). Anchoring on the fast cluster keeps {185, 257}
// active and parks {456, 809}. Isolated LAN short-circuit artifacts (a 2 ms leg
// beside a 250 ms cluster) are already kept out of the active set at mux-set time
// (#4253); the fastest-window-on-tie rule here is chosen for the slow-skew case,
// so a lone artifact anchors a 1-leg window and loses to the real cluster.
func fastClusterAnchor(known []float64, demoteRatio float64) float64 {
	anchor, bestCount := 0.0, -1
	for _, a := range known {
		if a <= 0 {
			continue
		}
		cnt := 0
		for _, l := range known {
			if l >= a && l <= a*demoteRatio {
				cnt++
			}
		}
		if cnt > bestCount {
			bestCount, anchor = cnt, a
		}
	}
	return anchor
}

// partitionLatencyBand decides which legs to demote to / promote from warm
// standby so the ACTIVE stripe set stays within a tight latency band anchored on
// the FAST cluster (see fastClusterAnchor). Striping across latency-disparate
// legs opens a reorder gap the size of the difference (a 2ms LAN short-circuit
// beside a 300ms route), HoL-capping or stalling the no-skip reorder buffer — the
// multi-leg collapse. A leg outside [anchor, demoteRatio*anchor] — too slow
// (high-side stall) OR faster than the fast cluster's floor (a LAN short-circuit
// artifact) — is demoted to warm standby in every mode. Only re-PROMOTION stays
// gated to manualMode: in adaptive mode the tick owns re-admission and its
// 512-deep warm pool must not be dumped into the active set (demotion to standby
// never dumps the pool, so it is safe to run always). Pure (no locks / rg state)
// so it is unit-tested directly. Never demotes the primary or the last active leg.
func partitionLatencyBand(legs []bandLeg, manualMode, tight bool) (demote, promote []int) {
	return partitionLatencyBandWith(nil, legs, manualMode, tight)
}

// partitionLatencyBandWith is partitionLatencyBand resolved against one route
// group's knob view; the pure form above reads the visor-wide values.
func partitionLatencyBandWith(kn *routersettings.View, legs []bandLeg, manualMode, tight bool) (demote, promote []int) {
	demoteRatio, admitRatio := kn.Ratio(routersettings.BandDemoteRatio), kn.Ratio(routersettings.BandAdmitRatio)
	if tight {
		demoteRatio, admitRatio = kn.Ratio(routersettings.BandDemoteRatioTight), kn.Ratio(routersettings.BandAdmitRatioTight)
	}
	known := make([]float64, 0, len(legs))
	for _, l := range legs {
		if l.latMs > 0 {
			known = append(known, l.latMs)
		}
	}
	if len(known) < kn.Int(routersettings.BandMinLegs) {
		return nil, nil
	}
	sort.Float64s(known)
	anchor := fastClusterAnchor(known, demoteRatio)
	if anchor <= 0 {
		return nil, nil
	}
	demoteHi := demoteRatio * anchor // slower than this → high-side stall demote
	admitHi := admitRatio * anchor   // re-admit only within this tighter band

	// GOODPUT GATE (BBR/bufferbloat): the leg latency above is measured by an
	// IN-BAND liveness pong that shares the leg's FIFO send queue with bulk data,
	// so UNDER LOAD it reads base_RTT + self-inflicted queuing delay — the BUSIEST
	// leg reads the HIGHEST latency. Demoting an ACTIVE leg on that would shed the
	// best-utilized leg. So an active leg is demoted on latency ONLY IF it is also
	// LOW-goodput: a leg delivering its share is working (its high latency is its
	// own queue), keep it; a leg out-of-band AND barely delivering is genuinely
	// bad (idle-slow route, or HoL-stalling the frontier), park it. maxDelta is the
	// best active leg's recent delivered bytes; the gate self-disables when the
	// group is IDLE (maxDelta==0) — there the pong isn't queued behind data, so
	// latency is meaningful and the plain latency band applies. This also makes the
	// gate starvation-safe (BBR's application-limited rule): a leg the scheduler
	// starved reads LOW idle latency (good route, idle), so it is in-band and never
	// a demote candidate to begin with.
	var maxDelta uint64
	for _, l := range legs {
		if !l.standby && l.recvDelta > maxDelta {
			maxDelta = l.recvDelta
		}
	}

	active := 0
	for _, l := range legs {
		if !l.standby {
			active++
		}
	}

	for _, l := range legs {
		if l.latMs <= 0 {
			continue // unknown latency — leave the leg's state untouched
		}
		inBand := l.latMs >= anchor && l.latMs <= demoteHi
		// Under load, spare a busy (>= goodputGateFrac of the best) active leg from
		// latency demotion — its latency is self-queuing, not route quality.
		busy := maxDelta > 0 && float64(l.recvDelta) >= kn.Ratio(routersettings.BandGoodputGateFrac)*float64(maxDelta)
		switch {
		case !l.standby && !l.primary && active > 1 && !inBand && !busy:
			demote = append(demote, l.idx)
			active-- // never demote below one active leg
		case manualMode && l.standby && l.latMs >= anchor && l.latMs <= admitHi:
			promote = append(promote, l.idx)
			active++
		}
	}
	return demote, promote
}

// pickPrimaryReelection decides whether the PRIMARY slot (leg 0) should be
// re-elected onto a healthier leg. The primary is the always-ready, always-
// selectable anchor, so it is exempt from band demotion (partitionLatencyBand
// never demotes it) — but that means a primary that is itself a gross latency
// outlier (a 2ms LAN short-circuit artifact, or a self-healed 14000ms leg that
// landed at index 0) permanently anchors the active set off-band and wastes an
// active slot. This picker names a replacement so the primary can CHANGE
// dynamically instead of being protected in place: when leg 0 is outside the
// fast-cluster band [anchor, demoteRatio*anchor] AND a non-primary ACTIVE leg
// sits inside it, it returns that leg's index — the lowest-latency in-band active
// candidate, which becomes the new primary via a make-before-break swap
// (reelectPrimary). It returns ok=false unless a replacement exists, so the group
// never gives up its anchor without one. Anchoring on the fast cluster (not the
// median) matters here too: a slow-skewed set inflates the median so a genuinely-
// fast primary looks in-band, or a slow primary looks in-band — the fast-cluster
// anchor names the real fast leg to promote into the primary slot. Pure (no locks
// / rg state) so it is unit-tested directly.
func pickPrimaryReelection(legs []bandLeg, tight bool) (newPrimaryIdx int, ok bool) {
	return pickPrimaryReelectionWith(nil, legs, tight)
}

// pickPrimaryReelectionWith is pickPrimaryReelection resolved against one route
// group's knob view; the pure form above reads the visor-wide values.
func pickPrimaryReelectionWith(kn *routersettings.View, legs []bandLeg, tight bool) (newPrimaryIdx int, ok bool) {
	demoteRatio, admitRatio := kn.Ratio(routersettings.BandDemoteRatio), kn.Ratio(routersettings.BandAdmitRatio)
	if tight {
		demoteRatio, admitRatio = kn.Ratio(routersettings.BandDemoteRatioTight), kn.Ratio(routersettings.BandAdmitRatioTight)
	}
	known := make([]float64, 0, len(legs))
	var primary *bandLeg
	for i := range legs {
		if legs[i].primary {
			primary = &legs[i]
		}
		if legs[i].latMs > 0 {
			known = append(known, legs[i].latMs)
		}
	}
	if primary == nil || primary.latMs <= 0 || len(known) < kn.Int(routersettings.BandMinLegs) {
		return 0, false
	}
	sort.Float64s(known)
	anchor := fastClusterAnchor(known, demoteRatio)
	if anchor <= 0 {
		return 0, false
	}
	// Is the primary itself outside the fast-cluster band?
	if primary.latMs >= anchor && primary.latMs <= demoteRatio*anchor {
		return 0, false // primary is in the fast band — leave it
	}
	// Find the best (lowest-latency) in-band ACTIVE non-primary replacement.
	admitHi := admitRatio * anchor
	bestIdx, bestLat := -1, 0.0
	for i := range legs {
		l := legs[i]
		if l.primary || l.standby || l.latMs <= 0 {
			continue
		}
		if l.latMs < anchor || l.latMs > admitHi {
			continue // candidate must itself be in the fast band
		}
		if bestIdx == -1 || l.latMs < bestLat {
			bestIdx, bestLat = l.idx, l.latMs
		}
	}
	if bestIdx == -1 {
		return 0, false
	}
	return bestIdx, true
}

// reelectPrimary swaps the leg at newIdx into the primary slot (index 0),
// make-before-break: the parallel tps[]/fwd[]/rvs[] entries and the mux's per-leg
// counters/readiness/standby markers are exchanged in lockstep so each rule and
// counter stays attached to its own transport, and the route group — with the
// noise/yamux session riding it — is never torn down. The chosen leg is already
// active and carrying (pickPrimaryReelection selects only from the ready, in-band
// active set), so index 0 has a live send leg the instant the swap returns and
// the byte stream continues uninterrupted. The displaced old primary lands at
// newIdx as an ordinary (now demotable) leg, to be parked by the band pass if it
// is out of band. Caller must NOT hold rg.mu.
func (rg *RouteGroup) reelectPrimary(newIdx int) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if newIdx <= 0 || newIdx >= len(rg.tps) {
		return
	}
	rg.tps[0], rg.tps[newIdx] = rg.tps[newIdx], rg.tps[0]
	if newIdx < len(rg.fwd) {
		rg.fwd[0], rg.fwd[newIdx] = rg.fwd[newIdx], rg.fwd[0]
	}
	if newIdx < len(rg.rvs) {
		rg.rvs[0], rg.rvs[newIdx] = rg.rvs[newIdx], rg.rvs[0]
	}
	rg.mux.swapLegs(0, newIdx)
	// forwardHops records the PRIMARY leg's path (used for self-heal shared-hop
	// exclusion and mux info); re-point it at the new primary's stored hops.
	if rg.tps[0] != nil {
		if hops, okHops := rg.legForwardHops[rg.tps[0].Entry.ID]; okHops {
			rg.forwardHops = hops
		}
	}
	rg.logger.Infof("latency-band: re-elected leg %d into the primary slot (old primary was an out-of-band outlier)", newIdx)
	rg.noteLegEvent(MuxEventPrimaryRehome,
		fmt.Sprintf("latency band: leg %d re-elected, old primary was an out-of-band outlier", newIdx),
		MuxByAdaptive, 0, len(rg.tps), rg.tps[0], rg.legHopsLocked(tpEntryID(rg.tps[0])))
}

// enforceBottleneckGroups detects SHARED-BOTTLENECK groups among the mux legs
// from each leg's OWD-variation statistics (RFC 8382; see bottleneck.go), pushes
// the grouping to the mux (so rebuildWeights counts each bottleneck as ONE unit
// of capacity instead of N independent competing pipes), and parks redundant
// co-bottlenecked ACTIVE legs to warm standby (admission prefers legs from
// DISTINCT groups). Reuses samples already collected — the leg-liveness pong and
// every SACK's per-leg send→ack delay (foldLegDelaySample) — so no new probe
// traffic, and while data flows the windows fill in about a second rather than
// the ~110s the pong cadence alone took to clear sbdMinSamples, which is most of
// a transfer spent striping across a pipe that is not there. Runs on the
// data-progress cadence (5s), just before the weight
// rebuild and the latency band; the caller rebuilds the weights immediately after
// so the parks and grouping take effect the same tick. Never parks the primary or
// below one active leg per group (pickBottleneckDemotions guarantees this).
//
// This is the general-case complement to the same-LAN structural reject (#4253):
// that check only catches a co-located INTERMEDIATE at leg-creation time; this
// catches two disjoint routes that funnel through the same uplink, at runtime,
// from their shared delay-variation signature.
// foldLegDelaySample folds one per-SACK send→ack delay sample (ms) for one leg
// into that leg's shared-bottleneck window — the same window, and the same
// statistics, the liveness pong feeds (see bottleneck.go). It is the mux's
// onLegDelaySample hook, already rate-limited there to one sample per leg per
// SBDSampleInterval, so a bulk transfer's SACK rate cannot swamp the window.
//
// Both sources measure a round trip over the leg with its queueing included,
// which is the delay VARIATION signature RFC 8382 clusters on; what the SACK
// source adds is cadence. A leg with no window yet gets one, exactly as the pong
// path does, so a leg that only ever carries data is grouped too.
func (rg *RouteGroup) foldLegDelaySample(tpID uuid.UUID, ms float64) {
	if tpID == uuid.Nil || ms <= 0 || rg.isClosed() {
		return
	}
	// sbd.enabled off stops the SAMPLING as well as the rulings — the explicit
	// switch that replaces faking it with an unreachable sbd.min_samples.
	if !rg.knBool(routersettings.SBDEnabled) {
		return
	}
	rg.legLivenessMu.Lock()
	if rg.legOWD == nil {
		rg.legOWD = make(map[uuid.UUID]*sbdWindow)
	}
	w := rg.legOWD[tpID]
	if w == nil {
		w = newSBDWindow()
		rg.legOWD[tpID] = w
	}
	w.push(ms)
	rg.legLivenessMu.Unlock()
}

func (rg *RouteGroup) enforceBottleneckGroups(recvDeltas map[uuid.UUID]uint64) {
	if rg.isClosed() || rg.mux == nil || !rg.knBool(routersettings.SBDEnabled) {
		return
	}
	if rg.honorsMirrorActiveSet() {
		return // acceptor: the active set is the initiator's mirror, not ours to size
	}
	rg.mu.Lock()
	tpsCopy := append([]*transport.ManagedTransport(nil), rg.tps...)
	rg.mu.Unlock()

	// The evidence rate is measured on the SEND path — SACK-acknowledged bytes per
	// tick — because that is the side the detector's delay samples come from. The
	// recv-side deltas are the opposite end of a transfer: on a download the exit
	// SENDS every byte and receives almost none, so judging its rulings by what it
	// received read as an idle group through the whole 50 MB row, and the local end
	// (which receives everything and sends almost nothing) would have ruled on
	// samples it never took. Same quantity, same side, or no ruling.
	sendRate := sbdAggRate(rg.sbdSendDeltas(tpsCopy))

	// Read the verdict on any park this controller is still holding as a TRIAL
	// BEFORE ruling again: a leg the goodput evidence vindicates is back in the
	// active set, and its pair exempt, by the time this tick's grouping runs.
	rg.evaluateSBDTrials(sendRate, tpsCopy)

	if len(tpsCopy) < 2 {
		rg.mux.SetLegGroups(nil) // fewer than two legs — nothing to group
		return
	}

	// NO TRAFFIC, NO RULING — and, since the grouping is itself a weight decision
	// (see below), no grouping either. A shared bottleneck is a statement about a
	// queue two legs are both waiting in, and an idle group has no queue: its legs
	// co-vary only because neither is loaded. The floor is read HERE, before any
	// correlation work, so a tick that carried nothing produces no clustering, no
	// weight change and no event — the T2xL2 compose set recorded 118 sbd_ruling
	// events in 670 s, one of them on a send path delivering 6 B/s against a
	// 65536 B/s floor, because the floor used to gate only the demotion.
	floor := float64(SBDMinEvidenceRate())
	if sendRate < floor {
		rg.mux.SetLegGroups(nil)
		return
	}

	// Per-leg OWD summary statistics from the moving windows (legLivenessMu).
	stats := make([]sbdStats, len(tpsCopy))
	rg.legLivenessMu.Lock()
	for i, tp := range tpsCopy {
		if tp == nil {
			continue
		}
		if w := rg.legOWD[tp.Entry.ID]; w != nil {
			stats[i] = computeSBDStats(w.samples())
		}
	}
	rg.legLivenessMu.Unlock()

	// Pairs a failed park trial already proved independent are vetoed out of the
	// clustering for their backoff window, so the detector cannot re-park a leg
	// the measurement just paid for.
	groups := groupLegsBySBDExcept(stats, func(i, j int) bool {
		if tpsCopy[i] == nil || tpsCopy[j] == nil {
			return false
		}
		return rg.sbdSuppressed(tpsCopy[i].Entry.ID, tpsCopy[j].Entry.ID)
	})

	// THE GROUPING IS A DE-FACTO PARK, so it goes to the mux only when demotion is
	// on. rebuildWeights (route_mux.go) gives the group's AGGREGATE throughput to
	// one representative leg and ZERO send weight to every other member, which
	// takes a leg's traffic away exactly as setLegStandby would — without a park
	// event, without a trial, and without anything that could undo it. That is how
	// the T2xL2 compose set reached upload shares of 49175:100%,49176:0% with
	// sbd_demote OFF: 2 tunnels x 2 legs became 2 x 1 and the set collapsed
	// (uploads x0.06-0.12). With demotion off the detector is purely
	// OBSERVATIONAL: it records the ruling and changes nothing.
	if SBDDemote() {
		rg.mux.SetLegGroups(groups)
	} else {
		rg.mux.SetLegGroups(nil)
	}

	// Distinct-group admission: park any redundant co-bottlenecked active legs.
	legs := make([]bottleneckLeg, 0, len(tpsCopy))
	for i, tp := range tpsCopy {
		if tp == nil || tp.IsClosed() {
			continue
		}
		legs = append(legs, bottleneckLeg{
			idx:     i,
			group:   groups[i],
			standby: rg.mux.isLegStandby(i),
			primary: i == 0,
			goodput: recvDeltas[tp.Entry.ID],
			latMs:   rg.legBandLatencyMs(tp),
		})
	}
	demote := pickBottleneckDemotions(legs)
	demote = rg.keepReverseFloor(demote, recvDeltas)
	if len(demote) == 0 {
		return
	}

	// DEMOTION IS OFF BY DEFAULT (sbdDemoteDefault): the ruling is recorded, with
	// the numbers behind it, and NOTHING else happens — no leg is taken away and,
	// per the SetLegGroups branch above, no weight moves. The record is rate
	// limited to one ruling per leg PAIR per SBDBackoff window: the detector rules
	// on every 5 s tick of a transfer, and an unlimited record is 118 identical
	// events in 670 s (the T2xL2 compose set) rather than a diagnosis.
	// Turning demotion on is `route settings --sbd-demote true` on BOTH ends.
	if !SBDDemote() {
		for _, idx := range demote {
			var tp *transport.ManagedTransport
			if idx < len(tpsCopy) {
				tp = tpsCopy[idx]
			}
			keeper := sbdGroupKeeper(legs, demote, idx)
			if !rg.sbdRulingDue(sbdLegID(tpsCopy, idx), sbdLegID(tpsCopy, keeper)) {
				continue
			}
			reason := sbdRulingReason(idx, keeper, groups, stats, sendRate, floor)
			rg.logger.Debugf("%s", reason)
			rg.noteLegEvent(MuxEventSBDRuling, reason, MuxByAdaptive, idx, len(tpsCopy), tp, nil)
		}
		return
	}

	// The evidence floor was cleared at the top of this tick (nothing below it gets
	// this far), so the park below is always decided on a group that was actually
	// carrying sbdMinEvidenceRate. That is what makes it arbitrable: a park decided
	// at idle compares against a pre-park rate of zero, which can never fall, and
	// stands for the whole transfer — the measured defect in
	// bench/2026-09-16/0251e5da4-smoke/mux-legs-2, where parks at 01:06:30.615 and
	// 01:06:35.615 landed before row 1 moved a byte and all 15 rows after them ran
	// single-leg at x0.81 (50 MB) and x0.68 (10 MB) against the paired reference,
	// where the same route pair with parking off gave x1.13.
	for _, idx := range demote {
		rg.logger.Infof("shared-bottleneck: parking leg %d to warm standby on TRIAL (co-bottlenecked with a kept active leg in group %d — one pipe, not two; striping it adds only reorder cost). Aggregate goodput now %.0f B/s; if it falls more than %.0f%% within %v the park is undone",
			idx, groups[idx], sendRate, SBDTrialLoss()*100, SBDTrialWindow())
		rg.mux.setLegStandby(idx, true)
		if idx < len(tpsCopy) && tpsCopy[idx] != nil {
			// The ruling is provisional: record the pre-park aggregate rate and the
			// active leg this one was judged redundant against, so the next tick can
			// undo the park if it cost the group capacity (see sbd_trial.go).
			var keeperID uuid.UUID
			if k := sbdGroupKeeper(legs, demote, idx); k >= 0 && k < len(tpsCopy) && tpsCopy[k] != nil {
				keeperID = tpsCopy[k].Entry.ID
			}
			rg.beginSBDTrial(tpsCopy[idx].Entry.ID, keeperID, sendRate)
			rg.noteLegEvent(MuxEventLegParked, "shared bottleneck: co-bottlenecked with a kept active leg (one pipe, not two) — on trial until the aggregate goodput is re-read", MuxByAdaptive, idx, len(tpsCopy), tpsCopy[idx], nil)
			// Start the park's minimum hold so the latency band — which runs later
			// in THIS same data-progress tick and is blind to the grouping — cannot
			// re-admit the leg on the spot (the measured 5s park/promote flap).
			rg.noteAdaptivePark(tpsCopy[idx].Entry.ID, "shared bottleneck: co-bottlenecked with a kept active leg (one pipe, not two)")
		}
		// Mirror the park to the remote (CapLegState) so the bulk-sending peer
		// stops striping across this leg — without this the native park is
		// send-side-only here and the peer keeps spraying the download over it.
		rg.sendLegState(idx, true)
	}
}

// enforceLatencyBand keeps the mux's active stripe set within a tight latency
// band (see partitionLatencyBand). Runs on the data-progress cadence so it holds
// even in a manual (non-adaptive) preset, where nothing else manages the active
// set. Reads each leg's MINIMUM end-to-end latency over the last
// legRTTMinWindow (legBandMinLatencyMs — the latest sample is queue-inflated
// under load and made this controller flap), decides demotions/promotions,
// and applies them via the mux's send-side standby marker — a demoted leg gets
// zero scheduler weight and is never striped, so it can no longer open a reorder
// gap, while its rules stay installed (a warm standby, promotable again if it
// returns to the band).
func (rg *RouteGroup) enforceLatencyBand(recvDeltas map[uuid.UUID]uint64) {
	if rg.isClosed() || rg.mux == nil {
		return
	}
	if rg.honorsMirrorActiveSet() {
		return // acceptor: honor the initiator's mirrored active set; do not re-admit
	}
	rg.mu.Lock()
	manual := !rg.standbyNewLegs
	// In capacity (aggregation) mode the active legs stripe a single ordered
	// stream, so the arrivals must stay near-in-order for the no-skip reorder
	// frontier to advance; a tighter latency band keeps the held active set
	// homogeneous and stops the frontier-stall → prune → collapse spiral. In
	// ECF/failover mode the wider band is fine (only one leg carries data).
	tight := rg.mux.distributionMode() == WeightModeCapacity
	legs := make([]bandLeg, 0, len(rg.tps))
	for i, tp := range rg.tps {
		if tp == nil || tp.IsClosed() {
			continue
		}
		latMs, windowed := rg.legBandMinLatencyMs(tp)
		legs = append(legs, bandLeg{
			idx:       i,
			latMs:     latMs,
			windowed:  windowed,
			standby:   rg.mux.isLegStandby(i),
			primary:   i == 0,
			recvDelta: recvDeltas[tp.Entry.ID],
		})
	}
	rg.mu.Unlock()

	// If the primary slot itself is a gross latency outlier, re-elect a healthier
	// active leg into it (make-before-break) BEFORE the band pass. The primary is
	// exempt from demotion, so without this a bad leg that lands at index 0 (a
	// LAN-artifact or a self-healed multi-second leg) anchors the active set off-
	// band forever. After the swap the leg indices have changed, so rebuild the
	// list; the displaced old primary is now an ordinary leg the band pass can
	// park.
	if newPrimary, ok := pickPrimaryReelectionWith(rg.knobs(), legs, tight); ok {
		rg.reelectPrimary(newPrimary)
		rg.mu.Lock()
		legs = legs[:0]
		for i, tp := range rg.tps {
			if tp == nil || tp.IsClosed() {
				continue
			}
			latMs, windowed := rg.legBandMinLatencyMs(tp)
			legs = append(legs, bandLeg{
				idx:       i,
				latMs:     latMs,
				windowed:  windowed,
				standby:   rg.mux.isLegStandby(i),
				primary:   i == 0,
				recvDelta: recvDeltas[tp.Entry.ID],
			})
		}
		rg.mu.Unlock()
	}

	demote, promote := partitionLatencyBandWith(rg.knobs(), legs, manual, tight)
	demote = rg.keepReverseFloor(demote, recvDeltas)
	// Per-leg diagnostics: the rendered statistic each decision was taken on, so
	// the log line and the leg event name the number AND what it is.
	statOf := make(map[int]string, len(legs))
	for _, l := range legs {
		statOf[l.idx] = bandStatLabel(l.latMs, l.windowed)
	}
	if len(demote) > 0 || len(promote) > 0 {
		mode := "adaptive"
		if manual {
			mode = "manual"
		}
		band := "wide"
		if tight {
			band = "tight"
		}
		rg.logger.Infof("latency-band[%s/%s]: %d active legs, demote=%v promote=%v", mode, band, len(legs), demote, promote)
	}
	for _, idx := range demote {
		rg.logger.Infof("latency-band: parking leg %d to warm standby (%s out-of-band, keeping the active stripe set homogeneous)", idx, statOf[idx])
		rg.mux.setLegStandby(idx, true)
		rg.mu.Lock()
		var bandTp *transport.ManagedTransport
		if idx < len(rg.tps) {
			bandTp = rg.tps[idx]
		}
		bandLegs := len(rg.tps)
		rg.mu.Unlock()
		if bandTp != nil {
			reason := fmt.Sprintf("latency band: %s is out of the active set's band", statOf[idx])
			rg.noteLegEvent(MuxEventLegParked, reason, MuxByAdaptive, idx, bandLegs, bandTp, nil)
			rg.noteAdaptivePark(bandTp.Entry.ID, reason)
		}
		// Mirror to the remote so the bulk sender stops striping across this leg
		// (CapLegState) — the native park is otherwise send-side-only here.
		rg.sendLegState(idx, true)
	}
	for _, idx := range promote {
		// Through the hysteresis seam: a leg another controller just parked (say
		// as co-bottlenecked) is still perfectly in-band, so without the hold the
		// band re-admits it microseconds after the park and the two controllers
		// flap the leg once per tick.
		if !rg.promoteLegAdaptive(idx, fmt.Sprintf("latency band: %s back within the active set's band", statOf[idx])) {
			continue
		}
		rg.logger.Infof("latency-band: re-admitting leg %d to active (%s back within band)", idx, statOf[idx])
	}
	if len(demote) > 0 || len(promote) > 0 {
		rg.mu.Lock()
		if rg.mux != nil {
			rg.mux.rebuildWeights(rg.tps)
		}
		rg.mu.Unlock()
	}
}

// keepReverseFloor filters a park-controller's demote list so it never strands
// the DOWNLOAD floor — at least one active reverse (download-class) leg must
// survive. The homogeneity controllers (shared-bottleneck, latency-band) are
// direction-BLIND: they keep the primary (the DIRECT/upload leg when directional)
// and one leg per bottleneck group, but nothing guarantees a surviving
// download-class leg. Parking every download leg leaves reverse_active=0, so the
// bulk sender (the exit, on a download) has no mirrored active set to confine to
// (CapLegState) and sprays download across warm-standby legs — over-subscribing
// the no-skip reorder frontier until it wedges and the group collapses (measured:
// ~99% of a download riding standby legs, ~55x over-subscription).
//
// When the demotions would zero the active download set this keeps the
// highest-goodput download leg active (floor = 1, the adaptRevActive default;
// higher floors are the preset tick's DemoteToStandby budget, which pkg/router
// cannot read without an import cycle). Download-class is flip-aware
// (Direct == flipped). Non-directional groups have no forward/reverse split and
// are returned unchanged. Stalled-leg parking (leg-dataprogress) is deliberately
// NOT guarded — a dead sole reverse leg must still be parked/pruned.
func (rg *RouteGroup) keepReverseFloor(demote []int, recvDeltas map[uuid.UUID]uint64) []int {
	if len(demote) == 0 || rg.mux == nil {
		return demote
	}
	directional, flipped := rg.mux.dirState()
	if !directional {
		return demote
	}
	rg.mu.Lock()
	tps := append([]*transport.ManagedTransport(nil), rg.tps...)
	rg.mu.Unlock()

	// Collect the active (non-standby) download-class legs with their goodput.
	var activeDL []legGoodput
	for i, tp := range tps {
		if tp == nil || tp.IsClosed() || rg.mux.isLegStandby(i) {
			continue
		}
		if rg.mux.legIsDirectTp(tp) != flipped { // not a download-class leg
			continue
		}
		activeDL = append(activeDL, legGoodput{idx: i, gp: recvDeltas[tp.Entry.ID]})
	}
	demoteSet := make(map[int]bool, len(demote))
	for _, d := range demote {
		demoteSet[d] = true
	}
	rescue := reverseFloorRescue(activeDL, demoteSet)
	if rescue < 0 {
		return demote // floor already met, or no active download leg is being parked
	}
	out := make([]int, 0, len(demote))
	for _, d := range demote {
		if d != rescue {
			out = append(out, d)
		}
	}
	rg.logger.Infof("reverse-floor: keeping active download leg %d active (parking it would strand reverse_active=0; the bulk sender needs an active reverse set to confine to, else it sprays download across standby)", rescue)
	return out
}

// reverseFloorRescue is the pure decision behind keepReverseFloor: given the
// currently-active download-class legs (activeDL) and which leg indices a park
// pass wants to demote (demoteSet), it returns the index of the best-goodput leg
// to KEEP active so the pass never strands reverse_active=0 — or -1 when at least
// one active download leg already survives the demotions (floor already met) or
// none of them is being demoted (nothing to rescue).
func reverseFloorRescue(activeDL []legGoodput, demoteSet map[int]bool) int {
	survivors := 0
	bestIdx, bestGP := -1, uint64(0)
	for _, l := range activeDL {
		if !demoteSet[l.idx] {
			survivors++
			continue
		}
		if bestIdx < 0 || l.gp > bestGP {
			bestIdx, bestGP = l.idx, l.gp
		}
	}
	if survivors >= 1 {
		return -1
	}
	return bestIdx
}

// pruneLivenessDeadLegs drops the given black-holing legs (by transport ID) from
// the mux WITHOUT closing their (possibly shared) transports, deletes their
// local rules, compacts the mux, then triggers self-heal. It never drops below
// one leg, so a false positive at worst causes a self-heal re-dial — never an
// outage. Mirrors pruneDeadTransports' removal, selecting by liveness instead
// of tp.IsClosed().
// demoteStalledLegs parks the given legs (by transport ID) to WARM STANDBY
// instead of removing them — the manual-mode counterpart to pruneLivenessDeadLegs.
// A standby leg keeps its rules installed and its transport up and still RECEIVES
// (standby is a send-side exclusion only), so any sequences the sender already
// assigned to it keep arriving and draining the no-skip reorder frontier — no
// orphaned gap, no wedge. The primary anchor (index 0) is never parked (a group
// must always have a selectable send leg); if the primary itself is the stalled
// one, enforceLatencyBand's re-election moves a healthy leg into slot 0 on the
// next cadence. The parked legs stay in the group, re-promotable by the band pass
// once their goodput recovers, so a manually-pinned set is preserved across a
// transient stall instead of being torn down.
func (rg *RouteGroup) demoteStalledLegs(deadIDs []uuid.UUID) {
	deadSet := make(map[uuid.UUID]struct{}, len(deadIDs))
	for _, id := range deadIDs {
		deadSet[id] = struct{}{}
	}
	if rg.honorsMirrorActiveSet() {
		// ACCEPTOR: the active set is the initiator's mirror, not ours to size —
		// the same rule enforceBottleneckGroups and enforceLatencyBand already
		// obey. Parking here is worse than re-admitting, because the park is
		// mirrored BACK over CapLegState (sendLegState below) and the initiator
		// adopts it: measured on a composed 2x2 run, seven leg_parked events all
		// arrived as by=peer mirrors, four of them mid-set on one 201.8 ms leg,
		// and a 15 s all-paths blackout sat inside one such park window. Report
		// the stall so it stays attributable from `visor state`, and leave the
		// decision to the initiator, whose own stalled-leg policy is unchanged.
		rg.reportStalledLegs(deadSet)
		return
	}
	rg.mu.Lock()
	var idxs []int
	var parkedIDs []uuid.UUID
	for i, tp := range rg.tps {
		if i == 0 || tp == nil {
			continue // never park the primary anchor
		}
		if _, dead := deadSet[tp.Entry.ID]; dead {
			idxs = append(idxs, i)
			rg.noteLegEvent(MuxEventLegParked, "data progress stalled with an open reorder gap (parked to standby, rules kept)",
				MuxByAdaptive, i, len(rg.tps), tp, rg.legHopsLocked(tp.Entry.ID))
			parkedIDs = append(parkedIDs, tp.Entry.ID)
		}
	}
	rg.mu.Unlock()
	for _, id := range parkedIDs {
		// Hold this park too: a stalled leg that is otherwise in-band must not be
		// re-admitted by the latency band on the very next tick.
		rg.noteAdaptivePark(id, "data progress stalled with an open reorder gap (parked to standby, rules kept)")
	}
	for _, i := range idxs {
		rg.mux.setLegStandby(i, true)
		// Mirror to the remote so it also stops sending on the dead leg
		// (CapLegState); the native park is otherwise send-side-only here.
		rg.sendLegState(i, true)
	}
	if len(idxs) > 0 {
		rg.mu.Lock()
		if rg.mux != nil {
			rg.mux.rebuildWeights(rg.tps)
		}
		rg.mu.Unlock()
	}
}

// reportStalledLegs records the stall WITHOUT acting on it: one MuxEventLegStalled
// per stalled leg, naming the leg and the reorder gap age. Used by the side that
// honors the peer's mirrored active set (the acceptor), where a local park would
// be signaled back to the initiator and override the set the initiator owns.
func (rg *RouteGroup) reportStalledLegs(deadSet map[uuid.UUID]struct{}) {
	if rg.mux == nil {
		return
	}
	gap := rg.mux.gapAge()
	rg.mu.Lock()
	defer rg.mu.Unlock()
	reported := 0
	for i, tp := range rg.tps {
		if i == 0 || tp == nil {
			continue // the primary anchor is never parked anyway
		}
		if _, dead := deadSet[tp.Entry.ID]; !dead {
			continue
		}
		rg.noteLegEvent(MuxEventLegStalled,
			fmt.Sprintf("data progress stalled with an open reorder gap (age %v) — acceptor honors the initiator's mirrored active set, so the leg is reported, not parked", gap),
			MuxByAdaptive, i, len(rg.tps), tp, rg.legHopsLocked(tp.Entry.ID))
		reported++
	}
	if reported > 0 {
		rg.logger.Infof("leg-dataprogress: NOT parking %d stalled leg(s) — this side honors the initiator's mirrored active set (reorder gap age %v); recorded as leg_stalled",
			reported, gap)
	}
}

func (rg *RouteGroup) pruneLivenessDeadLegs(deadIDs []uuid.UUID) {
	rg.pruneDeadLegs(deadIDs,
		fmt.Sprintf("liveness: no echo for %d probes (transport stays up)", rg.knInt(routersettings.LegPongMissThreshold)),
		"liveness-dead")
}

// pruneDeadLegs drops every leg whose transport id is in deadIDs, deletes that
// leg's rules, and reports how many legs went. reason is recorded on the
// leg_removed event and hookEvent names the leg-change hook's event.
//
// The LAST leg is never dropped here: a group with no legs has nowhere to put
// the next byte, so an empty group is an outage rather than a repair. Callers
// that know the leg is gone for good (a transport the visor itself removed —
// see handleTransportClosed) close the group instead.
func (rg *RouteGroup) pruneDeadLegs(deadIDs []uuid.UUID, reason, hookEvent string) int {
	deadSet := make(map[uuid.UUID]struct{}, len(deadIDs))
	for _, id := range deadIDs {
		deadSet[id] = struct{}{}
	}

	rg.mu.Lock()
	if len(rg.tps) <= 1 {
		rg.mu.Unlock()
		return 0
	}
	aliveTps := make([]*transport.ManagedTransport, 0, len(rg.tps))
	aliveFwd := make([]routing.Rule, 0, len(rg.fwd))
	aliveRvs := make([]routing.Rule, 0, len(rg.rvs))
	var deadRuleIDs []routing.RouteID
	var droppedIdx []int
	remaining := len(rg.tps)
	for i, tp := range rg.tps {
		isDead := false
		if tp != nil {
			_, isDead = deadSet[tp.Entry.ID]
		}
		if isDead && remaining > 1 {
			if i < len(rg.fwd) {
				deadRuleIDs = append(deadRuleIDs, rg.fwd[i].KeyRouteID())
			}
			if i < len(rg.rvs) {
				deadRuleIDs = append(deadRuleIDs, rg.rvs[i].KeyRouteID())
			}
			if tp != nil {
				rg.logger.Infof("pruning leg %v: %s", tp.Entry.ID, reason)
			}
			rg.noteLegEvent(MuxEventLegRemoved, reason, MuxByAdaptive, i, remaining-1, tp,
				rg.legHopsLocked(tpEntryID(tp)))
			if i == 0 {
				rg.noteMuxEvent(MuxEvent{Event: MuxEventPrimaryRehome, By: MuxByAdaptive, LegIndex: 0,
					Legs: remaining - 1, Reason: "primary leg failed " + reason})
			}
			droppedIdx = append(droppedIdx, i)
			remaining--
			continue
		}
		aliveTps = append(aliveTps, tp)
		if i < len(rg.fwd) {
			aliveFwd = append(aliveFwd, rg.fwd[i])
		}
		if i < len(rg.rvs) {
			aliveRvs = append(aliveRvs, rg.rvs[i])
		}
	}
	if len(droppedIdx) == 0 {
		rg.mu.Unlock()
		return 0
	}
	if len(deadRuleIDs) > 0 {
		rg.rt.DelRules(deadRuleIDs)
	}
	rg.tps = aliveTps
	rg.fwd = aliveFwd
	rg.rvs = aliveRvs
	if rg.mux != nil {
		rg.mux.removeLegs(droppedIdx...)
	}
	rg.mu.Unlock()

	for _, idx := range droppedIdx {
		rg.fireLegChange(hookEvent, idx)
	}
	rg.signalRotate()
	rg.maybeSelfHeal()
	return len(droppedIdx)
}

// soleLegExcludeHopsLocked returns the intermediate PKs of the route group's
// recorded (primary) forward path, so a replacement dial can be steered onto a
// disjoint route. Best-effort: the rg only records the primary forwardHops, so
// for an aux-only survivor this is a hint, not an exact exclusion — combined with
// addOneAuxForwardLeg's own avoid-already-used-intermediates planning it is
// enough to keep the finder off the black-holing path. Caller holds rg.mu.
func (rg *RouteGroup) soleLegExcludeHopsLocked() []string {
	if len(rg.forwardHops) <= 1 {
		return nil
	}
	excl := make([]string, 0, len(rg.forwardHops)-1)
	for _, h := range rg.forwardHops[:len(rg.forwardHops)-1] {
		excl = append(excl, h.To.String())
	}
	return excl
}

// healSoleBlackHoledLeg replaces a single black-holing leg WITHOUT ever dropping
// to zero legs. It dials one replacement aux leg via a disjoint path (excluding
// the current leg's intermediates so the finder cannot re-pick the same
// black-holing route — classically a deceptively-low-latency LAN short-circuit),
// and prunes the dead leg only once the replacement is live. maybeSelfHeal cannot
// do this: it bails when selfHealTarget<=1 (the low-latency-direct / single-route
// case), which is exactly when a lone leg has no failover and most needs
// replacing. If no disjoint replacement is available the dead leg is kept (still
// one leg, not an outage) and a later cycle retries.
func (rg *RouteGroup) healSoleBlackHoledLeg(deadID uuid.UUID) {
	rg.mu.Lock()
	add := rg.selfHealAdd
	excl := rg.soleLegExcludeHopsLocked()
	rg.mu.Unlock()
	if add == nil || rg.isClosed() {
		return
	}
	if !rg.healInFlight.CompareAndSwap(false, true) {
		return // a replacement is already dialing
	}
	go func() {
		defer rg.healInFlight.Store(false)
		before := rg.aliveLegCount()
		add(excl) // dials one disjoint replacement leg (blocks ~one setup dial)
		if rg.isClosed() {
			return
		}
		if rg.aliveLegCount() > before {
			// Replacement is live; now it is safe to drop the black-holing leg.
			rg.pruneLivenessDeadLegs([]uuid.UUID{deadID})
		} else if rg.logger != nil {
			rg.logger.Debug("sole-leg heal: no disjoint replacement available; keeping current leg for now")
		}
	}()
}

// pruneDeadTransports removes closed transports from the mux.
// Cleans up the corresponding forward/reverse rules from the routing table.
// Must be called with rg.mu held. Returns the original indexes
// of legs that were pruned, so the caller can fire OnLegChange
// events for them after releasing the lock.
func (rg *RouteGroup) pruneDeadTransports() []int {
	if len(rg.tps) <= 1 {
		return nil // Don't prune the last transport
	}

	aliveTps := make([]*transport.ManagedTransport, 0, len(rg.tps))
	aliveFwd := make([]routing.Rule, 0, len(rg.fwd))
	aliveRvs := make([]routing.Rule, 0, len(rg.rvs))
	var deadRuleIDs []routing.RouteID
	var droppedIdx []int

	for i, tp := range rg.tps {
		if tp != nil && !tp.IsClosed() {
			aliveTps = append(aliveTps, tp)
			if i < len(rg.fwd) {
				aliveFwd = append(aliveFwd, rg.fwd[i])
			}
			if i < len(rg.rvs) {
				aliveRvs = append(aliveRvs, rg.rvs[i])
			}
		} else {
			if i < len(rg.fwd) {
				deadRuleIDs = append(deadRuleIDs, rg.fwd[i].KeyRouteID())
			}
			if i < len(rg.rvs) {
				deadRuleIDs = append(deadRuleIDs, rg.rvs[i].KeyRouteID())
			}
			reason := "transport closed"
			if tp == nil {
				reason = "leg has no transport"
			} else {
				rg.logger.Infof("Pruning dead mux transport %v", tp.Entry.ID)
			}
			rg.noteLegEvent(MuxEventLegRemoved, reason, MuxByLocal, i, len(rg.tps)-len(droppedIdx)-1, tp,
				rg.legHopsLocked(tpEntryID(tp)))
			if i == 0 {
				rg.noteMuxEvent(MuxEvent{Event: MuxEventPrimaryRehome, By: MuxByLocal, LegIndex: 0,
					Legs: len(rg.tps) - len(droppedIdx) - 1, Reason: "primary leg's " + reason})
			}
			droppedIdx = append(droppedIdx, i)
		}
	}

	if len(aliveTps) == len(rg.tps) {
		return nil // Nothing was pruned
	}

	// Clean up dead rules from routing table
	if len(deadRuleIDs) > 0 {
		rg.rt.DelRules(deadRuleIDs)
	}

	rg.tps = aliveTps
	rg.fwd = aliveFwd
	rg.rvs = aliveRvs

	// Compact the per-leg counter/readiness arrays in lockstep so they stay
	// aligned with the pruned tps[]/fwd[]/rvs[]. droppedIdx are the original
	// pre-compaction indices.
	if rg.mux != nil {
		rg.mux.removeLegs(droppedIdx...)
	}

	rg.logger.Infof("Pruned dead transports: %d alive, %d removed", len(aliveTps), len(deadRuleIDs)/2)
	return droppedIdx
}

// pruneLegByConsumeRule removes the single mux leg whose consume (reverse) rule
// matches routeID, WITHOUT closing the route group — the endpoint counterpart to
// a remote peer retiring one leg (routing.CloseLegRetired). It reclaims that
// leg's forward+consume rules and drops it from the mux, leaving the group and
// its other legs live. Returns true if a leg was pruned; false if this is the
// group's last leg (or the rule wasn't found), in which case the caller falls
// back to a normal whole-group close. Mirrors pruneDeadTransports' slice/rule
// bookkeeping; tolerates pruning index 0 exactly as the dead-transport path does
// (the mux selector is rebuilt and tps[0]-hardcoded probes re-target the new
// primary leg).
func (rg *RouteGroup) pruneLegByConsumeRule(routeID routing.RouteID) bool {
	rg.mu.Lock()
	if len(rg.tps) <= 1 || len(rg.rvs) <= 1 {
		rg.mu.Unlock()
		return false // last leg — let the caller do a full close
	}
	idx := -1
	for i, rv := range rg.rvs {
		if rv != nil && rv.KeyRouteID() == routeID {
			idx = i
			break
		}
	}
	if idx < 0 {
		rg.mu.Unlock()
		return false
	}

	var deadRuleIDs []routing.RouteID
	if idx < len(rg.fwd) {
		deadRuleIDs = append(deadRuleIDs, rg.fwd[idx].KeyRouteID())
	}
	if idx < len(rg.rvs) {
		deadRuleIDs = append(deadRuleIDs, rg.rvs[idx].KeyRouteID())
	}

	var retiredTp *transport.ManagedTransport
	if idx < len(rg.tps) {
		retiredTp = rg.tps[idx]
		rg.tps = append(rg.tps[:idx], rg.tps[idx+1:]...)
	}
	if idx < len(rg.fwd) {
		rg.fwd = append(rg.fwd[:idx], rg.fwd[idx+1:]...)
	}
	if idx < len(rg.rvs) {
		rg.rvs = append(rg.rvs[:idx], rg.rvs[idx+1:]...)
	}
	rg.noteLegEvent(MuxEventLegRemoved, "peer retired the leg (close: leg-retired)", MuxByRemote,
		idx, len(rg.tps), retiredTp, rg.legHopsLocked(tpEntryID(retiredTp)))
	if idx == 0 {
		rg.noteMuxEvent(MuxEvent{Event: MuxEventPrimaryRehome, By: MuxByRemote, LegIndex: 0, Legs: len(rg.tps),
			Reason: "peer retired the primary leg"})
	}

	if len(deadRuleIDs) > 0 {
		rg.rt.DelRules(deadRuleIDs)
	}
	if rg.mux != nil {
		rg.mux.removeLegs(idx)
		rg.mux.rebuildWeights(rg.tps)
	}
	rg.logger.Infof("Retired remote mux leg %d (consume rule %d); %d legs remain", idx, routeID, len(rg.tps))
	rg.mu.Unlock()

	rg.fireLegChange("retired-remote", idx)
	return true
}

// selfHealNoProgressLimit is how many consecutive self-heal dials may fail to
// grow the live leg count before the heal concludes the destination's disjoint-
// intermediate set is exhausted for now and stops (instead of hammering the
// setup node for the full uncapped target). A later leg death or newly-online
// transport re-triggers the heal, so this is a backoff, not a cap.
const selfHealNoProgressLimit = 4

// legRecvDelta is one active-or-standby leg's rg-scoped recv progress over a
// data-progress interval.
type legRecvDelta struct {
	id      uuid.UUID
	delta   uint64
	standby bool
}

// legStallAction is what the receive-side data-progress detector does with the
// legs selectDataStalledLegs flagged.
type legStallAction int

// bandLeg is one leg's latency-band inputs: its index, measured end-to-end
// latency (ms; <=0 == not yet measured), whether it is currently a warm standby,
// whether it is the primary (index 0, never demoted), and its recent delivered
// goodput (recvDelta, bytes over the last data-progress interval) used to gate
// latency demotion (see the goodput-gate note in partitionLatencyBand).
type bandLeg struct {
	idx   int
	latMs float64
	// windowed is true when latMs is the leg's min-RTT over legRTTMinWindow
	// rather than the fallback single EWMA/first-hop sample. Diagnostic only —
	// the partition math treats both alike — but it is what the leg event's
	// reason names, so an operator can tell the two apart.
	windowed  bool
	standby   bool
	primary   bool
	recvDelta uint64
}

// legGoodput pairs a leg index with its recv-goodput delta, for reverse-floor
// rescue ranking.
type legGoodput struct {
	idx int
	gp  uint64
}
