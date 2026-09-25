// Package router pkg/router/route_group_services.go c2-net-routing
package router

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// fecFlushServiceFn completes a pending partial FEC block on stream idle so the
// tail frames (the final j<K of a transfer, which Add never codes because the
// block never fills) gain repair protection. On an idle tick with a partial block
// pending it emits the K-j remaining slots as empty PADDING frames through the
// normal send path: each advances writeSeq and the striper, and the K-th
// completes the block so Add() emits the R repair frames (auto-scheduled onto a
// fast leg by write). The padding frames are REAL sealed sequenced-data frames,
// so encoder and decoder agree bit-for-bit in the wire domain (no special
// nonce), and they also fill the seq gap that the no-skip reorder buffer would
// otherwise stall on. The receiver records them for FEC and delivers them as
// 0-byte reads, which the mux delivery loop filters out of the app stream (the
// app never sends an empty frame — Write rejects len==0 — so an empty delivered
// payload is unambiguously FEC padding). Bounded by K as a runaway guard.
func (rg *RouteGroup) fecFlushServiceFn(_ time.Duration) {
	if rg.mux == nil || !rg.mux.fecEnabled || rg.mux.fecStriper == nil {
		return
	}
	lastSent := time.Unix(0, rg.lastSent.Load())
	if !fecShouldFlush(time.Now(), lastSent, fecDefaultIdleFlush, rg.mux.fecStriper.hasPartialBlock()) {
		return
	}
	for i := 0; i < rg.knInt(routersettings.FECK) && rg.mux.fecStriper.hasPartialBlock(); i++ {
		if err := rg.writePaddingFrame(); err != nil {
			return
		}
	}
}

func (rg *RouteGroup) startOffServiceLoops() {
	go rg.servicePacketLoop("keep-alive", rg.cfg.KeepAliveInterval, rg.keepAliveServiceFn, nil)
	// Per-leg end-to-end liveness (issue #2): detect mux legs that black-hole
	// BEYOND the first hop (invisible to pruneDeadTransports' local tp.IsClosed
	// check) and drop them so self-heal re-dials a live replacement.
	go rg.serviceKnobLoop("leg-liveness", routersettings.LegLivenessInterval, rg.legLivenessServiceFn)
	// Fast data-progress prune: catch a leg that black-holes bulk DATA (while
	// still echoing the tiny liveness ping, so the pong-miss path above never
	// sees it) in seconds, by watching per-leg recv progress against an open
	// reorder gap. Restores mux throughput to the reliable legs' rate instead of
	// limping at the fragile leg's retransmit tax.
	go rg.serviceKnobLoop("leg-dataprogress", routersettings.LegDataProgressInterval, rg.legDataProgressServiceFn)
	// Send-window refresh: a leg's window grows with what the peer's SACKs
	// prove delivered; refreshed only at the rebuild cadence (seconds) it
	// doubled from 128 KiB every ~5 s and pinned an upload at ~1.7 MB/s, so
	// refresh four times a second.
	// Gated: with nothing outstanding in the retransmit buffer the window is
	// recomputed from unchanged inputs, and no writer can be parked waiting to
	// be signaled. The write path wakes it.
	go rg.serviceKnobLoopGated("send-window", routersettings.SendWindowRefreshInterval, rg.windowServiceFn, rg.muxDormant)
	// Timer-driven reorder-stall recovery: when a frontier gap is stuck past
	// reorderTimeout with no packet arriving to trigger the arrival-driven SACK,
	// emit a SACK so the sender retransmits the missing seq in order (never skip).
	// Gated: this fires only on a frontier gap held past reorder.timeout, and a
	// gap can only open when a packet arrives — which wakes it.
	go rg.serviceKnobLoopGated("reorder-stall", routersettings.ReorderStallInterval, rg.reorderStallServiceFn, rg.muxDormant)
	// Periodic leg-state resync: re-assert the full standby/active set to the peer
	// so a lost park/promote signal self-corrects instead of desyncing the mirror
	// permanently (CapLegState). No-op unless negotiated; see legStateResyncServiceFn.
	// Gated: its loop runs from leg index 1, so it is a no-op below two legs.
	go rg.serviceKnobLoopGated("legstate-resync", routersettings.LegStateResyncInterval, rg.legStateResyncServiceFn, rg.singleLegDormant)
	// Unidirectional flip controller (CapUniDir): both ends run this, so the
	// direction→leg-class mapping flips together when the traffic asymmetry
	// inverts (upload outweighs download → the heavy upload gets the mux). No-op
	// unless directional; the fn self-gates. Cadence matches the reorder-stall
	// tick so the hysteresis is a small number of seconds.
	// Gated: a direction→leg-class mapping needs two classes of leg to map onto.
	go rg.serviceKnobLoopGated("unidir-flip", routersettings.UnidirFlipInterval, rg.unidirFlipServiceFn, rg.singleLegDormant)
	// Note: Automatic ping loop removed. Latency is now measured once at transport creation.
	// Rotation loop is NOT started here — startOffServiceLoops runs
	// during initial route-group setup, before the router-side
	// SetRotation call wires the hook + interval. SetRotation spawns
	// the rotation goroutine itself.
}

