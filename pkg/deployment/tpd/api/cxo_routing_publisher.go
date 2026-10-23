// Package api pkg/deployment/tpd/api/cxo_routing_publisher.go c4-net-discovery
//
// CXO routing feed: what visors route on. It carries the transports that
// exist now (see routingEntries) with the latency and throughput routes are
// weighed by, as a snapshot sharded one leaf per visor plus the deltas since
// it (see package routingfeed for the layout):
//
//	routing/<pk>          // gzipped JSON []allTransportsWireEntry
//	routing/delta/<seq>   // gzipped JSON routingfeed.Delta
//
// A transport lives in the shard of its lower-keyed edge (entries keep their
// edges sorted), so each appears once. The all-transports feed republished
// its whole ~19 MB snapshot every minute and, the transport set churning,
// every subscriber fetched all of it every time. Here an unchanged shard
// re-encodes to the object subscribers already hold, so a tick ships only
// the shards that changed. Figures are rounded to two significant digits so
// probe jitter does not rewrite a shard.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/routingfeed"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// RoutingPathPrefix is the routing feed's sub-tree; shard paths are
// RoutingPathPrefix + <pk hex>.
const RoutingPathPrefix = routingfeed.Prefix

// DefaultRoutingPublishEvery is how often the routing shards are recomputed
// when the config leaves it empty. Telemetry is applied every 15 minutes, so
// publishing faster resends whole shards for noise to every subscriber.
const DefaultRoutingPublishEvery = 10 * time.Minute

// routingKeepRoots keeps the last few Roots, so a subscriber whose fill of
// the changed shards outlasts one publish can still fetch them.
const routingKeepRoots = 3

// DefaultRoutingSnapshotEvery is how many publishes go by between snapshots
// when the config leaves it empty: an hour at the default interval.
const DefaultRoutingSnapshotEvery = 6

// RoutingFeedConfig configures the routing feed. Zero values take the
// defaults.
type RoutingFeedConfig struct {
	// Every is the publish interval.
	Every time.Duration
	// SnapshotEvery is how many publishes go by between snapshots; the ones
	// between add a delta each.
	SnapshotEvery int
	Smoothing     RoutingSmoothing
}

// RoutingCXOPublisher publishes the routing feed.
type RoutingCXOPublisher struct {
	api           *API
	pub           *treestore.Publisher
	log           *logging.Logger
	every         time.Duration
	snapshotEvery int
	smooth        *routingSmoother

	cancel context.CancelFunc
	done   chan struct{}

	// lists are the shards of the last snapshot, by path, so the next one puts
	// only what changed; published is the state the last publish carried,
	// which the next delta starts from; deltaSeq counts the deltas since the
	// snapshot. Only the publish loop touches them.
	lists     map[string][]allTransportsWireEntry
	published map[string][]allTransportsWireEntry
	deltaSeq  int

	mu        sync.Mutex
	lastError error
}

// StartRoutingCXOPublisher starts the routing feed on DmsgTPDRoutingCXOPort,
// configured by cfg.
func StartRoutingCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, cfg RoutingFeedConfig, logger logrus.FieldLogger) (*RoutingCXOPublisher, error) {
	if cfg.Every <= 0 {
		cfg.Every = DefaultRoutingPublishEvery
	}
	if cfg.SnapshotEvery <= 0 {
		cfg.SnapshotEvery = DefaultRoutingSnapshotEvery
	}
	log := logging.MustGetLogger("tpd-cxo-routing-pub")
	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgTPDRoutingCXOPort,
		KeepRoots:  routingKeepRoots,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil) // open feed: every visor routes on it
	pubCtx, cancel := context.WithCancel(ctx)
	rp := &RoutingCXOPublisher{api: api, pub: pub, log: log, every: cfg.Every, snapshotEvery: cfg.SnapshotEvery,
		smooth: newRoutingSmoother(cfg.Smoothing), cancel: cancel, done: make(chan struct{})}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgTPDRoutingCXOPort).
			Info("CXO routing publisher running")
	}
	go rp.loop(pubCtx)
	return rp, nil
}

