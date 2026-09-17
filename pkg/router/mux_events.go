// Package router pkg/router/mux_events.go c2-net-routing
// mux_events.go holds the router's bounded history of route-group and mux-leg
// changes.
package router

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// MuxEvent is one entry in the router's bounded history of route-group and
// mux-leg changes, with the reason each one happened. It exists because the
// question "who took that leg away" had no answer: a pinned two-hop proxy
// session's `proxy mux info` went from one leg to none mid-measurement while
// its first-hop transport stayed open, and neither `visor state` nor
// diag.transport_events (#4919) recorded anything — the visor log ring is the
// only other witness and it holds minutes. `visor state --select diag` carries
// the last MuxEventRingSize of these, and `proxy mux info --json` carries each
// group's most recent few next to its legs.
type MuxEvent struct {
	At    time.Time `json:"at"`
	Event string    `json:"event"`
	// App is the app name the route group was tagged with at dial time
	// (SetAppName), e.g. "via0281a102". Empty on the accept side.
	App string `json:"app,omitempty"`
	// Desc identifies the route group the event belongs to.
	Desc routing.RouteDescriptorFields `json:"desc"`
	// By names who decided: "local", "remote", "operator", "adaptive"
	// (liveness/self-heal) or "policy" (routing-policy rotation hook).
	By string `json:"by,omitempty"`
	// LegIndex is the leg's index at the moment of the event (-1 for
	// whole-group events); Legs is how many legs the group had AFTER it.
	LegIndex int `json:"leg_index"`
	Legs     int `json:"legs"`
	// TpID/TpType/Remote describe the leg's FIRST hop — the transport this
	// visor owns. Zero for whole-group events.
	TpID   uuid.UUID     `json:"tp_id,omitempty"`
	TpType string        `json:"tp_type,omitempty"`
	Remote cipher.PubKey `json:"remote_pk,omitempty"`
	// Hops is the leg's full forward path when the route group recorded one
	// (recordLegRoute); nil for a leg whose path was never stored.
	Hops []RouteHopInfo `json:"hops,omitempty"`
	// Reason is what decided this event, in the words of the code that did
	// ("operator: mux rm", "transport closed", "liveness: no echo for 3
	// probes", "peer retired the leg", …).
	Reason string `json:"reason,omitempty"`
}

// Mux event kinds.
const (
	MuxEventGroupCreated = "group_created"
	MuxEventGroupClosed  = "group_closed"
	MuxEventLegAdded     = "leg_added"
	MuxEventLegRemoved   = "leg_removed"
	MuxEventLegDropped   = "leg_dropped"
	MuxEventLegParked    = "leg_parked"
	// MuxEventLegPromoted is a warm-standby leg entering the active stripe set —
	// the operator pinned it (mux add / mux set) or the policy tick promoted it.
	// The counterpart of MuxEventLegParked, so churn is countable in both
	// directions.
	MuxEventLegPromoted   = "leg_promoted"
	MuxEventLegAddFailed  = "leg_add_failed"
	MuxEventPrimaryRehome = "primary_rehomed"
	// MuxEventReorderWedge / ...Cleared bracket a RECEIVE-side reorder wedge:
	// the frontier held past reorderTimeout because the sender's retransmit
	// never refilled the missing sequence, and then it advanced again. Whole-
	// group events (LegIndex -1) whose Reason names the stuck seq, how long it
	// stayed stuck and how many packets were dammed behind it — so the wedge
	// outlives the visor log ring and is readable from `visor state`.
	MuxEventReorderWedge        = "reorder_wedge"
	MuxEventReorderWedgeCleared = "reorder_wedge_cleared"
	// MuxEventDialDecision is recorded once per group this visor dialed with
	// DiversifyTransports set: its Reason is the route-choice trail (sibling
	// exclusions, candidate filtering, the chosen first hop), so a tunnel that
	// shares a first hop with its siblings says why, from `visor state`.
	MuxEventDialDecision = "dial_decision"
)

// Mux event initiators (MuxEvent.By).
const (
	MuxByLocal    = "local"
	MuxByRemote   = "remote"
	MuxByOperator = "operator"
	MuxByAdaptive = "adaptive"
	MuxByPolicy   = "policy"
)

// MuxEventRingSize is how many mux events the router keeps.
const MuxEventRingSize = 256

// muxEventsPerGroup is how many of a group's own events ride along in its
// MuxStats snapshot (`proxy mux info --json`) — enough to show the churn next
// to the legs without turning a per-second poll into a log dump.
const muxEventsPerGroup = 8

type muxEventRing struct {
	mu   sync.Mutex
	buf  []MuxEvent
	next int
	full bool
}