// rotationServiceFn fires the policy's on_tick hook against the
// current leg snapshot and applies the returned action: drop
// the listed leg indices, then optionally request one fresh aux
// leg via the router-supplied applyAdd callback.
//
// Drops apply before the add so a "drop oldest + add new" policy
// produces a clean rotation (one in, one out). The add runs
// synchronously here — a slow setup-node dial blocks the next
// tick for THIS route group only (other groups have their own
// rotation goroutines).
func (rg *RouteGroup) rotationServiceFn(_ time.Duration) {
	rg.mu.Lock()
	hook := rg.rotationHook
	applyAdd := rg.rotationApplyAdd
	applyAddForward := rg.rotationApplyAddForward
	info := rg.legChangeInfo
	legs := rg.snapshotLegs()
	rg.mu.Unlock()
	if hook == nil {
		return
	}
	// Track the live self-heal target for an adaptive group: its pool size
	// (active width + warm-standby reserve) is a runtime tunable, so re-read it
	// each tick and re-cap maybeSelfHeal's target — lowering the reserve at
	// runtime then actually stops the self-heal from re-dialing beyond the new
	// size (the excess parked legs are shed by the tick's own converge-down rule).
	if st, ok := hook.(SelfHealTargeter); ok {
		if target, ok := st.SelfHealTarget(); ok {
			rg.setSelfHealTarget(target)
		}
	}
	action := hook.OnTick(info, legs)
	if len(action.DropLegs) == 0 && !action.AddLeg && !action.AddForwardLeg &&
		len(action.DemoteToStandby) == 0 && len(action.PromoteFromStandby) == 0 {
		return
	}

	// Promote/demote first: they only flip the mux's per-leg standby flag (no
	// teardown, no compaction), so they must run on the pre-drop indices. A
	// demoted leg stays in tps[] — kept alive by the keepalive/liveness loops —
	// but is skipped by selectTransport; promoting re-selects it instantly.
	demoted := false
	if rg.mux != nil {
		for _, idx := range action.PromoteFromStandby {
			// Through the hysteresis seam: an adaptive park that named a reason
			// holds for legParkMinHold, so the policy tick cannot undo it on the
			// next tick. It also mirrors the promotion to the peer and emits the
			// lifecycle event.
			rg.promoteLegAdaptive(idx, "policy tick promoted the leg from standby")
		}
		for _, idx := range action.DemoteToStandby {
			rg.mux.setLegStandby(idx, true)
			// Tell the remote so it stops striping its send traffic across this
			// leg — the fix for the wide-mux download stall where the bulk-sending
			// peer fanned data over every established leg, standby or not.
			rg.sendLegState(idx, true)
			demoted = true
		}
	}

	// Forced retx flush on demote: the instant a leg flips to standby it stops
	// carrying its in-flight, unACKed sequences. The receiver's post-#4005
	// no-skip reorder holds that gap open until those sequences arrive; if they
	// age out of the bounded retx buffer before the receiver's SACK round-trip
	// asks for them, the sender can no longer resend and the gap wedges forever
	// (the #86 retx-window aged-out gap, here triggered by the demote itself —
	// the mechanism behind the live "17MB -> 0 at first demote" stall). So
	// proactively re-send the whole outstanding in-flight window onto an active,
	// non-standby leg NOW — a self-issued full SACK recovery — instead of racing
	// the buffer's aging. The retx buffer is keyed by sequence, not by leg, so
	// this is a flush of every held sequence, not just the demoted leg's; that is
	// bounded (retxBuf is bounded), infrequent (rotation cadence), and safe (the
	// receiver dedups by sequence number, consuming only the actual gap seqs and
	// dropping the rest). Runs before DropLegs so the data is rescued before any
	// compaction. If no active leg is available the flush is a safe no-op — leg 0
	// is never standby, so demoting an aux always leaves a resend target.
	if demoted && rg.mux != nil {
		// Narrowed to the DEMOTED legs' in-flight sequences (plus any with
		// unknown attribution): only those are stranded by the demote; the rest
		// still ride active legs and heal through the normal SACK path. The
		// whole-window flush this replaces duplicated the entire in-flight
		// window (measured 33.8MB of dupes against 14.7MB payload on one leg
		// of a 20MB transfer) on every demote event.
		// Map the demoted leg INDICES to their transports' stable UUIDs under
		// rg.mu before consulting the buffer — the retx tags are transport
		// identities, immune to the index shifts a slice compaction causes.
		rg.mu.Lock()
		demotedTps := make([]uuid.UUID, 0, len(action.DemoteToStandby))
		for _, idx := range action.DemoteToStandby {
			if idx >= 0 && idx < len(rg.tps) && rg.tps[idx] != nil {
				demotedTps = append(demotedTps, rg.tps[idx].Entry.ID)
			}
		}
		rg.mu.Unlock()
		if seqs := rg.mux.heldRetxSeqsOnTps(demotedTps); len(seqs) > 0 {
			rg.mux.retxReqFlush.Add(uint64(len(seqs)))
			if err := rg.resendSeqs(seqs); err != nil {
				rg.logger.WithError(err).Debug("demote retx flush: no active leg to resend on")
			}
		}
	}

	if len(action.DropLegs) > 0 {
		rg.dropLegsByIndex(action.DropLegs)
	}

	if action.AddLeg && applyAdd != nil {
		applyAdd(action.ExcludeHops)
	}

	// Forward-only widen: extra upstream send leg that leaves the reverse set
	// untouched. Falls back to the full-duplex add when no forward-only callback
	// is wired (older dial path) so the widen still happens.
	if action.AddForwardLeg {
		if applyAddForward != nil {
			applyAddForward(action.ExcludeHops)
		} else if applyAdd != nil {
			applyAdd(action.ExcludeHops)
		}
	}
}

