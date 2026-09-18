//go:build !tinygo || (js && wasm)

// Package router pkg/router/setup_batch_client.go c2-net-routing
//
// The INITIATOR side of batched route setup: collect the sibling dials a
// multi-route request produces and send them to the setup node as one message.
//
// Why a coalescer and not a "DialRoutesBatch" entry point. Every multi-route
// dial this visor makes already fans out into concurrent single dials — the
// standby-pool fill, `--tunnels N`, the mux leg self-heal, a diversify dial's
// K-race. They arrive at setupNodeDialer.Dial within milliseconds of each
// other, each carrying its own BidirectionalRoute to the SAME exit. Collecting
// them here batches every one of those callers without any of them knowing
// about batching, and without touching DialRoutes' 650 lines of route-group
// construction, handshake and rule bookkeeping.
//
// The window is paid by the FIRST dial of a burst and by nobody else: a lone
// dial waits setup.batch_window (40 ms by default) against a setup round trip
// whose measured p50 is 0.6-2.1 s. A burst of eight pays it once, for all eight.
//
// Negotiation, not a flag. The leader asks the setup node for its capability
// list once per connection; a node that does not advertise CapBatchRouteSetup
// (or that predates the Capabilities RPC entirely, answering "can't find
// method") makes the whole group fall back to today's concurrent single
// requests. Nothing has to be configured on either side, and a mixed fleet of
// setup nodes works.
package router

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// errBatchUnsupported means no reachable setup node advertised
// CapBatchRouteSetup, so the group must fall back to single requests. It is a
// routing decision, never surfaced to the caller of DialRoutes.
var errBatchUnsupported = errors.New("setup node does not support batched route setup")

// batchSendDeadline bounds one batched request end to end. It is generous: a
// batch is up to setup.batch_max routes' worth of hop dialing and rule
// installing on the setup node, and the node's own per-request deadline (30 s,
// scaled by batch size) is the tighter bound in practice. This exists so a
// wedged connection cannot hold a group's waiters forever.
const batchSendDeadline = 120 * time.Second

// batchKey groups dials that may share one request: same source, same exit.
// Mixing either is refused by BidirectionalRouteBatch.Check, and the per-hop
// coalescing is only a saving because both are common to every member.
type batchKey struct {
	src cipher.PubKey
	dst cipher.PubKey
}

// batchOutcome is one member's answer.
type batchOutcome struct {
	rules routing.EdgeRules
	node  cipher.PubKey
	err   error
}

// batchWaiter is one dial parked in a forming group.
type batchWaiter struct {
	req routing.BidirectionalRoute
	out chan batchOutcome
}

// batchGroup is a forming batch: the waiters collected so far plus the timer
// that will close it.
type batchGroup struct {
	waiters []*batchWaiter
	timer   *time.Timer
}

// batchSender issues one batched request and returns a result per member, in
// member order. It returns errBatchUnsupported when the reachable setup node
// cannot take a batch.
type batchSender func(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client,
	setupNodes []cipher.PubKey, reqs []routing.BidirectionalRoute) ([]batchOutcome, error)

// setupBatcher collects concurrent dials to the same exit into one request.
// The zero value is not usable; see newSetupBatcher.
type setupBatcher struct {
	mu     sync.Mutex
	groups map[batchKey]*batchGroup

	// metrics, read by the dial trail and mux events; never gate behavior.
	batched  uint64
	singles  uint64
	fallback uint64
}

func newSetupBatcher() *setupBatcher {
	return &setupBatcher{groups: make(map[batchKey]*batchGroup)}
}

// batcherStats is a point-in-time copy for observability.
type batcherStats struct {
	// Batched is how many routes went out inside a batched request.
	Batched uint64
	// Singles is how many went out on their own because they were alone in
	// their window.
	Singles uint64
	// Fallback is how many fell back to a single request because the setup
	// node did not advertise the batch capability.
	Fallback uint64
}

