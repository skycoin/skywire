// Package router — in-flight first-hop reservations for diversify dials.
//
// siblingRouteGroupExclusions answers "which first hops are taken?" from the
// route groups that already EXIST. That is blind to the dials still in flight,
// and a standby-pool fill is exactly the workload that runs several at once
// (setup.fill_inflight = 8, tunnel.audition_parallel = 3): at startup eight
// tunnels dialed inside one second each saw an empty exclusion set and each
// picked the same lowest-latency direct transport, so the pool came up eight
// deep on ONE first hop (#5125).
//
// A dial therefore CLAIMS its first hop — transport, peer and remote IP — for
// the duration of the setup, and a sibling dial to the same exit treats the
// claim exactly like a first hop a live route group holds. The claim is
// released when the dial returns, by which time its route group is registered
// and siblingRouteGroupExclusions can see it for itself.
package router

import (
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// dialFirstHopHold is one in-flight dial's claim on a first hop: the transport
// it leaves over, the peer that transport reaches and (where the transport
// exposes one) that peer's remote IP, so the same host behind a second public
// key is caught as well. The three fields mirror what
// siblingRouteGroupExclusions reports for an established group.
type dialFirstHopHold struct {
	tpID uuid.UUID
	peer cipher.PubKey
	ip   string
}

// dialFirstHopHolds is the per-router registry of those claims, keyed by
// destination (exit PK + port) so tunnels to different exits never contend.
// The zero value is ready to use.
type dialFirstHopHolds struct {
	mu     sync.Mutex
	held   map[string]map[uint64]dialFirstHopHold
	nextID uint64
}

// dialDstKey names the destination a set of claims belongs to. It matches the
// (rPK, rPort) pair siblingRouteGroupExclusions scans the route groups for.
func dialDstKey(rPK cipher.PubKey, rPort routing.Port) string {
	return fmt.Sprintf("%s:%d", rPK.Hex(), rPort)
}

// exclusions reports the first hops in-flight dials to dst have claimed, in the
// shape DialOptions carries them.
func (h *dialFirstHopHolds) exclusions(dst string) (ids []uuid.UUID, peers []cipher.PubKey, ips []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, hold := range h.held[dst] {
		ids = append(ids, hold.tpID)
		var zero cipher.PubKey
		if hold.peer != zero {
			peers = append(peers, hold.peer)
		}
		if hold.ip != "" {
			ips = append(ips, hold.ip)
		}
	}
	return ids, peers, ips
}

// claim reserves hold for dst unless another in-flight dial to the same exit
// already holds its transport, its peer or its remote IP. The test and the
// insert happen under one lock, which is the whole point: two dials that both
// read an empty exclusion set and both chose the same transport cannot both win
// here, so the loser re-picks instead of laying a second tunnel on that hop.
//
// Returns the release func and whether the claim took. The release func is safe
// to call more than once.
func (h *dialFirstHopHolds) claim(dst string, hold dialFirstHopHold) (func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var zero cipher.PubKey
	for _, other := range h.held[dst] {
		if other.tpID == hold.tpID {
			return nil, false
		}
		if hold.peer != zero && other.peer == hold.peer {
			return nil, false
		}
		if hold.ip != "" && other.ip == hold.ip {
			return nil, false
		}
	}

	if h.held == nil {
		h.held = make(map[string]map[uint64]dialFirstHopHold)
	}
	if h.held[dst] == nil {
		h.held[dst] = make(map[uint64]dialFirstHopHold)
	}
	h.nextID++
	id := h.nextID
	h.held[dst][id] = hold

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.held[dst], id)
			if len(h.held[dst]) == 0 {
				delete(h.held, dst)
			}
		})
	}, true
}

// inFlightFirstHopExclusions is the router-level read: the first hops dials to
// (rPK, rPort) are currently setting up over. Fed into a diversify dial's
// DialOptions alongside the live route groups' own first hops, so every path
// that consults them — freeFirstHops, firstHopExcluded, the K-candidate race,
// the RSN oracle and the finder's disjoint filter — honors them without
// knowing they exist.
func (r *router) inFlightFirstHopExclusions(rPK cipher.PubKey, rPort routing.Port) (ids []uuid.UUID, peers []cipher.PubKey, ips []string) {
	return r.firstHopHolds.exclusions(dialDstKey(rPK, rPort))
}

// claimFirstHop reserves the first hop of hops for a dial to (rPK, rPort).
// A path with no hops claims nothing and succeeds, so a caller need not special
// case it.
func (r *router) claimFirstHop(rPK cipher.PubKey, rPort routing.Port, hops []routing.Hop) (func(), bool) {
	if len(hops) == 0 {
		return func() {}, true
	}
	hold := dialFirstHopHold{tpID: hops[0].TpID, peer: hops[0].To}
	if r.tm != nil {
		if tp := r.tm.Transport(hops[0].TpID); tp != nil {
			hold.ip = tp.RemoteIP()
		}
	}
	return r.firstHopHolds.claim(dialDstKey(rPK, rPort), hold)
}