// legLivenessServiceFn probes each multiplexed leg end-to-end once per tick and
// drops any leg that has missed legPongMissThreshold consecutive echoes. It
// exists because pruneDeadTransports only detects a dead LOCAL (first-hop)
// transport; a transport dying beyond the first hop leaves the local tp open,
// so the leg silently black-holes and the mux keeps selecting it. The probe
// reuses the existing Ping→Pong echo (the destination echoes the send-timestamp
// verbatim), so it is purely source-side: no wire-format or remote-version
// dependency. Single-leg groups are skipped (no failover target, never drop the
// last leg), so the common case carries zero overhead.
func (rg *RouteGroup) legLivenessServiceFn(_ time.Duration) {
	if rg.isClosed() {
		return
	}

	type legProbe struct {
		tp   *transport.ManagedTransport
		rule routing.Rule
		id   uuid.UUID
	}
	rg.mu.Lock()
	probes := make([]legProbe, 0, len(rg.tps))
	for i, tp := range rg.tps {
		if tp == nil || tp.IsClosed() || i >= len(rg.fwd) {
			continue
		}
		probes = append(probes, legProbe{tp: tp, rule: rg.fwd[i], id: tp.Entry.ID})
	}
	rg.mu.Unlock()

	// A single-leg group is still probed (below): a lone black-holing leg has no
	// failover and self-heal's target<=1 bail never replaces it, so without this
	// it would sit at zero throughput forever. Only a truly empty group is skipped.
	if len(probes) == 0 {
		return
	}

	live := make(map[uuid.UUID]struct{}, len(probes))
	for _, p := range probes {
		live[p.id] = struct{}{}
	}

	// Tally the previous cycle's echoes; a leg already probed that missed
	// legPongMissThreshold consecutive cycles is declared black-holing.
	var dead []uuid.UUID
	rg.legLivenessMu.Lock()
	for _, p := range probes {
		if rg.legPongSeen[p.id] {
			rg.legMissed[p.id] = 0
		} else if _, probed := rg.legMissed[p.id]; probed {
			rg.legMissed[p.id]++
			if rg.legMissed[p.id] >= rg.knInt(routersettings.LegPongMissThreshold) {
				dead = append(dead, p.id)
			}
		}
		rg.legPongSeen[p.id] = false
	}
	for id := range rg.legMissed {
		if _, ok := live[id]; !ok {
			delete(rg.legMissed, id)
			delete(rg.legPongSeen, id)
		}
	}
	staleBeforeMs := time.Now().UTC().UnixNano()/int64(time.Millisecond) -
		int64(rg.knInt(routersettings.LegPongMissThreshold)+2)*rg.knDur(routersettings.LegLivenessInterval).Milliseconds()
	for ts := range rg.inflightPings {
		if ts < staleBeforeMs {
			delete(rg.inflightPings, ts)
		}
	}
	rg.legLivenessMu.Unlock()

	if len(dead) > 0 {
		if len(probes) < 2 {
			// Sole leg is black-holing. Never drop the last leg (that is an
			// outage); instead dial a REPLACEMENT via a disjoint path and drop
			// the dead one only once the replacement is live.
			rg.healSoleBlackHoledLeg(dead[0])
		} else {
			rg.pruneLivenessDeadLegs(dead)
		}
	}

	// Probe the surviving legs for the next cycle. Each send-ts is unique
	// (ms clock + per-leg offset) so two legs probed in the same millisecond
	// don't collide as inflightPings keys; the destination echoes it verbatim.
	deadSet := make(map[uuid.UUID]struct{}, len(dead))
	for _, d := range dead {
		deadSet[d] = struct{}{}
	}
	baseMs := time.Now().UTC().UnixNano() / int64(time.Millisecond)
	for offset, p := range probes {
		if _, isDead := deadSet[p.id]; isDead {
			continue
		}
		ts := baseMs + int64(offset)
		rg.legLivenessMu.Lock()
		rg.inflightPings[ts] = p.id
		if _, ok := rg.legMissed[p.id]; !ok {
			rg.legMissed[p.id] = 0 // mark probed so the next cycle counts misses
		}
		rg.legLivenessMu.Unlock()
		throughput := rg.networkStats.RemoteThroughput()
		packet := routing.MakePingPacket(p.rule.NextRouteID(), ts, throughput)
		if err := rg.writePacket(context.Background(), p.tp, packet, p.rule.KeyRouteID()); err != nil {
			rg.logger.WithError(err).Debugf("leg-liveness: probe write failed on leg %s", p.id)
		}
	}
}