func (r *RoutingCXOPublisher) loop(ctx context.Context) {
	defer close(r.done)
	r.publishOnce(ctx)
	t := time.NewTicker(r.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.publishOnce(ctx)
		}
	}
}

func (r *RoutingCXOPublisher) publishOnce(ctx context.Context) {
	entries, err := routingEntries(ctx, r.api.store)
	if err != nil {
		r.log.WithError(err).Debug("routing fetch failed; will retry next tick")
		r.recordError(err)
		return
	}
	lists := routingLists(r.smooth.apply(entries))

	// A snapshot rewrites the shards that changed since the last one and
	// deletes the deltas since it; any other publish adds one delta.
	snapshot := r.lists == nil || r.deltaSeq >= r.snapshotEvery
	var ops []treestore.PutOp
	var deltaPath string
	if snapshot {
		if ops, err = r.changedShards(lists); err != nil {
			r.log.WithError(err).Warn("routing shard encode failed")
			r.recordError(err)
			return
		}
		r.pub.Walk(routingfeed.DeltaPrefix, func(path string, _ []byte) bool {
			ops = append(ops, treestore.PutOp{Path: path})
			return true
		})
	} else if body, n := encodeDelta(r.published, lists); n > 0 {
		deltaPath = routingfeed.DeltaPath(r.deltaSeq + 1)
		ops = append(ops, treestore.PutOp{Path: deltaPath, Value: body})
	}
	// Every cycle still publishes a Root, so a new subscriber always finds a
	// fresh head. One stored shard will do.
	if len(ops) == 0 {
		for path := range lists {
			if body, ok := r.pub.Get(path); ok {
				ops = append(ops, treestore.PutOp{Path: path, Value: body})
			}
			break
		}
	}
	if err := r.pub.PutBatch(ops); err != nil {
		r.log.WithError(err).Warn("routing PutBatch failed")
		r.recordError(err)
		return
	}
	switch {
	case snapshot:
		r.lists, r.deltaSeq = lists, 0
	case deltaPath != "":
		r.deltaSeq++
	}
	r.published = lists
}

// encodeDelta returns the gzipped Delta from prev to cur and how many
// transports it names.
func encodeDelta(prev, cur map[string][]allTransportsWireEntry) ([]byte, int) {
	type wireRemoval struct {
		Edges [2]cipher.PubKey `json:"edges"`
		Type  string           `json:"type"`
	}
	index := func(lists map[string][]allTransportsWireEntry) map[routingKey]allTransportsWireEntry {
		out := make(map[routingKey]allTransportsWireEntry)
		for _, list := range lists {
			for _, w := range list {
				out[routingKey{w.Edges, w.Type}] = w
			}
		}
		return out
	}
	before, after := index(prev), index(cur)
	var changed []allTransportsWireEntry
	var removed []wireRemoval
	for k, w := range after {
		if old, ok := before[k]; !ok || old != w {
			changed = append(changed, w)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			removed = append(removed, wireRemoval{Edges: k.edges, Type: string(k.typ)})
		}
	}
	if len(changed)+len(removed) == 0 {
		return nil, 0
	}
	less := func(a, b [2]cipher.PubKey) bool {
		if c := bytes.Compare(a[0][:], b[0][:]); c != 0 {
			return c < 0
		}
		return bytes.Compare(a[1][:], b[1][:]) < 0
	}
	sort.Slice(changed, func(i, j int) bool {
		if changed[i].Edges != changed[j].Edges {
			return less(changed[i].Edges, changed[j].Edges)
		}
		return changed[i].Type < changed[j].Type
	})
	sort.Slice(removed, func(i, j int) bool {
		if removed[i].Edges != removed[j].Edges {
			return less(removed[i].Edges, removed[j].Edges)
		}
		return removed[i].Type < removed[j].Type
	})
	body, err := json.Marshal(struct {
		Changed []allTransportsWireEntry `json:"c,omitempty"`
		Removed []wireRemoval            `json:"r,omitempty"`
	}{changed, removed})
	if err != nil {
		return nil, 0
	}
	return cxoutils.Gzip(body), len(changed) + len(removed)
}

