//go:build !tinygo || (js && wasm)

// Package router pkg/router/setup_batch.go c2-net-routing
//
// The setup-node side of the batched route-setup protocol
// (routing/batch_route.go): install N routes to ONE destination with the
// per-hop work coalesced.
//
// What coalescing actually removes. A single CreateRouteGroup for a 2-hop route
// S->I->D does, per route:
//
//	3 dials / pool borrows (S, I, D)   -> one id-reservation RPC each
//	1 AddIntermediaryRules RPC on I
//	1 AddEdgeRules RPC on D
//
// Eight such routes to the same exit over eight different intermediates cost 8
// setup requests and 8*5 = 40 router RPCs, of which 16 are the SAME two peers
// (S and D) asked for route IDs eight times each.
//
// CreateRouteGroupBatch builds ONE IDReserver over every member's forward and
// reverse hop lists. NewIDReserver already tallies "how many route IDs does
// this PK owe" across all the paths it is given, so S and D are dialed once
// and asked once — for all eight routes' worth of IDs in a single RPC. The
// intermediary-rule broadcast is merged the same way: the rules of every member
// that traverses a given hop go out in one AddIntermediaryRules call to that
// hop, so a batch whose members SHARE an intermediate (the common shape once
// the pool grows past the count of distinct first hops) collapses those too.
//
// The 8-route fill above becomes 1 setup request + 10 id reservations (S, D,
// I1..I8) + 8 intermediary installs + 8 destination edge installs = 27 RPCs
// against 48, and the two peers common to every route are dialed twice instead
// of sixteen times.
//
// Partial success is the contract. A member whose intermediate is unreachable,
// whose route fails Check, or whose destination edge install is refused comes
// back with an Error string while its siblings come back installed — and only
// that member's already-installed rules are torn down, from its own tracker.
// The only all-or-nothing step is the shared id reservation: if the source or
// the destination cannot be reached at all there is no batch to run, and every
// member fails with that one error.
package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// batchMember is one admitted route plus everything the batch computed for it.
type batchMember struct {
	id       uint32
	route    routing.BidirectionalRoute
	initEdge routing.EdgeRules
	respEdge routing.EdgeRules

	// interByAddr is this member's OWN intermediary rules, keyed by the
	// "pk:port" address they install on. The batch merges these across members
	// into one call per address; this copy is what gets tracked for teardown
	// and what attributes a per-hop failure back to the right members.
	interByAddr map[string][]routing.Rule

	// installed tracks only THIS member's successfully installed rules, so a
	// member that fails late is torn down without disturbing its siblings.
	installed *installedRules

	err string
}

// fail records the member's first error. Later errors are ignored — the first
// one is the cause, the rest are consequences.
func (m *batchMember) fail(format string, a ...interface{}) {
	if m.err == "" {
		m.err = fmt.Sprintf(format, a...)
	}
}

// result renders the member as a wire result.
func (m *batchMember) result() routing.BatchRouteResult {
	if m.err != "" {
		return routing.BatchRouteResult{ID: m.id, Error: m.err}
	}
	return routing.BatchRouteResult{ID: m.id, Rules: m.initEdge}
}

// BatchSavings is what one batch actually saved, for the setup node's /stats.
// Coalesced is the number of per-hop RPCs this batch's shape would have cost as
// N separate requests; Issued is what it cost as one. Both count the
// id-reservation and intermediary-install RPCs — the two the batch merges — and
// exclude the per-member destination edge install, which is not merged.
type BatchSavings struct {
	Routes    int
	Issued    int
	Coalesced int
}

// Saved is the per-hop RPC count the coalescing removed.
func (s BatchSavings) Saved() int {
	if s.Coalesced <= s.Issued {
		return 0
	}
	return s.Coalesced - s.Issued
}

