// Package api pkg/deployment/tpd/api/cxo_routing_publisher.go c4-net-discovery
//
// CXO routing feed: what visors route on. It carries the transports that
// exist now (see routingEntries) with the latency and throughput routes are
// weighed by, sharded one leaf per visor:
//
//	routing/<pk>   // gzipped JSON []allTransportsWireEntry
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
	"context"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/tpdpaths"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
)

// RoutingPathPrefix is the routing feed's sub-tree; shard paths are
// RoutingPathPrefix + <pk hex>.
const RoutingPathPrefix = tpdpaths.RoutingPathPrefix

// routingPublishInterval is how often the routing shards are recomputed.
const routingPublishInterval = 60 * time.Second

// RoutingCXOPublisher publishes the routing feed.
type RoutingCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	// shards is the set of shard paths the last cycle wrote, so the next
	// can delete the ones whose visor has no transports left. Only the
	// publish loop touches it.
	shards map[string]bool

	mu        sync.Mutex
	lastError error
}

// StartRoutingCXOPublisher starts the routing feed on DmsgTPDRoutingCXOPort.
func StartRoutingCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*RoutingCXOPublisher, error) {
	log := logging.MustGetLogger("tpd-cxo-routing-pub")
	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgTPDRoutingCXOPort,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil) // open feed: every visor routes on it
	pubCtx, cancel := context.WithCancel(ctx)
	rp := &RoutingCXOPublisher{api: api, pub: pub, log: log, cancel: cancel, done: make(chan struct{}), shards: map[string]bool{}}
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
	t := time.NewTicker(routingPublishInterval)
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
	shards, err := routingShards(entries)
	if err != nil {
		r.log.WithError(err).Warn("routing shard encode failed")
		r.recordError(err)
		return
	}
	// Sized for the puts; the few shards that went away since the last
	// publish grow it. (A sum of the two lengths here is what CodeQL's
	// allocation-size-overflow check flags.)
	ops := make([]treestore.PutOp, 0, len(shards))
	for path := range r.shards {
		if _, still := shards[path]; !still {
			ops = append(ops, treestore.PutOp{Path: path})
		}
	}
	for path, body := range shards {
		ops = append(ops, treestore.PutOp{Path: path, Value: body})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	if err := r.pub.PutBatch(ops); err != nil {
		r.log.WithError(err).Warn("routing PutBatch failed")
		r.recordError(err)
		return
	}
	next := make(map[string]bool, len(shards))
	for path := range shards {
		next[path] = true
	}
	r.shards = next
}

// routingShards groups entries into one gzipped leaf per lower-keyed edge.
// Output is deterministic: an unchanged shard yields identical bytes.
func routingShards(entries []*transport.Entry) (map[string][]byte, error) {
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
	out := make(map[string][]byte, len(byPK))
	for pk, list := range byPK {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Edges[1] != list[j].Edges[1] {
				return list[i].Edges[1].Hex() < list[j].Edges[1].Hex()
			}
			return list[i].Type < list[j].Type
		})
		body, err := json.Marshal(list)
		if err != nil {
			return nil, err
		}
		out[RoutingPathPrefix+pk.Hex()] = cxoutils.Gzip(body)
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