type routingKey struct {
	edges [2]cipher.PubKey
	typ   types.Type
}

// changedShards returns the puts that bring the published tree from r.lists
// to lists: only a shard whose list changed is encoded again, and a shard
// whose visor has no transports left is deleted.
func (r *RoutingCXOPublisher) changedShards(lists map[string][]allTransportsWireEntry) ([]treestore.PutOp, error) {
	var ops []treestore.PutOp
	for path := range r.lists {
		if _, still := lists[path]; !still {
			ops = append(ops, treestore.PutOp{Path: path})
		}
	}
	for path, list := range lists {
		if prev, ok := r.lists[path]; ok && slices.Equal(prev, list) {
			continue
		}
		body, err := encodeShard(list)
		if err != nil {
			return nil, err
		}
		ops = append(ops, treestore.PutOp{Path: path, Value: body})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	return ops, nil
}

// routingLists groups entries by their lower-keyed edge into one sorted list
// per shard path.
func routingLists(entries []*transport.Entry) map[string][]allTransportsWireEntry {
	byPK := make(map[cipher.PubKey][]allTransportsWireEntry)
	for _, e := range entries {
		if e == nil || e.Edges[0] == e.Edges[1] {
			continue
		}
		edges := transport.SortEdges(e.Edges[0], e.Edges[1])
		byPK[edges[0]] = append(byPK[edges[0]], allTransportsWireEntry{
			Edges:         edges,
			Type:          e.Type,
			Label:         e.Label,
			Latency:       roundSig2(e.Latency),
			ThroughputBps: roundSig2(e.ThroughputBps),
		})
	}
	out := make(map[string][]allTransportsWireEntry, len(byPK))
	for pk, list := range byPK {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Edges[1] != list[j].Edges[1] {
				return bytes.Compare(list[i].Edges[1][:], list[j].Edges[1][:]) < 0
			}
			return list[i].Type < list[j].Type
		})
		out[RoutingPathPrefix+pk.Hex()] = list
	}
	return out
}

func encodeShard(list []allTransportsWireEntry) ([]byte, error) {
	body, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}
	return cxoutils.Gzip(body), nil
}

// routingShards encodes every shard: one gzipped leaf per lower-keyed edge.
func routingShards(entries []*transport.Entry) (map[string][]byte, error) {
	lists := routingLists(entries)
	out := make(map[string][]byte, len(lists))
	for path, list := range lists {
		body, err := encodeShard(list)
		if err != nil {
			return nil, err
		}
		out[path] = body
	}
	return out, nil
}

// roundSig2 rounds v to two significant digits: coarse enough that a
// probe's jitter leaves the shard unchanged, fine enough to rank routes.
func roundSig2(v float64) float64 {
	if v == 0 {
		return 0
	}
	p := math.Pow(10, 2-math.Ceil(math.Log10(math.Abs(v))))
	return math.Round(v*p) / p
}

func (r *RoutingCXOPublisher) recordError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastError = err
}

// LastError reports the most recent publish failure, if any.
func (r *RoutingCXOPublisher) LastError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastError
}

// FeedPK returns the publisher's feed PK (TPD's own PK).
func (r *RoutingCXOPublisher) FeedPK() cipher.PubKey { return r.pub.Feed() }

// Publisher returns the underlying treestore publisher.
func (r *RoutingCXOPublisher) Publisher() *treestore.Publisher { return r.pub }

// Close stops the loop and the publisher.
func (r *RoutingCXOPublisher) Close() error {
	if r.cancel != nil {
		r.cancel()
	}
	<-r.done
	return r.pub.Close()
}