// legDataProgressServiceFn is the FAST data-plane prune. The pong-miss liveness
// above only catches a leg that stops echoing the tiny control ping — but a leg
// (classically webrtc under load) can keep echoing pings while black-holing bulk
// DATA, so it survives ~90s while taxing the whole mux: every seq it drops is
// SACK-retransmitted on a live leg, capping aggregate throughput at the fragile
// leg's rate. This loop samples each leg's rg-scoped RecvBytes every few seconds
// and prunes an under-delivering ACTIVE leg (via selectDataStalledLegs) in two
// regimes: WHEN a reorder frontier gap has stayed open past legDataStallGapAge
// (the receiver is genuinely stuck on a missing seq) it sheds any leg well below
// the leader; and even when the frontier is HEALTHY it sheds a pure goodput
// black-hole — a leg delivering ~zero while a clearly-moving leader carries the
// group (the normal-latency black-hole the latency band never catches). Pruning
// is safe by construction: it never
// drops the last active leg, keeps a shared transport open, and a false positive
// merely triggers a self-heal re-dial (see pruneLivenessDeadLegs). Standby legs
// are excluded (they aren't sent to, so zero recv is expected).
// reorderStallServiceFn drives IN-ORDER recovery of a stuck reorder gap by asking
// the sender to retransmit the missing sequence (via a SACK), so a silent/dead leg
// degrades to the surviving legs' rate WITHOUT ever skipping the gap — skipping
// would corrupt the reliable ordered byte stream the RouteGroup carries (the
// bad-record-mac failure that skipping produced under multi-leg churn).
func (rg *RouteGroup) reorderStallServiceFn(_ time.Duration) {
	if rg.isClosed() || rg.mux == nil || rg.isRemoteClosed() {
		return
	}
	// A frontier gap held past reorderTimeout means the missing sequence's leg has
	// likely stalled/died and the normal packet-arrival-driven SACK isn't firing
	// (nothing is arriving to trigger it). Emit a SACK NOW so the sender resends
	// the missing seq on a live leg and the gap fills IN ORDER. We never skip the
	// gap: the RouteGroup is a reliable ordered net.Conn (TCP/TLS rides it), so
	// delivering past a hole corrupts the stream. The leg-dataprogress prune
	// concurrently removes a genuinely dead leg so its seqs retransmit on survivors.
	gapAge := rg.mux.gapAge()
	if gapAge > routersettings.ReorderTimeout.Duration() {
		// Observability for the reorder WEDGE (the multi-leg-collapse-to-0-B/s
		// failure): the frontier has been stuck past the timeout, so the sender's
		// retransmit isn't refilling the missing seq. Log the stuck seq, how many
		// packets are dammed behind it, the gap age, and the active-leg count so a
		// wedge is diagnosable from the log. Throttled to ~once per reorderTimeout
		// (the service fires every reorderStallInterval) via a transition/decay
		// counter so a sustained wedge doesn't spam.
		ticks := rg.reorderWedgeTicks.Add(1)
		if ticks == 1 || gapAge > rg.reorderWedgeLoggedAt+routersettings.ReorderTimeout.Duration() {
			active := rg.mux.activeLegCount()
			seq := rg.mux.reorderNextSeq()
			pending := rg.mux.reorderPending()
			rg.logger.Warnf("reorder-WEDGE: frontier stuck at seq=%d for %v, pending=%d packets dammed, active_legs=%d — sender retransmit not refilling the gap (SACK re-sent)",
				seq, gapAge.Truncate(time.Millisecond), pending, active)
			rg.reorderWedgeLoggedAt = gapAge
			if ticks == 1 {
				// First detection of THIS wedge: stamp it and record the event, so
				// the far end's `visor state --select diag` carries the wedge next
				// to the leg churn instead of it living only in this visor's log
				// ring (which holds minutes).
				rg.reorderWedgeStartNano.Store(time.Now().UnixNano())
				atomic.StoreUint32(&rg.reorderWedgeSeq, seq)
				rg.noteMuxEvent(MuxEvent{
					Event:    MuxEventReorderWedge,
					By:       MuxByLocal,
					LegIndex: -1,
					Legs:     active,
					Reason: fmt.Sprintf("frontier stuck at seq=%d for %v, pending=%d, active_legs=%d",
						seq, gapAge.Truncate(time.Millisecond), pending, active),
				})
			}
		}
		if err := rg.sendSACK(); err != nil {
			rg.logger.WithError(err).Debug("reorder-stall SACK failed")
		}
	} else if ticks := rg.reorderWedgeTicks.Swap(0); ticks > 0 {
		// Gap cleared — log the recovery so the wedge's duration is bounded in the log.
		var held time.Duration
		if startNano := rg.reorderWedgeStartNano.Swap(0); startNano != 0 {
			held = time.Since(time.Unix(0, startNano))
		}
		seq := atomic.LoadUint32(&rg.reorderWedgeSeq)
		rg.reorderWedges.Add(1)
		if ms := held.Milliseconds(); ms > rg.reorderWedgeLongestMs.Load() {
			rg.reorderWedgeLongestMs.Store(ms)
		}
		rg.logger.Infof("reorder-wedge CLEARED after %d stall ticks (frontier advancing again)", ticks)
		rg.noteMuxEvent(MuxEvent{
			Event:    MuxEventReorderWedgeCleared,
			By:       MuxByLocal,
			LegIndex: -1,
			Legs:     rg.mux.activeLegCount(),
			Reason:   fmt.Sprintf("seq=%d after %v, %d ticks", seq, held.Truncate(100*time.Millisecond), ticks),
		})
		rg.reorderWedgeLoggedAt = 0
	}
}

