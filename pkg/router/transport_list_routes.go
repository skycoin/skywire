//go:build !tinygo || (js && wasm)

// Package router pkg/router/transport_list_routes.go c2-net-routing
package router

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// TransportListFetcher fetches another visor's signed transport list over a
// skywire transport (GET /transports on its :80). The visor implements it.
type TransportListFetcher interface {
	FetchTransportList(ctx context.Context, pk cipher.PubKey) (*transport.SignedList, error)
}

// SetTransportListFetcher installs the fetcher that lets route calculation
// read other visors' own lists instead of TPD's copy of the graph.
func (r *router) SetTransportListFetcher(f TransportListFetcher) {
	r.tpListMu.Lock()
	r.tpListFetcher = f
	r.tpListMu.Unlock()
}

func (r *router) transportListFetcher() TransportListFetcher {
	r.tpListMu.Lock()
	defer r.tpListMu.Unlock()
	return r.tpListFetcher
}

// Bounds on one 3-hop calculation: how many first-hop neighbors' lists it
// reads, how many at once, and how long it may take in all.
const (
	listRouteMaxNeighbors = 32
	listRouteParallel     = 8
	listRouteTimeout      = 5 * time.Second
)

var errListRoutesInert = errors.New("transport-list routes: no fetcher wired")

// hopEnd is the transport chosen for one end of a route through a peer.
type hopEnd struct {
	id        uuid.UUID
	tpType    tptypes.Type
	latencyMs float64
}

// firstHopsByPeer picks the best live non-DMSG transport from src to each
// peer, leaving out what opts excludes.
func firstHopsByPeer(src, dst cipher.PubKey, localTps []oracleLocalTp, opts *DialOptions) map[cipher.PubKey]hopEnd {
	excludeIntermediates := map[cipher.PubKey]struct{}{}
	excludeTpIDs := map[uuid.UUID]struct{}{}
	excludeFirstHopPeers := map[cipher.PubKey]struct{}{}
	excludeFirstHopIPs := map[string]struct{}{}
	if opts != nil {
		for _, pk := range opts.ExcludeIntermediatePKs {
			excludeIntermediates[pk] = struct{}{}
		}
		for _, id := range opts.ExcludeTransportIDs {
			excludeTpIDs[id] = struct{}{}
		}
		for _, pk := range opts.ExcludeFirstHopPeers {
			excludeFirstHopPeers[pk] = struct{}{}
		}
		for _, ip := range opts.ExcludeFirstHopIPs {
			excludeFirstHopIPs[ip] = struct{}{}
		}
	}
	out := map[cipher.PubKey]hopEnd{}
	for _, tp := range localTps {
		if tp.tpType == tptypes.DMSG {
			continue
		}
		if _, hit := excludeTpIDs[tp.id]; hit {
			continue
		}
		peer := tp.remotePK
		if peer == src || peer == dst {
			continue
		}
		if _, hit := excludeIntermediates[peer]; hit {
			continue
		}
		if _, hit := excludeFirstHopPeers[peer]; hit {
			continue
		}
		if tp.remoteIP != "" {
			if _, hit := excludeFirstHopIPs[tp.remoteIP]; hit {
				continue
			}
		}
		if cur, ok := out[peer]; !ok || tptypes.TypePreference(tp.tpType) < tptypes.TypePreference(cur.tpType) {
			out[peer] = hopEnd{id: tp.id, tpType: tp.tpType, latencyMs: tp.latencyMs}
		}
	}
	return out
}

// lastHopsByPeer picks the best non-DMSG, non-setup transport each peer has
// to dst, from dst's own list.
func lastHopsByPeer(src, dst cipher.PubKey, dstEntries []*transport.Entry) map[cipher.PubKey]hopEnd {
	out := map[cipher.PubKey]hopEnd{}
	for _, e := range dstEntries {
		if e == nil || e.Type == tptypes.DMSG || e.Label == transport.LabelSetup {
			continue
		}
		peer := e.RemoteEdge(dst)
		if peer == src || peer == dst {
			continue
		}
		if cur, ok := out[peer]; !ok || tptypes.TypePreference(e.Type) < tptypes.TypePreference(cur.tpType) {
			out[peer] = hopEnd{id: e.ID, tpType: e.Type, latencyMs: e.Latency}
		}
	}
	return out
}

