// Package router pkg/router/leg_identity.go c2-net-routing
package router

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// A leg index is only valid while rg.mu is held. Across a lock release a leg is
// named by its transport ID and resolved again by the helpers in this file.

// legIndexLocked returns the index of the leg on transport id, or -1. Caller
// holds rg.mu.
func (rg *RouteGroup) legIndexLocked(id uuid.UUID) int {
	if id == uuid.Nil {
		return -1
	}
	for i := range rg.legs {
		if tp := rg.legs[i].tp; tp != nil && tp.Entry.ID == id {
			return i
		}
	}
	return -1
}

// legIDsLocked returns each leg's transport ID in leg order, uuid.Nil for a leg
// with no transport. Caller holds rg.mu.
func (rg *RouteGroup) legIDsLocked() []uuid.UUID {
	ids := make([]uuid.UUID, len(rg.legs))
	for i := range rg.legs {
		ids[i] = tpEntryID(rg.legs[i].tp)
	}
	return ids
}

// legIDsAt translates indices into the leg snapshot shown to turn them into
// transport IDs. Indices outside the snapshot and repeats are dropped.
func legIDsAt(shown []uuid.UUID, indices []int) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(indices))
	for _, i := range indices {
		if i < 0 || i >= len(shown) || shown[i] == uuid.Nil || slices.Contains(ids, shown[i]) {
			continue
		}
		ids = append(ids, shown[i])
	}
	return ids
}

// legByID returns the current index and transport of the leg on id.
func (rg *RouteGroup) legByID(id uuid.UUID) (int, *transport.ManagedTransport) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	idx := rg.legIndexLocked(id)
	if idx < 0 {
		return -1, nil
	}
	return idx, rg.legs[idx].tp
}

// legStandbyByID reports whether the leg on id is in warm standby. ok is false
// when the leg is gone.
func (rg *RouteGroup) legStandbyByID(id uuid.UUID) (standby, ok bool) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	idx := rg.legIndexLocked(id)
	if idx < 0 || rg.mux == nil {
		return false, false
	}
	return rg.mux.isLegStandby(idx), true
}

// setLegStandbyByID parks or promotes the leg on id and returns where it is
// now. ok is false, and nothing changes, when the leg is gone or is the primary.
func (rg *RouteGroup) setLegStandbyByID(id uuid.UUID, standby bool) (idx int, tp *transport.ManagedTransport, ok bool) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	idx = rg.legIndexLocked(id)
	if idx <= 0 || rg.mux == nil {
		return idx, nil, false
	}
	rg.mux.setLegStandby(idx, standby)
	return idx, rg.legs[idx].tp, true
}

// sendLegStateByID tells the peer the leg on id is standby or active, on that
// leg. It does nothing when the leg is gone or the peer lacks CapLegState.
func (rg *RouteGroup) sendLegStateByID(id uuid.UUID, standby bool) {
	if rg.mux == nil || !rg.mux.legStateEnabled {
		return
	}
	rg.mu.Lock()
	var tp *transport.ManagedTransport
	var rule routing.Rule
	idx := rg.legIndexLocked(id)
	if idx >= 0 {
		tp, rule = rg.legs[idx].tp, rg.legs[idx].fwd
	}
	rg.mu.Unlock()
	if tp == nil || rule == nil {
		return
	}
	packet := routing.MakeLegStatePacket(rule.NextRouteID(), standby)
	if err := rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID()); err != nil {
		rg.logger.WithError(err).Debugf("LegState: failed to signal leg %d", idx)
	}
}

// setLegGroupsFor hands the mux a grouping computed over the leg snapshot
// shown. A grouping for a leg set that has since changed is dropped.
func (rg *RouteGroup) setLegGroupsFor(shown []uuid.UUID, groups []int) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if !slices.Equal(shown, rg.legIDsLocked()) {
		groups = nil
	}
	rg.mux.SetLegGroups(groups)
}

// syncMuxLegsLocked gives the mux a record for every leg, named by its
// transport ID. Call it after every append to rg.legs. Caller holds rg.mu.
func (rg *RouteGroup) syncMuxLegsLocked() {
	if rg.mux != nil {
		rg.mux.syncLegs(rg.legIDsLocked())
	}
}

// compactMuxLocked drops indices from the mux after rg.legs lost the same legs.
// A parked leg left in the primary slot goes active, so the peer is told.
func (rg *RouteGroup) compactMuxLocked(indices ...int) {
	if rg.mux == nil || !rg.mux.removeLegs(indices...) || len(rg.legs) == 0 {
		return
	}
	tp := rg.legs[0].tp
	id := tpEntryID(tp)
	rg.forgetPeerPark(id)
	rg.noteLegEvent(MuxEventLegPromoted, "parked leg took the primary slot", MuxByAdaptive,
		0, len(rg.legs), tp, nil)
	go rg.sendLegStateByID(id, false)
}
