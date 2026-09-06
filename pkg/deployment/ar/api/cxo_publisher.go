// Package api pkg/deployment/ar/api/cxo_publisher.go c4-net-discovery
//
// CXO publisher for the address-resolver's bindings, keyed by peer public key.
//
// Until now the AR published no CXO feed at all: visors push their bindings in
// over CXO (pkg/deployment/ar/regcxo) and nothing comes back, so the ~1.5k
// warm CXO connections the AR already holds carry traffic in one direction
// only. This adds the read side, in the shape a per-dial address lookup wants
// — see pkg/deployment/ar/arfeed for the tree layout and why it is 256
// always-present buckets rather than a flat per-peer level or a per-server
// batch.
//
// Purely additive: HTTP GET /resolve stays authoritative and unchanged, and
// the feed is inert until something subscribes to or previews it.
//
// # Where the data comes from
//
// Two sources, because neither alone is both fresh and correct:
//
//   - Every write to the store marks (peer, type) dirty. The worker resolves
//     the STORED record for that pair on the next flush and re-encodes the
//     peer's bucket. Resolving rather than trusting the value passed to Bind
//     matters: the redis store merges IPv4/IPv6 families across two separate
//     binds, so only the stored record is the whole truth.
//   - A periodic full resync rebuilds the state from the per-type index. This
//     is what removes peers whose binding TTL'd out — an expiry never calls
//     DelBind, so an incremental-only publisher would keep stale addresses
//     forever.
//
// Both run on one worker goroutine, so the state map needs no lock, and the
// store writes never block on the publisher: marks go through a buffered
// channel and are dropped (counted) on overflow. A dropped mark costs at most
// one resync interval of staleness for that peer.
package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/deployment/ar/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// bindingsBatchWindow is how long treestore coalesces tree mutations before it
// re-encodes and publishes the Root. A publish clones and re-encodes the whole
// in-memory tree, so the window trades publish frequency against CPU; 30s
// matches the dmsg-discovery clients-by-server publisher, which learned the
// same lesson the expensive way.
const bindingsBatchWindow = 30 * time.Second

// bindingsFlushWindow coalesces the per-bucket leaf re-encode (resolve + JSON +
// gzip). Under the fleet's ~90s SUDPH re-bind cadence roughly twenty peers go
// dirty per second, most of them in distinct buckets, so flushing per mark
// would gzip the same bucket repeatedly for no change in content. Kept well
// under bindingsBatchWindow so a bucket is current before a publish captures
// it.
const bindingsFlushWindow = 3 * time.Second

// bindingsResyncInterval is how often the whole tree is rebuilt from the
// store's per-type index. It bounds how long a TTL-expired binding can linger
// in the feed, since an expiry produces no DelBind to mark it dirty.
const bindingsResyncInterval = 5 * time.Minute

// bindingsQueueDepth bounds in-flight dirty marks. Sized for the fleet's
// re-bind rate with headroom for a restart thundering herd. Overflow is
// dropped and counted; the store write never blocks.
const bindingsQueueDepth = 8192

// bindableTypes is the set of transport types the AR stores bindings for, and
// therefore the set the feed carries.
var bindableTypes = []types.Type{types.STCPR, types.SUDPH, types.QUIC, types.WT}

// dirtyMark is one (peer, transport type) pair awaiting a re-resolve.
type dirtyMark struct {
	pk cipher.PubKey
	t  types.Type
}

// BindingsCXOPublisher mirrors the address-resolver's bindings onto a CXO
// TreeStore feed keyed by peer public key.
type BindingsCXOPublisher struct {
	pub   *treestore.Publisher
	store store.Store
	log   *logging.Logger

	// flushWindow and resyncInterval are fields rather than constants so a
	// test can drive the worker at a pace it can wait on.
	flushWindow    time.Duration
	resyncInterval time.Duration

	marks chan dirtyMark
	done  chan struct{}
	wg    sync.WaitGroup

	dropped uint64 // atomic; bumped when marks overflows

	// state is the live per-peer record set, and pending the marks awaiting a
	// re-resolve. Both are owned exclusively by the worker goroutine (run), so
	// they need no lock.
	state   map[cipher.PubKey]*arfeed.PeerBindings
	pending map[dirtyMark]struct{}

	mu        sync.Mutex
	lastError error
}

// StartBindingsCXOPublisher constructs the publisher backed by the given dmsg
// client and the AR's secret key, and runs an immediate full resync so the
// feed is complete before anything reads it. The allowlist is open: GET
// /resolve is a public read, so its CXO mirror inherits that.
//
// Returns nil plus an error if the underlying treestore publisher cannot be
// created; callers log and continue without it, HTTP staying the source of
// truth.
func StartBindingsCXOPublisher(dmsgC *dmsg.Client, sk cipher.SecKey, st store.Store, logger logrus.FieldLogger) (*BindingsCXOPublisher, error) {
	log := logging.MustGetLogger("ar-cxo-bindings-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:      log,
		InMemoryDB:  true, // the tree is recomputed from the store on every resync
		DmsgPort:    skyenv.DmsgARBindingsCXOPort,
		BatchWindow: bindingsBatchWindow,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil)

	p := newBindingsCXOPublisher(pub, st, log, bindingsFlushWindow, bindingsResyncInterval)

	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgARBindingsCXOPort).
			Info("CXO bindings publisher running")
	}
	return p, nil
}