// legStateResyncServiceFn periodically re-asserts this side's COMPLETE aux-leg
// standby/active set to the peer (CapLegState). The park/promote signals are
// otherwise purely event-driven, so a single dropped LegStatePacket desyncs the
// two ends permanently: the accept side (all-active) keeps striping its send
// traffic across legs this side has parked, over-subscribing the no-skip reorder
// frontier (measured: the peer holding 33 active legs while this side had ~8, so
// ~99% of a download rode "standby" legs). Re-broadcasting the full set every
// legStateResyncInterval lets any lost event self-correct so the bulk sender
// converges on this side's active set. Leg 0 (the primary) is always active and
// never signaled. No-op unless CapLegState was negotiated.
func (rg *RouteGroup) legStateResyncServiceFn(_ time.Duration) {
	if rg.isClosed() || rg.mux == nil || !rg.mux.legStateEnabled || rg.isRemoteClosed() {
		return
	}
	rg.mu.Lock()
	n := len(rg.tps)
	rg.mu.Unlock()
	// sendLegState / isLegStandby are each individually bounds-checked and take
	// their own locks, so the leg set changing mid-loop is safe (a since-removed
	// index is a no-op).
	for idx := 1; idx < n; idx++ {
		standby := rg.mux.isLegStandby(idx)
		if standby && rg.legParkedByPeer(idx) {
			// This park is the PEER's decision, mirrored here. The resync exists to
			// repair a dropped signal of OUR OWN decisions; echoing the peer's park
			// back at it holds the leg down from both ends even after the peer would
			// have promoted it. The peer's own resync keeps its side asserted, and
			// its promote clears this record (notePeerLegState).
			continue
		}
		rg.sendLegState(idx, standby)
	}
}