// CreateRouteGroupBatch installs every route in the batch, coalescing the
// per-hop work, and returns one result per member in request order.
//
// It never returns a short slice for a non-empty batch: a failure that kills the
// whole batch (malformed batch, or a shared id reservation that could not be
// made) is reported as that error on every member, so the caller always learns
// the fate of every route it asked for.
func CreateRouteGroupBatch(
	ctx context.Context,
	dialer network.Dialer,
	pool *ClientPool,
	batch routing.BidirectionalRouteBatch,
	metrics setupmetrics.Metrics,
) (results []routing.BatchRouteResult, savings BatchSavings) {
	if len(batch.Routes) == 0 {
		return nil, BatchSavings{}
	}

	src, dst := batch.Src(), batch.Dst()
	log := logging.MustGetLogger(fmt.Sprintf("batch:%s->%s", src, dst))

	members := make([]*batchMember, 0, len(batch.Routes))
	for i := range batch.Routes {
		members = append(members, &batchMember{
			id:          batch.Routes[i].ID,
			route:       batch.Routes[i].Route,
			interByAddr: make(map[string][]routing.Rule),
			installed:   newInstalledRules(),
		})
	}
	savings.Routes = len(members)

	collector, haveCollector := metrics.(*setupmetrics.Collector)
	if haveCollector {
		collector.RecordSetupKind(setupmetrics.SetupKindBatch, len(members))
	}

	// Per-member admission: an invalid route or an open circuit breaker takes
	// that member out of the batch without touching the rest. Every member that
	// takes a recording closure has it resolved exactly once in the deferred
	// sweep below — a probe an aborted batch left in flight would block its
	// breaker's only recovery slot until the 30-minute force reset.
	admitted := make([]*batchMember, 0, len(members))
	finishers := make([]func(*error), len(members))
	for i, m := range members {
		if err := m.route.Check(); err != nil {
			m.fail("invalid route: %v", err)
			continue
		}
		if haveCollector {
			probes := &setupmetrics.ProbeHolder{}
			finishers[i] = collector.RecordRouteContextProbes(
				ctx, m.route.Desc.SrcPK(), m.route.Desc.DstPK(), len(m.route.Forward), probes)
			if ok, reason := collector.AllowDestination(m.route.Desc.DstPK(), probes); !ok {
				m.fail("%v: %s", ErrCircuitOpen, reason)
				continue
			}
			if blocked, reason := batchIntermediateBlocked(collector, probes, m.route); blocked {
				m.fail("%v: intermediate %s", ErrCircuitOpen, reason)
				continue
			}
		}
		admitted = append(admitted, m)
	}
	defer func() {
		for i, fin := range finishers {
			if fin == nil {
				continue
			}
			var err error
			if members[i].err != "" {
				err = errors.New(members[i].err)
			}
			fin(&err)
		}
	}()

	if len(admitted) == 0 {
		return batchResults(members), savings
	}

	// One reserver over EVERY admitted member's forward and reverse hops: this
	// is the coalescing. Each distinct PK is dialed once and asked once for
	// the full count of route IDs the whole batch needs from it.
	paths := make([][]routing.Hop, 0, 2*len(admitted))
	for _, m := range admitted {
		paths = append(paths, m.route.Forward, m.route.Reverse)
	}
	rtIDR, err := NewIDReserver(ctx, dialer, pool, paths)
	if err != nil {
		for _, m := range admitted {
			m.fail("batch id reserver: %v", err)
		}
		return batchResults(members), savings
	}
	if err := rtIDR.ReserveIDs(ctx); err != nil {
		rtIDR.Close() //nolint:errcheck,gosec
		for _, m := range admitted {
			m.fail("failed to reserve route ids: %v", err)
		}
		return batchResults(members), savings
	}
	defer func() {
		// Tear down only the members that failed; the ones that installed keep
		// their rules. The reserver's per-PK clients are still live here, which
		// is what the teardown reuses.
		for _, m := range admitted {
			if m.err != "" {
				m.installed.teardown(ctx, log, rtIDR)
			}
		}
		if pool != nil && countInstalled(members) > 0 {
			rtIDR.ReturnToPool(pool)
			return
		}
		rtIDR.Close() //nolint:errcheck,gosec
	}()

	// Per-member rule generation off the SHARED reserver. Each member pops its
	// own route-ID chain, so the members stay independent even though the
	// reservation was one RPC per hop.
	interRules := make(RulesMap)
	interOwners := make(map[string][]*batchMember)
	live := make([]*batchMember, 0, len(admitted))
	for _, m := range admitted {
		fwdRt, revRt := m.route.ForwardAndReverse()
		fwdRules, revRules, mInter, gErr := GenerateRules(rtIDR, []routing.Route{fwdRt, revRt})
		if gErr != nil {
			m.fail("rule generation: %v", gErr)
			continue
		}
		srcKey, dstKey := m.route.Desc.Src().String(), m.route.Desc.Dst().String()
		if len(fwdRules[srcKey]) == 0 || len(revRules[srcKey]) == 0 ||
			len(fwdRules[dstKey]) == 0 || len(revRules[dstKey]) == 0 {
			m.fail("rule generation: missing edge rules")
			continue
		}
		m.initEdge = routing.EdgeRules{Desc: revRt.Desc, Forward: fwdRules[srcKey][0], Reverse: revRules[srcKey][0]}
		m.respEdge = routing.EdgeRules{Desc: fwdRt.Desc, Forward: fwdRules[dstKey][0], Reverse: revRules[dstKey][0]}
		for addr, rules := range mInter {
			interRules[addr] = append(interRules[addr], rules...)
			interOwners[addr] = append(interOwners[addr], m)
			m.interByAddr[addr] = append(m.interByAddr[addr], rules...)
		}
		live = append(live, m)
	}
	if len(live) == 0 {
		return batchResults(members), savings
	}

	// Savings bookkeeping over the members that actually ran: what this shape
	// would have cost as separate requests, against what it cost as one.
	savings.Issued = distinctBatchHops(live) + len(interRules)
	for _, m := range live {
		savings.Coalesced += distinctHopCount(m.route) + len(m.interByAddr)
	}

	// One AddIntermediaryRules per DISTINCT hop, carrying every live member's
	// rules for that hop. A hop that refuses fails only the members whose rules
	// it was carrying.
	batchBroadcastIntermediary(ctx, log, rtIDR, interRules, interOwners)

	// Destination edge rules stay per member — the destination installs one
	// route group per route and there is no merged form of AddEdgeRules on a
	// visor's router RPC. They do go out concurrently over the ONE pooled
	// connection the shared reserver already holds to the destination.
	batchInstallDstEdges(ctx, rtIDR, live, dst)

	ok := countInstalled(members)
	log.WithField("routes", len(members)).
		WithField("installed", ok).
		WithField("rpcs_issued", savings.Issued).
		WithField("rpcs_unbatched", savings.Coalesced).
		Info("Batched route setup complete")
	if haveCollector {
		collector.RecordBatch(len(members), ok, savings.Saved())
	}
	return batchResults(members), savings
}