func (r *muxEventRing) add(e MuxEvent) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buf == nil {
		r.buf = make([]MuxEvent, MuxEventRingSize)
	}
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// snapshot returns the events oldest first.
func (r *muxEventRing) snapshot() []MuxEvent {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buf == nil {
		return nil
	}
	if !r.full {
		return append([]MuxEvent(nil), r.buf[:r.next]...)
	}
	out := make([]MuxEvent, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	out = append(out, r.buf[:r.next]...)
	return out
}

// forDesc returns the last n events belonging to desc, oldest first.
func (r *muxEventRing) forDesc(desc routing.RouteDescriptor, n int) []MuxEvent {
	if r == nil || n <= 0 {
		return nil
	}
	all := r.snapshot()
	out := make([]MuxEvent, 0, n)
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		if all[i].Desc.SrcPK != desc.SrcPK() || all[i].Desc.DstPK != desc.DstPK() ||
			all[i].Desc.SrcPort != desc.SrcPort() || all[i].Desc.DstPort != desc.DstPort() {
			continue
		}
		out = append(out, all[i])
	}
	// out is newest-first; reverse into the oldest-first order every other
	// event view uses.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// MuxEvents implements Router. Returns the route-group/mux-leg history,
// oldest first.
func (r *router) MuxEvents() []MuxEvent {
	return r.muxEvents.snapshot()
}

// noteMuxEvent stamps e with this route group's identity and records it on the
// router's ring. Nil-safe (a route group built outside a router — tests, the
// setup node — simply records nothing) and takes NO locks, so it is callable
// with or without rg.mu held.
func (rg *RouteGroup) noteMuxEvent(e MuxEvent) {
	if rg == nil || rg.muxEvents == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.App = rg.appName
	e.Desc = routing.RouteDescriptorFields{
		DstPK:   rg.desc.DstPK(),
		SrcPK:   rg.desc.SrcPK(),
		DstPort: rg.desc.DstPort(),
		SrcPort: rg.desc.SrcPort(),
	}
	rg.muxEvents.add(e)
}

// noteLegEvent is noteMuxEvent for a leg-scoped event: it fills the first-hop
// transport fields from tp (which may be nil for a leg whose transport is
// already gone) and the leg's recorded forward path from hops.
func (rg *RouteGroup) noteLegEvent(kind, reason, by string, idx, legs int, tp *transport.ManagedTransport, hops []routing.Hop) {
	e := MuxEvent{Event: kind, Reason: reason, By: by, LegIndex: idx, Legs: legs, Hops: hopInfos(hops)}
	if tp != nil {
		e.TpID = tp.Entry.ID
		e.TpType = string(tp.Entry.Type)
		e.Remote = tp.Remote()
	}
	rg.noteMuxEvent(e)
}

// legHopsLocked returns the recorded forward path for tpID. Callers MUST hold
// rg.mu (the unlocked counterpart is legHopsFor).
func (rg *RouteGroup) legHopsLocked(tpID uuid.UUID) []routing.Hop {
	if rg.legForwardHops == nil {
		return nil
	}
	return rg.legForwardHops[tpID]
}

// tpEntryID is tp.Entry.ID for a transport that may be nil.
func tpEntryID(tp *transport.ManagedTransport) uuid.UUID {
	if tp == nil {
		return uuid.UUID{}
	}
	return tp.Entry.ID
}

// setCloseReason records why the NEXT close of this route group happens, for
// the group_closed mux event. The close paths that know more than the close
// code (rule expiry, the keepalive write-failure collapse) call this just
// before Close(); close() consumes it. Has its own mutex so it is callable
// with or without rg.mu held.
func (rg *RouteGroup) setCloseReason(reason string) {
	if rg == nil {
		return
	}
	rg.closeReasonMu.Lock()
	rg.closeReason = reason
	rg.closeReasonMu.Unlock()
}

// takeCloseReason returns the reason set by setCloseReason, or fallback.
func (rg *RouteGroup) takeCloseReason(fallback string) string {
	rg.closeReasonMu.Lock()
	defer rg.closeReasonMu.Unlock()
	if rg.closeReason == "" {
		return fallback
	}
	return rg.closeReason
}

// hopInfos projects a route's hops into the display shape the rest of the mux
// views use (full PKs, per-hop transport type derived from the transport ID).
func hopInfos(hops []routing.Hop) []RouteHopInfo {
	if len(hops) == 0 {
		return nil
	}
	out := make([]RouteHopInfo, len(hops))
	for i, h := range hops {
		out[i] = RouteHopInfo{
			TpID:   h.TpID.String(),
			From:   h.From.String(),
			To:     h.To.String(),
			TpType: string(transport.TypeFromTransportID(h.TpID, h.From, h.To)),
		}
	}
	return out
}

// legCount is the number of legs the group currently holds. Callers must NOT
// hold rg.mu.
func (rg *RouteGroup) legCount() int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	return len(rg.tps)
}

// activatePinnedLeg promotes the leg riding tpID out of warm standby and
// mirrors the state to the peer. An operator-pinned leg (mux add / mux set) is
// meant to carry traffic now: aux legs otherwise enter warm standby whenever a
// rotation hook is wired (SetRotation), and with no policy engine behind that
// hook nothing ever promotes them — the second leg of a two-leg pinned set sat
// parked for every row of a bench, the latency band needing three legs to act.
// No-op for the primary, an unknown transport, or a leg already active.
func (rg *RouteGroup) activatePinnedLeg(tpID uuid.UUID) {
	if rg.mux == nil {
		return
	}
	rg.mu.Lock()
	idx := -1
	var tp *transport.ManagedTransport
	for i, t := range rg.tps {
		if t != nil && t.Entry.ID == tpID {
			idx, tp = i, t
			break
		}
	}
	rg.mu.Unlock()
	if idx <= 0 {
		return
	}
	// The OPERATOR overrides an adaptive park outright: pinning or re-widening a
	// leg is an explicit instruction, never something legParkMinHold may gate.
	// Dropping the record also means the next adaptive park starts a fresh hold.
	rg.clearAdaptivePark(tpID)
	// Unconditional: the leg's standby slot may not exist yet (the slice grows
	// lazily, see setLegStandby), so "not standby" here does not mean active.
	rg.mux.setLegStandby(idx, false)
	rg.sendLegState(idx, false)
	rg.noteLegEvent(MuxEventLegPromoted, "operator: pinned leg active", MuxByOperator, idx, rg.legCount(), tp, nil)
}