func (rg *RouteGroup) legDataProgressServiceFn(_ time.Duration) {
	if rg.isClosed() || rg.mux == nil {
		return
	}

	rg.mu.Lock()
	tpsCopy := append([]*transport.ManagedTransport(nil), rg.tps...)
	manual := !rg.standbyNewLegs
	rg.mu.Unlock()

	stats := rg.mux.snapshotLegs()

	type legRecv struct {
		id      uuid.UUID
		recv    uint64
		standby bool
		direct  bool
	}
	legs := make([]legRecv, 0, len(tpsCopy))
	for i, tp := range tpsCopy {
		if tp == nil || i >= len(stats) {
			continue
		}
		legs = append(legs, legRecv{
			id:      tp.Entry.ID,
			recv:    stats[i].PayloadBytes, // payload only: control frames (SACKs) must not elect a leader while the peer downloads
			standby: rg.mux.isLegStandby(i),
			direct:  rg.mux.legIsDirectTp(tp),
		})
	}

	// Only judge when the receiver is genuinely stuck on a missing sequence.
	gapStuck := rg.mux.gapAge() >= rg.knDur(routersettings.LegDataStallGapAge)

	// Compute per-leg recv deltas vs the last sample and refresh the snapshot.
	rg.legLivenessMu.Lock()
	perDelta := make(map[uuid.UUID]uint64, len(legs))
	var aggDelta uint64
	liveIDs := make(map[uuid.UUID]struct{}, len(legs))
	for _, l := range legs {
		liveIDs[l.id] = struct{}{}
		if prev, ok := rg.legRecvSnap[l.id]; ok && l.recv >= prev {
			d := l.recv - prev
			perDelta[l.id] = d
			aggDelta += d
		}
		rg.legRecvSnap[l.id] = l.recv
	}
	for id := range rg.legRecvSnap {
		if _, ok := liveIDs[id]; !ok {
			delete(rg.legRecvSnap, id)
		}
	}
	rg.legLivenessMu.Unlock()

	deltas := make([]legRecvDelta, 0, len(legs))
	for _, l := range legs {
		deltas = append(deltas, legRecvDelta{id: l.id, delta: perDelta[l.id], standby: l.standby})
	}

	// Directional confinement / fan-out observability. Under CapUniDir the download
	// must ride only the ACTIVE reverse (multihop) legs — the initiator-mirrored
	// active set. Two failure signatures make the collapse-to-0 diagnosable:
	//   reverse_standby_recv > 0 — the exit is spraying the download onto standby
	//     reverse legs it should not use (the fan-out bug that over-subscribes the
	//     reorder frontier and wedges the group), and
	//   direct_recv > 0 during a download — confinement broke and payload is
	//     landing on the wrong-direction direct leg.
	// Logged only while data is actually moving, throttled, and immediately when a
	// failure signature appears so a wedge's cause is in the log next to it.
	if rg.mux.isDirectional() && aggDelta > 0 {
		var revActive, revStandby, directRecv int
		for _, l := range legs {
			if perDelta[l.id] == 0 {
				continue
			}
			switch {
			case l.direct:
				directRecv++
			case l.standby:
				revStandby++
			default:
				revActive++
			}
		}
		rg.dirFanoutTicks++
		badSignature := revStandby > 0 || directRecv > 0
		if badSignature || rg.dirFanoutTicks == 1 || rg.dirFanoutTicks%10 == 0 {
			lvl := rg.logger.Debugf
			if badSignature {
				lvl = rg.logger.Warnf
			}
			msg := "unidir fan-out: download on reverse_active=%d reverse_STANDBY=%d direct=%d legs (moved %dB)"
			if badSignature {
				msg += " — STANDBY/direct recv should be 0: the exit is not honoring the mirrored active set (reorder-frontier over-subscription)"
			}
			lvl(msg, revActive, revStandby, directRecv, aggDelta)
		}
	} else {
		rg.dirFanoutTicks = 0
	}
	dead := selectDataStalledLegsWith(rg.knobs(), deltas, gapStuck)
	if len(dead) > 0 {
		// PARK (don't remove) whenever REMOVING a stalled leg would orphan the
		// sequences the sender already stripe-assigned to it — which permanently
		// wedges the no-skip reorder frontier (the observed N-leg → 1-leg collapse
		// that then stalls at 0 B/s). Two cases must park:
		//   - manual mode: the operator / static-mux policy pinned this set, and
		//   - a STUCK frontier (gapStuck): the receiver is HoL-blocked on a missing
		//     seq, so most legs read as "stalled" only because delivery is blocked —
		//     they are not black-holing. Removing them here is exactly what turned a
		//     transient stall into a permanent wedge: the sender's SACK retransmits
		//     of the orphaned seqs then landed on a route whose rule was just torn
		//     down ("Dropped transport frame for stale route"), so the gap could
		//     never fill. Parking keeps each leg RECEIVING (its in-flight drains and
		//     retransmits still land) and re-promotable once the gap clears; the
		//     active-download floor re-promotes the healthy ones.
		// Only a genuine black-hole caught while the frontier is HEALTHY
		// (gapStuck == false) is REMOVED + self-heal-redialed: there is no critical
		// in-flight to orphan, and churning a dead leg for a fresh one is the intent.
		switch stalledLegAction(manual, gapStuck) {
		case legStallIgnore:
			// IDLE, not stalled: the frontier is healthy, so nothing is being held
			// up — these legs simply carried no payload because the PEER chose not
			// to send on them. A receiver cannot tell "the sender skipped this leg"
			// from "this leg dropped my data" except by the gap, so with no gap
			// there is no evidence of a stall and no park. (Measured: during an
			// upload the exit saw 9537/17/16 B on the idle leg, parked it with gap
			// age 0s / stuck=false, mirrored the park back over CapLegState, and the
			// next five downloads ran on one leg.)
			rg.logger.Debugf("leg-dataprogress: %d leg(s) idle but frontier healthy (gap age %v) — idle is not stalled, not parking",
				len(dead), rg.mux.gapAge())
		case legStallPark:
			reason := "frontier stuck — parking avoids orphaning in-flight (retransmits keep landing)"
			if manual {
				reason = "manual mode — pinned set kept for recovery"
			}
			rg.logger.Infof("leg-dataprogress: parking %d stalled leg(s) to warm standby (%s; reorder gap age %v, stuck=%v)",
				len(dead), reason, rg.mux.gapAge(), gapStuck)
			rg.demoteStalledLegs(dead)
		default:
			rg.logger.Infof("leg-dataprogress: fast-pruning %d data-black-holing leg(s) (delivered a negligible share of the fastest leg over %v while group moved %dB; reorder gap age %v, stuck=%v)",
				len(dead), rg.knDur(routersettings.LegDataProgressInterval), aggDelta, rg.mux.gapAge(), gapStuck)
			rg.pruneLivenessDeadLegs(dead)
		}
	}

	// SOLE-LEG BLACK-HOLE reaping. A group down to ONE active leg whose route
	// passes liveness pongs (tiny frames get through) but never delivers bulk
	// data is invisible to BOTH prunes: pong-liveness sees "alive", and
	// selectDataStalledLegs bails at active<2 (no leader to compare against). So
	// a --mux 1 client that lands a black-holing route stays dead forever — the
	// establishment lottery this fixes. Detect it directly: the sole active leg
	// has SENT a request (cumulative sent past a floor) yet DELIVERED no payload
	// (cumulative unique payload still zero) for several consecutive intervals,
	// so dial a REPLACEMENT (bypassing the target<=1 self-heal gate).
	// Once a second leg is up the ordinary black-hole prune sheds the dead one.
	activeCnt, soleSent, soleRecv := 0, uint64(0), uint64(0)
	for i, tp := range tpsCopy {
		if tp == nil || i >= len(stats) || rg.mux.isLegStandby(i) {
			continue
		}
		activeCnt++
		soleSent, soleRecv = stats[i].SentBytes, stats[i].PayloadBytes
	}
	// Direction-aware exemption: under unidirectional assignment (CapUniDir) the
	// sole active leg is the light-direction leg (the direct upload leg on a
	// download), which receives ~nothing because the download flows on the reverse
	// mux legs. If the GROUP is receiving on those legs (aggDelta above the floor),
	// the sole send leg is not a black-hole — reaping it would prune the direct leg
	// exactly when unidir is doing its job (the leg then re-dials multihop and the
	// direct/forward binding is lost). Skip the reaping in that case.
	if soleLegBlackHoleExempt(rg.mux.isDirectional(), aggDelta) {
		rg.soleBHTicks = 0
	} else if soleLegBlackHoled(activeCnt, soleSent, soleRecv) {
		rg.soleBHTicks++
		if rg.soleBHTicks >= rg.knInt(routersettings.LegSoleBlackHoleTicks) {
			rg.logger.Warnf("sole-leg black-hole: only active route sent %dB but delivered %dB of payload over %v — dialing a replacement route",
				soleSent, soleRecv, time.Duration(rg.knInt(routersettings.LegSoleBlackHoleTicks))*rg.knDur(routersettings.LegDataProgressInterval))
			rg.soleBHTicks = 0
			go rg.healReplaceSoleLeg()
		}
	} else {
		rg.soleBHTicks = 0
	}

	// Detect shared-bottleneck groups from each leg's OWD-variation statistics
	// (RFC 8382) BEFORE the weight rebuild below, so the capacity weights count
	// each bottleneck as ONE pipe (not N competing legs) and redundant
	// co-bottlenecked legs are parked to warm standby. No new probe traffic — the
	// OWD windows are fed from the leg-liveness pong. The rebuild that follows
	// picks up both the fresh grouping and any parks.
	rg.enforceBottleneckGroups(perDelta)

	// Refresh transport-selection weights on this fast cadence so
	// WeightModeCapacity tracks RECENT goodput within seconds instead of the
	// ~5min keep-alive cadence — essential for the weighted-ramp (a promoted leg
	// earns its share within a few ticks, a fading leg loses it just as fast).
	// Cheap: a per-leg byte-delta under the mux lock plus a selector rebuild.
	rg.mu.Lock()
	if rg.mux != nil && len(rg.tps) > 1 {
		rg.mux.rebuildWeights(rg.tps)
	}
	rg.mu.Unlock()

	// Keep the active stripe set within a tight latency band so one out-of-band
	// leg (classically a 2ms LAN short-circuit beside 300ms routes) can't open a
	// reorder gap that HoL-caps or stalls the whole mux. Same cadence as the
	// data-progress prune; complementary — the prune sheds a leg that stops
	// delivering, this parks a leg whose latency is too disparate to stripe with.
	rg.enforceLatencyBand(perDelta)
}