// newBindingsCXOPublisher wires the worker around an already-constructed
// treestore publisher. Split out from StartBindingsCXOPublisher so a test can
// drive the whole thing over the CXO node's native TCP transport, with no dmsg
// client or discovery in the way.
func newBindingsCXOPublisher(pub *treestore.Publisher, st store.Store, log *logging.Logger,
	flushWindow, resyncInterval time.Duration) *BindingsCXOPublisher {
	p := &BindingsCXOPublisher{
		pub:            pub,
		store:          st,
		log:            log,
		flushWindow:    flushWindow,
		resyncInterval: resyncInterval,
		marks:          make(chan dirtyMark, bindingsQueueDepth),
		done:           make(chan struct{}),
		state:          make(map[cipher.PubKey]*arfeed.PeerBindings),
		pending:        make(map[dirtyMark]struct{}),
	}
	p.wg.Add(1)
	go p.run()
	return p
}

// FeedPK returns the publisher's feed PK (the AR's own PK).
func (p *BindingsCXOPublisher) FeedPK() cipher.PubKey { return p.pub.Feed() }

// Dropped returns the cumulative count of dropped dirty marks.
func (p *BindingsCXOPublisher) Dropped() uint64 {
	if p == nil {
		return 0
	}
	return atomic.LoadUint64(&p.dropped)
}

// LastError returns the most recent publish error, or nil.
func (p *BindingsCXOPublisher) LastError() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}

// Close stops the worker and the underlying publisher. Safe to call twice.
func (p *BindingsCXOPublisher) Close() error {
	if p == nil || p.pub == nil {
		return nil
	}
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	p.wg.Wait()
	return p.pub.Close()
}

// MarkDirty records that a peer's binding for one transport type changed and
// must be re-read from the store. Non-blocking and safe from any goroutine:
// this runs on the HTTP / UDP / CXO-ingest write paths.
func (p *BindingsCXOPublisher) MarkDirty(t types.Type, pk cipher.PubKey) {
	if p == nil || pk == (cipher.PubKey{}) {
		return
	}
	select {
	case p.marks <- dirtyMark{pk: pk, t: types.NormalizeType(t)}:
	case <-p.done:
	default:
		dropped := atomic.AddUint64(&p.dropped, 1)
		if dropped&(dropped-1) == 0 {
			p.log.WithField("dropped_total", dropped).
				Warn("CXO bindings mark queue full; dropping (peer recovers on the next resync)")
		}
	}
}

// run is the single worker. It owns state and pending.
func (p *BindingsCXOPublisher) run() {
	defer p.wg.Done()

	// Materialize all 256 buckets up front so every level is a dense sorted
	// array from the very first Root — that density is what lets a reader
	// address a bucket by computed index instead of searching for it.
	paths := make(map[string]struct{}, arfeed.BucketCount)
	for i := 0; i < arfeed.BucketCount; i++ {
		paths[arfeed.BucketPathAt(i)] = struct{}{}
	}
	p.encodeBuckets(paths)
	p.resync()

	flush := time.NewTicker(p.flushWindow)
	defer flush.Stop()
	resync := time.NewTicker(p.resyncInterval)
	defer resync.Stop()

	for {
		select {
		case <-p.done:
			return
		case m := <-p.marks:
			p.pending[m] = struct{}{}
		case <-flush.C:
			p.flush()
		case <-resync.C:
			p.resync()
		}
	}
}

// definitelyAbsent reports whether a Resolve error means the store really has
// no such binding, as opposed to the store being briefly unable to say.
//
// The distinction is the whole correctness of this publisher. Treating every
// error as absence would let one redis blip, or one flush that ran out of its
// context budget under a restart herd, PUBLISH the absence of bindings that
// exist — and a reader cannot tell a false absence from a real one.
func definitelyAbsent(err error) bool {
	return errors.Is(err, store.ErrNoEntry) || errors.Is(err, store.ErrUnknownTransportType)
}

// flush re-reads every pending (peer, type) from the store, updates the state
// map and re-encodes each affected bucket. Marks the store could not answer
// are put back for the next flush rather than published as absences.
// Worker-only.
func (p *BindingsCXOPublisher) flush() {
	if len(p.pending) == 0 {
		return
	}
	pending := p.pending
	p.pending = make(map[dirtyMark]struct{})

	ctx, cancel := context.WithTimeout(context.Background(), p.flushWindow)
	defer cancel()

	dirty := make(map[string]struct{}, len(pending))
	for m := range pending {
		data, err := p.store.Resolve(ctx, m.t, m.pk)
		if err != nil && !definitelyAbsent(err) {
			// Transient: re-queue and leave whatever we already publish for
			// this peer in place.
			p.pending[m] = struct{}{}
			p.recordError(err)
			continue
		}
		rec := p.state[m.pk]
		if rec == nil {
			rec = &arfeed.PeerBindings{}
		}
		if err != nil {
			rec.Set(m.t, nil)
		} else {
			v := data
			rec.Set(m.t, &v)
		}
		if rec.Empty() {
			delete(p.state, m.pk)
		} else {
			p.state[m.pk] = rec
		}
		dirty[arfeed.BucketPath(m.pk)] = struct{}{}
	}
	p.encodeBuckets(dirty)
}