func (b *setupBatcher) stats() batcherStats {
	if b == nil {
		return batcherStats{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return batcherStats{Batched: b.batched, Singles: b.singles, Fallback: b.fallback}
}

// dial parks req in the group for its (src, dst) and returns that member's
// answer once the group has been sent. Returns errBatchUnsupported when the
// group's request could not be made in batched form at all, which the caller
// answers by running its own single dial.
func (b *setupBatcher) dial(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	setupNodes []cipher.PubKey,
	req routing.BidirectionalRoute,
	send batchSender,
) (routing.EdgeRules, cipher.PubKey, error) {
	maxRoutes := SetupBatchMax()
	if maxRoutes > routing.MaxBatchRoutes {
		maxRoutes = routing.MaxBatchRoutes
	}
	if maxRoutes <= 1 {
		return routing.EdgeRules{}, cipher.PubKey{}, errBatchUnsupported
	}

	key := batchKey{src: req.Desc.SrcPK(), dst: req.Desc.DstPK()}
	w := &batchWaiter{req: req, out: make(chan batchOutcome, 1)}

	b.mu.Lock()
	g := b.groups[key]
	if g == nil {
		g = &batchGroup{}
		b.groups[key] = g
	}
	g.waiters = append(g.waiters, w)
	full := len(g.waiters) >= maxRoutes
	first := len(g.waiters) == 1
	switch {
	case full:
		// Close it now rather than waiting out the window: the batch is at the
		// size the operator asked for, and a later sibling opens a new group.
		if g.timer != nil {
			g.timer.Stop()
		}
		b.closeGroupLocked(key, g, log, dmsgC, setupNodes, send)
	case first:
		g.timer = time.AfterFunc(SetupBatchWindow(), func() {
			b.mu.Lock()
			cur := b.groups[key]
			if cur != g {
				b.mu.Unlock()
				return // already closed by a full group
			}
			b.closeGroupLocked(key, g, log, dmsgC, setupNodes, send)
			b.mu.Unlock()
		})
	}
	b.mu.Unlock()

	select {
	case <-ctx.Done():
		// The group still runs — its other members are waiting on it — and this
		// member's result is simply dropped when it arrives (out is buffered).
		return routing.EdgeRules{}, cipher.PubKey{}, ctx.Err()
	case res := <-w.out:
		return res.rules, res.node, res.err
	}
}

// closeGroupLocked detaches the group and sends it. Must be called with b.mu
// held; the send itself runs on its own goroutine, off the lock.
func (b *setupBatcher) closeGroupLocked(
	key batchKey,
	g *batchGroup,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	setupNodes []cipher.PubKey,
	send batchSender,
) {
	delete(b.groups, key)
	waiters := g.waiters
	if len(waiters) == 0 {
		return
	}
	if len(waiters) == 1 {
		b.singles++
	} else {
		b.batched += uint64(len(waiters)) //nolint:gosec // bounded by setup.batch_max
	}
	go b.send(waiters, log, dmsgC, setupNodes, send)
}

// send issues the batch and fans the results back to the waiters. Every waiter
// is answered exactly once, whatever happens.
func (b *setupBatcher) send(
	waiters []*batchWaiter,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	setupNodes []cipher.PubKey,
	send batchSender,
) {
	// The request outlives any single member's context: a member that gave up
	// must not cancel the setup its siblings are still waiting on.
	ctx, cancel := context.WithTimeout(context.Background(), batchSendDeadline)
	defer cancel()

	reqs := make([]routing.BidirectionalRoute, len(waiters))
	for i, w := range waiters {
		reqs[i] = w.req
	}

	results, err := send(ctx, log, dmsgC, setupNodes, reqs)
	if err != nil {
		if errors.Is(err, errBatchUnsupported) {
			b.mu.Lock()
			b.fallback += uint64(len(waiters)) //nolint:gosec // bounded by setup.batch_max
			b.mu.Unlock()
		}
		for _, w := range waiters {
			w.out <- batchOutcome{err: err}
		}
		return
	}
	for i, w := range waiters {
		if i < len(results) {
			w.out <- results[i]
			continue
		}
		w.out <- batchOutcome{err: fmt.Errorf("batched route setup: no result for route %d of %d", i, len(waiters))}
	}
}

// sendBatchOverSetupClient is the reference batchSender: connect to a setup
// node over DMSG, confirm it advertises CapBatchRouteSetup, send the batch and
// map the per-route results back into member order.
func sendBatchOverSetupClient(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	setupNodes []cipher.PubKey,
	reqs []routing.BidirectionalRoute,
) ([]batchOutcome, error) {
	client, err := NewSetupClient(ctx, log, dmsgC, setupNodes)
	if err != nil {
		return nil, err
	}
	defer client.Close() //nolint:errcheck,gosec
	node := client.ConnectedNode()

	caps, err := client.Capabilities(ctx)
	if err != nil || !hasCap(caps, CapBatchRouteSetup) {
		if err != nil {
			log.WithError(err).Debug("Setup node did not answer the capability handshake; using single requests")
		}
		return nil, errBatchUnsupported
	}

	batch := routing.BidirectionalRouteBatch{Routes: make([]routing.BatchRouteRequest, len(reqs))}
	for i, r := range reqs {
		batch.Routes[i] = routing.BatchRouteRequest{ID: uint32(i), Route: r} //nolint:gosec // bounded by setup.batch_max
	}
	reply, err := client.DialRouteGroupBatch(ctx, batch)
	if err != nil {
		if isUnimplementedRPC(err) {
			return nil, errBatchUnsupported
		}
		return nil, fmt.Errorf("batched route setup: %w", err)
	}

	byID := make(map[uint32]routing.BatchRouteResult, len(reply.Results))
	for _, res := range reply.Results {
		byID[res.ID] = res
	}
	out := make([]batchOutcome, len(reqs))
	for i := range reqs {
		res, ok := byID[uint32(i)] //nolint:gosec // bounded by setup.batch_max
		switch {
		case !ok:
			out[i] = batchOutcome{err: fmt.Errorf("batched route setup: setup node returned no result for route %d", i)}
		case res.Failed():
			out[i] = batchOutcome{err: fmt.Errorf("batched route setup: %s", res.Error)}
		default:
			out[i] = batchOutcome{rules: res.Rules, node: node}
		}
	}
	log.WithField("routes", len(reqs)).
		WithField("setup_node", node.String()).
		Debug("Batched route setup sent")
	return out, nil
}

// hasCap reports whether the advertised capability list carries name.
func hasCap(caps []string, name string) bool {
	for _, c := range caps {
		if c == name {
			return true
		}
	}
	return false
}