// servicePacketLoop runs f every interval until the group closes. trigger, when
// non-nil, is an out-of-band wake channel: a receive on it runs f immediately
// (in addition to the periodic tick) — used by the rotation loop so a leg death
// promotes a warm standby at once rather than on the next interval. Pass nil for
// loops that only need the periodic cadence (a nil channel never fires in the
// select, so those loops are unchanged).
// serviceKnobLoop is servicePacketLoop whose cadence is a live knob: it
// re-resolves the group's view on every tick (the change notification the
// dataplane reads from) and resets the ticker when the cadence itself moved.
// That is what makes send.window_refresh_interval — handed to a goroutine when
// the group was built, and excluded from the first live set for exactly that
// reason — settable on a running visor.
func (rg *RouteGroup) serviceKnobLoop(name string, k *routersettings.Knob, f sendServicePacketFn) {
	rg.serviceKnobLoopGated(name, k, f, nil)
}

// serviceKnobLoopGated is serviceKnobLoop with an optional dormancy gate: while
// dormant reports true the loop stops its ticker and parks on the group's wake
// broadcast, so it costs a stack and no timer. Pass nil to tick unconditionally.
//
// The gate is re-read after every wake and after every tick, so a stale or
// spurious wake merely re-parks. See service_gate.go for which loops take a gate
// and why parking each one is safe.
func (rg *RouteGroup) serviceKnobLoopGated(name string, k *routersettings.Knob, f sendServicePacketFn, dormant func() bool) {
	interval := rg.knDur(k)
	if interval <= 0 {
		select {
		case <-rg.remoteClosed:
		case <-rg.closed:
		}
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// parked is this loop's own view of whether its ticker is stopped; the
	// group-wide counter it maintains is what lets signal() skip the mutex when
	// nothing is parked. Unwind it on every exit path.
	parked := false
	defer func() {
		if parked {
			rg.wake.parked.Add(-1)
		}
	}()

	for {
		var wakeCh <-chan struct{}
		switch {
		case dormant == nil:
			// Ungated: tick unconditionally, exactly as before.
		case parked:
			// Still parked: take the CURRENT wake channel, then re-read the
			// gate. Taking it first is what makes a racing signal safe — it
			// closes a channel already held rather than one taken later.
			wakeCh = rg.wake.wait()
			if !dormant() {
				rg.wake.parked.Add(-1)
				parked = false
				wakeCh = nil
				ticker.Reset(interval)
			}
		case dormant():
			// Entering dormancy. Register BEFORE taking the channel and
			// re-reading the gate: a signal that observed a zero counter and
			// skipped therefore published its state before this second read.
			rg.wake.parked.Add(1)
			wakeCh = rg.wake.wait()
			if dormant() {
				// A stopped ticker's channel never fires again, so the select
				// below waits on the wake and the close channels alone.
				ticker.Stop()
				parked = true
			} else {
				rg.wake.parked.Add(-1)
				wakeCh = nil
			}
		}

		select {
		case <-rg.remoteClosed:
			rg.logger.Debugf("Remote got closed, stopping %s loop", name)
			return
		case <-rg.closed:
			rg.logger.Debugf("RouteGroup closed, stopping %s loop", name)
			return
		case <-ticker.C:
			// A tick buffered before Stop can still arrive once; f self-gates,
			// so running it while dormant is a no-op either way.
			f(interval)
		case <-wakeCh:
			// Woken from dormancy; the gate is re-evaluated at the top.
		}
		if rg.refreshKnobs() {
			if next := rg.knDur(k); next > 0 && next != interval {
				interval = next
				if !parked {
					ticker.Reset(interval)
				}
				rg.logger.Debugf("%s loop cadence moved to %s (%s)", name, interval, k.Name())
			}
		}
	}
}

func (rg *RouteGroup) servicePacketLoop(name string, interval time.Duration, f sendServicePacketFn, trigger <-chan struct{}) {
	if interval <= 0 {
		// No keep-alive — routes persist indefinitely. Just wait for close.
		select {
		case <-rg.remoteClosed:
		case <-rg.closed:
		}
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-rg.remoteClosed:
			rg.logger.Debugf("Remote got closed, stopping %s loop", name)
			return
		case <-rg.closed:
			rg.logger.Debugf("RouteGroup closed, stopping %s loop", name)
			return
		case <-ticker.C:
			f(interval)
		case <-trigger:
			f(interval)
		}
	}
}

