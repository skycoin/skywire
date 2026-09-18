// Package routing pkg/routing/batch_route.go c1-net-routing
//
// The BATCHED half of the route-setup protocol.
//
// A multiplexed dial wants N routes to ONE destination at once: a standby-pool
// fill, `--tunnels N`, and every aux mux leg are all "give me N disjoint routes
// to the same exit". Until now each of those was its own BidirectionalRoute in
// its own SetupRPCGateway.DialRouteGroup request, so the setup node re-dialed
// the source and the destination once PER ROUTE, reserved route IDs from each
// of them once PER ROUTE, and the initiator paid the full setup latency (p50
// 0.6-2.1 s, max 7.8 s on the live deployment) N times.
//
// BidirectionalRouteBatch carries all N in one message. The setup node can then
// coalesce the per-hop work — one id-reservation RPC per DISTINCT hop covering
// every route that traverses it, one intermediary-rule install per distinct hop
// carrying that hop's rules for every route — and answer with one result per
// route. Partial success is first class: a batch where one route's intermediate
// is unreachable still returns the other N-1 installed.
//
// The batch form is NOT a flag: a client sends it only when the setup node has
// advertised CapBatchRouteSetup (see the setup gateway's Capabilities RPC and
// HealthCheck reply). A setup node that predates this message never sees one.
package routing

import (
	"errors"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
)

// MaxBatchRoutes bounds one batched setup request. It is a protocol limit, not
// a tuning knob: it caps the work one RPC handler can queue on the setup node
// (each route holds route-ID reservations on every hop it traverses until the
// batch resolves) and bounds the reply size. A caller wanting more routes sends
// more batches.
const MaxBatchRoutes = 32

// Errors returned by BidirectionalRouteBatch.Check.
var (
	ErrBatchEmpty          = errors.New("route setup batch carries no routes")
	ErrBatchTooLarge       = fmt.Errorf("route setup batch carries more than %d routes", MaxBatchRoutes)
	ErrBatchDuplicateID    = errors.New("route setup batch has a duplicate correlation id")
	ErrBatchMixedEndpoints = errors.New("route setup batch mixes source/destination pairs")
)

// BatchRouteRequest is one member of a batched setup request. ID is assigned by
// the CALLER and is echoed on the matching BatchRouteResult — the results are
// not required to come back in request order, and a partial batch returns fewer
// results than it was sent.
type BatchRouteRequest struct {
	ID    uint32
	Route BidirectionalRoute
}

// BatchRouteResult is one route's outcome. Error is the empty string on
// success; a non-empty Error means Rules is meaningless for that route and the
// caller falls back to its own retry (a single DialRouteGroup, a different
// candidate path) for that member alone.
//
// The error travels as a STRING rather than an error value because this struct
// crosses the setup RPC: the wire codec has no error type, and the initiator
// only ever logs / classifies the text.
type BatchRouteResult struct {
	ID    uint32
	Rules EdgeRules
	Error string
}

// Failed reports whether this member did not install.
func (r BatchRouteResult) Failed() bool { return r.Error != "" }

// BidirectionalRouteBatch is up to MaxBatchRoutes bidirectional routes that
// share one source and one destination visor, set up in a single request.
//
// Same source and same destination is a REQUIREMENT, not a convention: the
// coalescing the setup node performs (one id reservation per distinct hop, one
// rule install per distinct hop) is only a saving because the source and the
// destination are common to every member, and a mixed batch would let one
// caller reserve route IDs on a peer it never names in a route it owns.
type BidirectionalRouteBatch struct {
	Routes []BatchRouteRequest
}

// BidirectionalRouteBatchReply carries one result per route the setup node
// attempted. A member missing from Results was never attempted (the batch was
// truncated by the node's own limit) and the caller must treat it as failed.
type BidirectionalRouteBatchReply struct {
	Results []BatchRouteResult
}

// Check validates the batch as a whole: non-empty, within MaxBatchRoutes,
// correlation ids unique, every member a valid BidirectionalRoute, and every
// member sharing the first member's source and destination.
//
// It deliberately does NOT reject a batch whose members repeat a path — two
// identical paths are two distinct route groups with their own route-ID chains,
// which is a legitimate (if unusual) thing to ask for.
func (b *BidirectionalRouteBatch) Check() error {
	if len(b.Routes) == 0 {
		return ErrBatchEmpty
	}
	if len(b.Routes) > MaxBatchRoutes {
		return ErrBatchTooLarge
	}
	seen := make(map[uint32]struct{}, len(b.Routes))
	src := b.Routes[0].Route.Desc.SrcPK()
	dst := b.Routes[0].Route.Desc.DstPK()
	for i := range b.Routes {
		m := &b.Routes[i]
		if _, dup := seen[m.ID]; dup {
			return fmt.Errorf("%w: %d", ErrBatchDuplicateID, m.ID)
		}
		seen[m.ID] = struct{}{}
		if err := m.Route.Check(); err != nil {
			return fmt.Errorf("route %d: %w", m.ID, err)
		}
		if m.Route.Desc.SrcPK() != src || m.Route.Desc.DstPK() != dst {
			return ErrBatchMixedEndpoints
		}
	}
	return nil
}

// Src returns the batch's common source visor. Only meaningful after Check.
func (b *BidirectionalRouteBatch) Src() cipher.PubKey {
	if len(b.Routes) == 0 {
		return cipher.PubKey{}
	}
	return b.Routes[0].Route.Desc.SrcPK()
}

// Dst returns the batch's common destination visor. Only meaningful after Check.
func (b *BidirectionalRouteBatch) Dst() cipher.PubKey {
	if len(b.Routes) == 0 {
		return cipher.PubKey{}
	}
	return b.Routes[0].Route.Desc.DstPK()
}

// Hops returns every forward and reverse hop list in the batch, in member
// order (forward then reverse per member) — the exact shape NewIDReserver takes,
// so one reserver covers the whole batch and each distinct hop is dialed and
// asked for its route IDs exactly once.
func (b *BidirectionalRouteBatch) Hops() [][]Hop {
	out := make([][]Hop, 0, 2*len(b.Routes))
	for i := range b.Routes {
		out = append(out, b.Routes[i].Route.Forward, b.Routes[i].Route.Reverse)
	}
	return out
}

// DistinctHopPKs counts the visors a batch touches — source, destination and
// every intermediate, each once. It is what the per-hop coalescing saving is
// measured against: without a batch the setup node does this much work PER
// ROUTE, with one it does it once for the batch.
func (b *BidirectionalRouteBatch) DistinctHopPKs() int {
	seen := make(map[cipher.PubKey]struct{}, 4*len(b.Routes))
	for _, hops := range b.Hops() {
		if len(hops) == 0 {
			continue
		}
		seen[hops[0].From] = struct{}{}
		for _, h := range hops {
			seen[h.To] = struct{}{}
		}
	}
	return len(seen)
}