// resync rebuilds the whole state map from the store's per-type indexes and
// re-encodes every bucket whose membership or content moved. This is the only
// thing that removes a peer whose binding expired by TTL. Worker-only.
func (p *BindingsCXOPublisher) resync() {
	ctx, cancel := context.WithTimeout(context.Background(), p.resyncInterval/2)
	defer cancel()

	next := make(map[cipher.PubKey]*arfeed.PeerBindings, len(p.state))
	for _, t := range bindableTypes {
		pks, err := p.store.GetAll(ctx, t)
		if err != nil {
			p.log.WithError(err).WithField("type", t).
				Debug("CXO bindings resync: index read failed; keeping previous state for this type")
			p.recordError(err)
			// A partial resync would publish an absence that is not real, so
			// abandon this round entirely and retry on the next tick.
			return
		}
		for _, raw := range pks {
			var pk cipher.PubKey
			if err := pk.UnmarshalText([]byte(raw)); err != nil {
				continue
			}
			data, err := p.store.Resolve(ctx, t, pk)
			if err != nil {
				if definitelyAbsent(err) {
					continue // TTL'd out between the index read and now
				}
				// The store could not answer. Carry the value we already
				// publish forward rather than dropping the peer, which would
				// be a false absence.
				p.recordError(err)
				if prev := p.state[pk].Get(t); prev != nil {
					rec := next[pk]
					if rec == nil {
						rec = &arfeed.PeerBindings{}
						next[pk] = rec
					}
					rec.Set(t, prev)
				}
				continue
			}
			rec := next[pk]
			if rec == nil {
				rec = &arfeed.PeerBindings{}
				next[pk] = rec
			}
			v := data
			rec.Set(t, &v)
		}
	}

	// Every bucket that gained or lost a peer, or whose peers changed, has to
	// be re-encoded. Cheapest correct answer: re-encode the buckets touched by
	// the symmetric difference of the two peer sets, plus any peer whose
	// record differs. Re-encoding a bucket whose bytes did not change is a
	// wire no-op (CXO is content-addressed), so a coarse dirty set only costs
	// local gzip.
	dirty := make(map[string]struct{})
	for pk := range p.state {
		if _, still := next[pk]; !still {
			dirty[arfeed.BucketPath(pk)] = struct{}{}
		}
	}
	for pk := range next {
		dirty[arfeed.BucketPath(pk)] = struct{}{}
	}
	p.state = next
	p.encodeBuckets(dirty)
	p.clearError()
}

// encodeBuckets re-encodes and Puts each named bucket from the current state.
// Worker-only.
func (p *BindingsCXOPublisher) encodeBuckets(paths map[string]struct{}) {
	if len(paths) == 0 {
		return
	}
	byBucket := make(map[string]map[cipher.PubKey]*arfeed.PeerBindings, len(paths))
	for pk, rec := range p.state {
		path := arfeed.BucketPath(pk)
		if _, want := paths[path]; !want {
			continue
		}
		m := byBucket[path]
		if m == nil {
			m = make(map[cipher.PubKey]*arfeed.PeerBindings)
			byBucket[path] = m
		}
		m[pk] = rec
	}
	// One batched tree mutation rather than one per bucket. treestore marks the
	// tree dirty per write and re-encodes the WHOLE tree on the next publish
	// tick, so 256 individual Puts inside one batch window can trigger repeated
	// whole-tree re-encodes; PutBatch is a single mutex acquire and a single
	// dirty mark. It matters most on the startup materialization, which touches
	// every bucket at once.
	ops := make([]treestore.PutOp, 0, len(paths))
	for path := range paths {
		blob, err := arfeed.EncodeBucket(byBucket[path])
		if err != nil {
			p.log.WithError(err).WithField("bucket", path).Debug("CXO bindings: bucket encode failed")
			p.recordError(err)
			continue
		}
		// An empty bucket is still published (as an empty map) rather than
		// deleted: every level must stay dense for index addressing to work.
		ops = append(ops, treestore.PutOp{Path: path, Value: blob})
	}
	if err := p.pub.PutBatch(ops); err != nil {
		p.log.WithError(err).Debug("CXO bindings: bucket PutBatch failed")
		p.recordError(err)
	}
}

func (p *BindingsCXOPublisher) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = err
}

func (p *BindingsCXOPublisher) clearError() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = nil
}