// compute3HopRoutes finds routes src -> A -> B -> dst from three signed
// sources: src's own transports, each first-hop peer A's list (the A-B edge)
// and dst's list (the B-dst edge). Excluded intermediates are skipped in
// either middle position. Legs come best first: fewest, then most preferred
// transport types, then lowest first-hop latency.
func compute3HopRoutes(
	src, dst cipher.PubKey,
	localTps []oracleLocalTp,
	neighborLists map[cipher.PubKey][]*transport.Entry,
	dstEntries []*transport.Entry,
	opts *DialOptions,
) ([]twoHopLeg, error) {
	if src == dst {
		return nil, errors.New("transport-list routes: src == dst")
	}
	first := firstHopsByPeer(src, dst, localTps, opts)
	last := lastHopsByPeer(src, dst, dstEntries)
	excluded := map[cipher.PubKey]struct{}{}
	if opts != nil {
		for _, pk := range opts.ExcludeIntermediatePKs {
			excluded[pk] = struct{}{}
		}
	}

	type cand struct {
		leg  twoHopLeg
		cost int
		lat  float64
	}
	var cands []cand
	for a, s := range first {
		for _, e := range neighborLists[a] {
			if e == nil || e.Type == tptypes.DMSG || e.Label == transport.LabelSetup {
				continue
			}
			b := e.RemoteEdge(a)
			if b == src || b == dst || b == a {
				continue
			}
			if _, hit := excluded[b]; hit {
				continue
			}
			d, ok := last[b]
			if !ok {
				continue
			}
			cost := tptypes.TypePreference(s.tpType)
			for _, t := range []tptypes.Type{e.Type, d.tpType} {
				if p := tptypes.TypePreference(t); p > cost {
					cost = p
				}
			}
			fwd := []routing.Hop{
				{TpID: s.id, From: src, To: a, Latency: s.latencyMs},
				{TpID: e.ID, From: a, To: b, Latency: e.Latency},
				{TpID: d.id, From: b, To: dst, Latency: d.latencyMs},
			}
			cands = append(cands, cand{leg: twoHopLeg{Intermediate: a, Forward: fwd, Reverse: reverseHops(fwd)}, cost: cost, lat: s.latencyMs})
		}
	}
	if len(cands) == 0 {
		return nil, errors.New("transport-list routes: no 3-hop path through the neighbors' lists")
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].cost != cands[j].cost {
			return cands[i].cost < cands[j].cost
		}
		li, lj := cands[i].lat, cands[j].lat
		if (li > 0) != (lj > 0) {
			return li > 0
		}
		if li != lj {
			return li < lj
		}
		return cands[i].leg.Forward[1].TpID.String() < cands[j].leg.Forward[1].TpID.String()
	})
	legs := make([]twoHopLeg, len(cands))
	for i, c := range cands {
		legs[i] = c.leg
	}
	return legs, nil
}

// localRouteTps snapshots this visor's live transports for route building.
func (r *router) localRouteTps(src cipher.PubKey) []oracleLocalTp {
	var out []oracleLocalTp
	if r.tm == nil {
		return out
	}
	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil || tp.IsClosed() || tp.Entry.Label == transport.LabelSetup {
			return true
		}
		out = append(out, oracleLocalTp{
			id:        tp.Entry.ID,
			remotePK:  tp.Entry.RemoteEdge(src),
			tpType:    tp.Entry.Type,
			latencyMs: tp.GetLatency(),
			remoteIP:  tp.RemoteIP(),
		})
		return true
	})
	return out
}

// listRoutes3Hop builds a 3-hop route from the neighbors' and the
// destination's own signed lists, read over transports. dst's list comes
// from the fetcher, or from the destination oracle when dst cannot be
// reached over a transport.
func (r *router) listRoutes3Hop(ctx context.Context, log *logging.Logger, src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	f := r.transportListFetcher()
	if f == nil {
		return nil, nil, errListRoutesInert
	}
	r.routeSource.listAttempts.Add(1)
	ctx, cancel := context.WithTimeout(ctx, listRouteTimeout)
	defer cancel()

	return r.listRoutes3HopFrom(ctx, log, f, src, dst, r.localRouteTps(src), opts)
}

// listRoutes3HopFrom is listRoutes3Hop over the given first-hop transports.
func (r *router) listRoutes3HopFrom(ctx context.Context, log *logging.Logger, f TransportListFetcher, src, dst cipher.PubKey, localTps []oracleLocalTp, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	first := firstHopsByPeer(src, dst, localTps, opts)
	if len(first) == 0 {
		r.routeSource.listNoFirstHop.Add(1)
		return nil, nil, errors.New("transport-list routes: no usable first hop")
	}

	var dstEntries []*transport.Entry
	if l, ferr := f.FetchTransportList(ctx, dst); ferr == nil {
		dstEntries = l.Transports()
	} else if o := r.dstTransportOracle(); o != nil {
		if dstEntries, err = o.DstTransports(ctx, src, dst); err != nil {
			r.routeSource.listNoDstList.Add(1)
			return nil, nil, err
		}
	} else {
		r.routeSource.listNoDstList.Add(1)
		return nil, nil, ferr
	}

	peers := make([]cipher.PubKey, 0, len(first))
	for pk := range first {
		peers = append(peers, pk)
	}
	sort.Slice(peers, func(i, j int) bool {
		li, lj := first[peers[i]].latencyMs, first[peers[j]].latencyMs
		if (li > 0) != (lj > 0) {
			return li > 0
		}
		return li < lj
	})
	if len(peers) > listRouteMaxNeighbors {
		peers = peers[:listRouteMaxNeighbors]
	}
	lists := make(map[cipher.PubKey][]*transport.Entry, len(peers))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, listRouteParallel)
	for _, pk := range peers {
		wg.Add(1)
		go func(pk cipher.PubKey) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r.routeSource.listFetches.Add(1)
			l, lerr := f.FetchTransportList(ctx, pk)
			if lerr != nil {
				r.routeSource.listFetchFails.Add(1)
				return
			}
			mu.Lock()
			lists[pk] = l.Transports()
			mu.Unlock()
		}(pk)
	}
	wg.Wait()

	if len(lists) == 0 {
		r.routeSource.listNoNeighborList.Add(1)
		return nil, nil, errors.New("transport-list routes: no neighbor answered with its list")
	}
	legs, err := compute3HopRoutes(src, dst, localTps, lists, dstEntries, opts)
	if err != nil {
		r.routeSource.listNoPath.Add(1)
		return nil, nil, err
	}
	r.routeSource.listRoutes.Add(1)
	leg := legs[0]
	log.WithField("lists", len(lists)).WithField("candidates", len(legs)).
		Infof("3-hop route from transport lists via %s and %s (no TPD)", leg.Forward[0].To, leg.Forward[1].To)
	if opts != nil {
		opts.note("transport-list: 3-hop via %s of %d candidates", leg.Intermediate.String(), len(legs))
	}
	return leg.Forward, leg.Reverse, nil
}
