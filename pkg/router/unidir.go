// Package router pkg/router/unidir.go c2-net-routing
package router

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// Unidirectional per-leg send selection (CapUniDir). Each end restricts its OWN
// send to legs matching its direction, so the two directions ride disjoint legs:
//
//   - DEFAULT: the initiator (upload / forward) sends on the DIRECT (1-hop) leg;
//     the acceptor (download / reverse) sends on the MULTIHOP mux legs. So the
//     light direction takes the low-latency direct transport and the heavy
//     direction aggregates over the mux — instead of both directions striping
//     every leg (the head-of-line churn the confound-free telemetry exposed).
//   - FLIPPED: the mapping is swapped (initiator sends on multihop, acceptor on
//     direct) so the HEAVY direction gets the mux when upload outweighs download.
//
// Both ends decide locally from their role + each leg's directness (no per-packet
// signaling); a flip is coordinated out of band. If no leg matches the wanted
// direction, selection falls back to any ready leg so a send never fails.

// setDirectional enables unidirectional send selection and records this end's
// role and the route-group endpoints (used to tell a direct leg from a multihop
// one). Called once at handshake when CapUniDir is negotiated.
func (m *routeMux) setDirectional(initiator bool, dst, src cipher.PubKey) {
	m.legMu.Lock()
	m.directional = true
	m.initiator = initiator
	m.dstPK = dst
	m.srcPK = src
	m.legMu.Unlock()
}

// setFlipped swaps the direction→leg-class mapping (heavy direction gets the
// mux). No-op unless directional. Returns true if the state changed. The bool is
// set both ways by the flip controller (2b); it is only exercised with true so
// far in tests.
//
//nolint:unparam
func (m *routeMux) setFlipped(flipped bool) (changed bool) {
	m.legMu.Lock()
	if m.directional && m.flipped != flipped {
		m.flipped = flipped
		changed = true
	}
	m.legMu.Unlock()
	return changed
}

// dirConfig snapshots the directional state under legMu so the send path reads it
// once and then uses lock-free pure helpers (avoids re-locking legMu per leg).
func (m *routeMux) dirConfig() (directional, wantDirect bool, dst, src cipher.PubKey) {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	// This end sends on DIRECT legs when it is the forward sender: the initiator by
	// default, or the acceptor when flipped.
	wantDirect = m.initiator != m.flipped
	return m.directional, wantDirect, m.dstPK, m.srcPK
}

// legIsDirect reports whether a leg's transport goes straight to a route-group
// endpoint (a 1-hop / direct leg) rather than through an intermediary.
func legIsDirect(tp *transport.ManagedTransport, dst, src cipher.PubKey) bool {
	if tp == nil {
		return false
	}
	r := tp.Remote()
	return r == dst || r == src
}

// dirRestrict reports whether direction filtering should be enforced this pick:
// true only when directional AND at least one READY leg matches the wanted
// direction. When no matching leg is available it returns false so selection
// falls back to any ready leg (a send is never dropped for lack of a
// direction-matching leg).
func (m *routeMux) dirRestrict(tps []*transport.ManagedTransport, wantDirect bool, dst, src cipher.PubKey) bool {
	for idx, tp := range tps {
		if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) && legIsDirect(tp, dst, src) == wantDirect {
			return true
		}
	}
	return false
}
