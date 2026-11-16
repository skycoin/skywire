// Package router pkg/router/leg_gone.go c2-net-routing
//
// Telling the peer that a leg died.
//
// A leg that dies past its first hop on one side looks healthy on the other:
// that end's own transport is up, and its liveness probe needs three missed
// echoes at leg.liveness_interval (90 s or more by default) to rule. Until then
// it keeps striping data and retransmits into the dead path. A download that
// lost its active leg measured about 50 KB/s for over a minute while the
// sender still listed the dead leg as alive.
//
// The side that prunes the leg knows at once, so it says so on every surviving
// leg. The notice names the dead leg by the peer's own first-hop transport,
// the hop of the leg's route that touches the peer.
package router

import (
	"context"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// peerFirstHopsOf maps local leg transports to the peer's first-hop transport
// of the same legs. Legs whose route is unknown are skipped.
func (rg *RouteGroup) peerFirstHopsOf(localIDs []uuid.UUID) []uuid.UUID {
	far := rg.farEndPK()
	rg.mu.Lock()
	defer rg.mu.Unlock()
	out := make([]uuid.UUID, 0, len(localIDs))
	for _, id := range localIDs {
		idx := rg.legIndexLocked(id)
		if idx < 0 {
			continue
		}
		if peer := peerFirstHop(rg.legs[idx].hops, far); peer != uuid.Nil {
			out = append(out, peer)
		}
	}
	return out
}

// peerFirstHop returns the transport of the hop in hops that touches far.
func peerFirstHop(hops []routing.Hop, far cipher.PubKey) uuid.UUID {
	for i := len(hops) - 1; i >= 0; i-- {
		if hops[i].To == far || hops[i].From == far {
			return hops[i].TpID
		}
	}
	return uuid.Nil
}

// tellPeerLegsGone sends one leg-gone notice per dead leg on every surviving
// leg. Only a peer that negotiated CapLegRehome reads the control band.
func (rg *RouteGroup) tellPeerLegsGone(peerIDs []uuid.UUID) {
	if len(peerIDs) == 0 || rg.mux == nil || !rg.mux.legRehomeEnabled || rg.isClosed() {
		return
	}
	rg.mu.Lock()
	legs := make([]rehomeLeg, 0, len(rg.legs))
	for _, lg := range rg.legs {
		if lg.fwd != nil && lg.tp != nil && !lg.tp.Closed() {
			legs = append(legs, rehomeLeg{fwd: lg.fwd, tp: lg.tp})
		}
	}
	rg.mu.Unlock()
	for _, id := range peerIDs {
		for _, lg := range legs {
			pkt, err := rg.mux.wrapControl(lg.fwd.NextRouteID(), routing.EncodeLegGoneFrame(id))
			if err != nil {
				continue
			}
			if err := rg.writePacket(context.Background(), lg.tp, pkt, lg.fwd.KeyRouteID()); err != nil {
				rg.logger.WithError(err).Debugf("leg-gone: notice for %v not sent on %v", id, lg.tp.Entry.ID)
				continue
			}
			globalMuxCounters.legGoneSent.Add(1)
		}
	}
}

// handleLegGone prunes the leg on transport tpID that the peer reported dead.
// It does not notify back, and a repeat notice finds no leg and does nothing.
func (rg *RouteGroup) handleLegGone(tpID uuid.UUID) {
	globalMuxCounters.legGoneReceived.Add(1)
	if n := rg.removeLegsByID([]uuid.UUID{tpID}, "peer reported the leg dead past its first hop", "peer-leg-gone"); n > 0 {
		globalMuxCounters.legGonePruned.Add(1)
		rg.signalRotate()
		rg.maybeSelfHeal()
	}
}