func (rg *RouteGroup) keepAliveServiceFn(interval time.Duration) {
	lastSent := time.Unix(0, rg.lastSent.Load())

	if time.Since(lastSent) < interval {
		return
	}

	if err := rg.sendKeepAlive(); err != nil {
		failures := atomic.AddInt32(&rg.consecutiveWriteFailures, 1)
		if failures >= maxConsecutiveWriteFailures {
			rg.logger.Warnf("Closing RouteGroup after %d consecutive write failures: %v", failures, err)
			rg.setCloseReason(fmt.Sprintf("%d consecutive keepalive write failures: %v", failures, err))
			go func() { rg.Close() }() //nolint:errcheck,gosec
			return
		}
		rg.logger.Warnf("Failed to send keepalive: %v", err)
	} else {
		atomic.StoreInt32(&rg.consecutiveWriteFailures, 0)
	}

	// Prune dead transports and rebuild weights
	var droppedLegs []int
	if rg.mux != nil {
		rg.mu.Lock()
		droppedLegs = rg.pruneDeadTransports()
		if len(rg.tps) > 1 {
			rg.mux.rebuildWeights(rg.tps)
		}
		rg.mu.Unlock()
	}
	// Fire the policy hook outside the lock so the script can
	// safely call back into rg methods (the on_leg_change script
	// returning a new distribution causes applyDistribution which
	// re-takes rg.mu).
	for _, idx := range droppedLegs {
		rg.fireLegChange("dropped", idx)
	}
	if len(droppedLegs) > 0 {
		rg.signalRotate()
		rg.maybeSelfHeal()
	}
}

// unidirFlipServiceFn is the unidirectional flip controller, run as a service
// loop on BOTH ends. It flips the direction→leg-class mapping when the traffic
// asymmetry inverts (upload outweighs download → the heavy upload takes the mux,
// the light download rides the direct leg), and reverts when it swings back.
// No-op unless CapUniDir negotiated (unidirFlipTick self-gates on directional).
func (rg *RouteGroup) unidirFlipServiceFn(_ time.Duration) {
	if rg.mux == nil {
		return
	}
	if flipped, changed := rg.mux.unidirFlipTick(); changed {
		if flipped {
			rg.logger.Info("unidir-flip: FLIPPED — upload is now the heavy direction, moved onto the mux (download on the direct leg)")
		} else {
			rg.logger.Info("unidir-flip: reverted — download is the heavy direction again, back on the mux (upload on the direct leg)")
		}
	}
}

// sackServiceFn is the periodic SACK sender, run as a service loop.
func (rg *RouteGroup) sackServiceFn(_ time.Duration) {
	if rg.mux == nil || !rg.mux.sackEnabled {
		return
	}
	if err := rg.sendSACK(); err != nil {
		rg.logger.WithError(err).Warn("Failed to send periodic SACK")
	}
}

// tlpServiceFn is the tail-loss probe, run as a service loop. On each tick it asks
// the mux whether a probe is due (idle ≥ PTO with unacked data and probe budget
// left); if so it re-sends the in-flight tail sequence on the fastest live leg via
// the shared retransmit path. The probe either fills a lost tail or draws a SACK
// that reports the gap, unblocking normal recovery. A no-op whenever data is
// flowing (the idle timer keeps resetting) or nothing is outstanding.
func (rg *RouteGroup) tlpServiceFn(_ time.Duration) {
	if rg.mux == nil || !rg.mux.sackEnabled {
		return
	}
	seq, due := rg.mux.tlpProbeSeq(time.Now())
	if !due {
		return
	}
	rg.mux.tlpProbes.Add(1)
	rg.logger.Debugf("TLP: probing tail seq=%d after idle PTO", seq)
	if err := rg.resendSeqs([]uint32{seq}); err != nil {
		rg.logger.WithError(err).Debug("TLP: tail probe send failed")
	}
}

// windowServiceFn refreshes the per-leg send windows from the latest
// SACK-proven delivery and wakes a parked writer (see routeMux.waitSendWindow).
func (rg *RouteGroup) windowServiceFn(_ time.Duration) {
	if rg.isClosed() || rg.mux == nil {
		return
	}
	rg.mu.Lock()
	tps := append([]*transport.ManagedTransport(nil), rg.tps...)
	rg.mu.Unlock()
	rg.mux.refreshLegWindows(tps)
	rg.mux.signalWindow()
}