// batchIntermediateBlocked reports whether any intermediate on the member's
// forward path has an open breaker — the same pre-check CreateRouteGroup runs,
// applied per member so one bad intermediate never costs the batch its siblings.
func batchIntermediateBlocked(c *setupmetrics.Collector, probes *setupmetrics.ProbeHolder, route routing.BidirectionalRoute) (bool, string) {
	for i, h := range route.Forward {
		if i == len(route.Forward)-1 {
			break // that's the destination, already checked
		}
		if ok, reason := c.AllowIntermediate(h.To, probes); !ok {
			return true, reason
		}
	}
	return false, ""
}

// batchBroadcastIntermediary issues ONE AddIntermediaryRules per distinct hop,
// carrying every member's rules for that hop. Failures are attributed to the
// members whose rules the call was carrying; successes are recorded on each
// owner's own tracker so a later per-member failure tears down only its share.
func batchBroadcastIntermediary(
	ctx context.Context,
	log *logging.Logger,
	rtIDR IDReserver,
	interRules RulesMap,
	owners map[string][]*batchMember,
) {
	if len(interRules) == 0 {
		return
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for addr, rules := range interRules {
		var pk cipher.PubKey
		if err := pk.Set(strings.Split(addr, ":")[0]); err != nil {
			for _, m := range owners[addr] {
				m.fail("bad intermediary address %q: %v", addr, err)
			}
			continue
		}
		wg.Add(1)
		go func(addr string, pk cipher.PubKey, rules []routing.Rule) {
			defer wg.Done()
			// The shared ctx is deliberately NOT canceled on one hop's
			// failure: canceling closes the pooled DMSG streams of healthy
			// siblings mid-broadcast (see BroadcastIntermediaryRules).
			ok, err := rtIDR.Client(pk).AddIntermediaryRules(ctx, rules)
			mu.Lock()
			defer mu.Unlock()
			for _, m := range owners[addr] {
				if err != nil || !ok {
					m.fail("intermediary rules on %s: %v", pk, err)
					continue
				}
				m.installed.add(pk, m.interByAddr[addr]...)
			}
		}(addr, pk, rules)
	}
	wg.Wait()
	log.WithField("intermediary_hops", len(interRules)).Debug("Batched intermediary rules broadcast")
}

// batchInstallDstEdges installs each live member's responding edge rules on the
// destination, concurrently over the shared connection. A member that already
// failed upstream is skipped.
func batchInstallDstEdges(ctx context.Context, rtIDR IDReserver, live []*batchMember, dst cipher.PubKey) {
	client := rtIDR.Client(dst)
	if client == nil {
		for _, m := range live {
			m.fail("no router client for destination %s", dst)
		}
		return
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, m := range live {
		if m.err != "" {
			continue
		}
		wg.Add(1)
		go func(m *batchMember) {
			defer wg.Done()
			ok, err := client.AddEdgeRules(ctx, m.respEdge)
			mu.Lock()
			defer mu.Unlock()
			if err != nil || !ok {
				m.fail("destination rules on %s: %v", dst, err)
				return
			}
			m.installed.add(dst, m.respEdge.Forward, m.respEdge.Reverse)
		}(m)
	}
	wg.Wait()
}

// distinctHopCount is how many visors one route touches — what its own
// unbatched setup would have paid in id-reservation RPCs.
func distinctHopCount(route routing.BidirectionalRoute) int {
	seen := make(map[cipher.PubKey]struct{}, 4)
	for _, hops := range [][]routing.Hop{route.Forward, route.Reverse} {
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

// distinctBatchHops is how many visors the whole batch touches — what the ONE
// shared reserver pays.
func distinctBatchHops(members []*batchMember) int {
	seen := make(map[cipher.PubKey]struct{}, 4*len(members))
	for _, m := range members {
		for _, hops := range [][]routing.Hop{m.route.Forward, m.route.Reverse} {
			if len(hops) == 0 {
				continue
			}
			seen[hops[0].From] = struct{}{}
			for _, h := range hops {
				seen[h.To] = struct{}{}
			}
		}
	}
	return len(seen)
}

func batchResults(members []*batchMember) []routing.BatchRouteResult {
	out := make([]routing.BatchRouteResult, 0, len(members))
	for _, m := range members {
		out = append(out, m.result())
	}
	return out
}

func countInstalled(members []*batchMember) int {
	n := 0
	for _, m := range members {
		if m.err == "" {
			n++
		}
	}
	return n
}
