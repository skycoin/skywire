// Package cxopreview pkg/cxo/cxopreview/cxopreview.go c2-net-cxo
//
// Point reads of a remote CXO treestore feed WITHOUT subscribing to it.
//
// Every other CXO consumer in the tree subscribes to a whole feed and holds it
// resident. That is the right trade for a caller that walks the whole set
// repeatedly — the reward pages, the network visualizer — and the wrong one
// for a caller that wants one record by key, once. `dmsgd-clients-by-server`
// is 1 MB for 930 entries and its cold first sync is bounded at 45s and can
// fail outright, which is why "CXO lookup is too expensive for a constrained
// client" became received wisdom. That conclusion was drawn against
// subscription, not against CXO.
//
// (*node.Conn).Preview is the other half of the API and, until this package,
// had no caller anywhere in skywire: it fetches the peer's latest Root over an
// ALREADY-OPEN connection, hands the callback a pack that pulls objects from
// that peer on demand, and subscribes only if the callback says so. Returning
// false leaves the reader holding nothing — verified, not assumed, by
// TestLookupDoesNotSubscribe.
//
// # What a caller must know
//
// Preview fetches only the branches the walk touches, so the COST IS THE TREE
// SHAPE, not the feed size. A level's children are individual objects fetched
// one per Get, so scanning a level of N children costs N network round-trips;
// treestore writes children in sorted name order, so an index is far cheaper
// than a name. A feed meant to be read this way should be laid out for it —
// see pkg/deployment/ar/arfeed, whose dense 256-bucket level makes a lookup a
// fixed handful of fetches at any network size.
//
// Everything here is bounded twice over, because this runs on a dial path: the
// caller's context bounds the whole lookup, and a per-Get object/deadline
// budget unwinds a walk that would otherwise chase an oversized or hostile
// Root. A reader that answers "I don't know" quickly is always better than one
// that answers slowly.
//
// # When this is the right tool, measured
//
// Against the address resolver's bindings feed (1711 peers, 256 buckets) over
// the live dmsg network, 40 lookups each:
//
//	preview, this package        p50  16.4 ms   7 objects, 2.2 KB, 0 resident
//	HTTP GET /resolve over dmsg  p50 140.3 ms   0 resident, occasional multi-second tail
//	warm subscription to it      p50 119 us     100 KB resident, 0.8 s cold sync
//
// So Preview is roughly an order of magnitude faster than the authenticated
// HTTP round-trip it replaces, and roughly two orders SLOWER than a resident
// copy. It buys latency over HTTP and memory over subscription; it wins
// outright over neither.
//
// The deciding question is therefore not speed but whether the caller can
// afford to hold the feed. A visor that dials constantly and has 100 KB to
// spare should subscribe. A browser tab, a microcontroller, a short-lived CLI
// invocation, or any client resolving a handful of peers should preview — it
// pays one round-trip and keeps nothing, where a subscription pays a cold sync
// and holds the whole set to answer one question. Do not make this a default
// anywhere without measuring the specific feed: a feed an order of magnitude
// larger moves the subscription line, and a feed keyed the wrong way (see
// arfeed on why the tree shape decides the cost) moves the preview line.
package cxopreview

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// ErrBudget is returned through the walk when the per-lookup object or
// deadline budget is exhausted. It unwinds a treestore walk cleanly rather
// than letting it hang.
var ErrBudget = errors.New("cxopreview: object budget exhausted")

// DefaultMaxObjects bounds how many objects one lookup may pull from the peer.
// A correctly-shaped feed needs single digits; the cap exists so a malformed
// or hostile Root cannot turn one lookup into a feed download.
const DefaultMaxObjects = 64

// DefaultResponseTimeout replaces the CXO node default of 59s for a reader's
// node. A per-request wait that long is a footgun on a dial path: it pins the
// request against a peer that has stopped answering long past the point the
// caller gave up.
const DefaultResponseTimeout = 5 * time.Second

// Stats reports what one lookup actually cost, which is the number worth
// reporting when comparing this against a subscription or an HTTP round-trip.
type Stats struct {
	// Objects is how many objects were fetched from the peer.
	Objects int
	// Bytes is their total encoded size — everything the reader touched, none
	// of which it kept.
	Bytes int
	// Elapsed is the wall time of the whole lookup, dial excluded.
	Elapsed time.Duration
}

// WalkFunc inspects the previewed tree. It receives a pack that resolves
// objects from the peer on demand and the feed's root TreeNode. It must not
// retain either past its return.
//
// One caveat, because Conn.Preview is a blocking call with no context of its
// own: if the caller's context expires, Lookup returns while the walk is still
// in flight, so a WalkFunc CAN run (and write to whatever it closes over)
// after Lookup has returned. Write into locals and let the caller copy them
// out only on a nil error — never publish from inside the walk to something a
// concurrent reader can observe.
type WalkFunc func(pack registry.Pack, root *treestore.TreeNode) error

// Config tunes a Reader.
type Config struct {
	Logger *logging.Logger
	// MaxObjects bounds one lookup; zero means DefaultMaxObjects.
	MaxObjects int
	// ResponseTimeout bounds a single request to the peer; zero means
	// DefaultResponseTimeout.
	ResponseTimeout time.Duration
	// DataDir, when set, backs the node's container with a directory instead
	// of memory. Previewed objects are never written to it — only the schema
	// registry is — so memory is the sensible default.
	DataDir string
}

// Reader owns a CXO node used ONLY for previewing. It never subscribes to a
// feed, so its container holds nothing but the treestore schema registry.
type Reader struct {
	log  *logging.Logger
	node *node.Node
	port uint16

	maxObjects int

	// tcpAddr maps a peer's public key to the TCP address it was reached at,
	// for the native-TCP variant. TCP.Connect is idempotent per address, so
	// this is an address book, not a connection cache.
	mu      sync.Mutex
	tcpAddr map[cipher.PubKey]string
}

// NewDMSG builds a Reader whose node speaks dmsg on the given port. The port
// is the publisher's listen port — for the address resolver's bindings feed
// that is skyenv.DmsgARBindingsCXOPort.
func NewDMSG(dmsgC *dmsg.Client, sk cipher.SecKey, port uint16, conf Config) (*Reader, error) {
	if dmsgC == nil {
		return nil, errors.New("cxopreview: nil dmsg client")
	}
	cxoNode, maxObjects, err := newNode(sk, conf)
	if err != nil {
		return nil, err
	}
	if err := cxoNode.EnableDMSG(cxotransport.NewDMSGFactory(dmsgC, port)); err != nil {
		_ = cxoNode.Close() //nolint:errcheck
		return nil, err
	}
	return &Reader{
		log:        conf.Logger,
		node:       cxoNode,
		port:       port,
		maxObjects: maxObjects,
	}, nil
}

// newNode builds the preview-only CXO node shared by both constructors.
func newNode(sk cipher.SecKey, conf Config) (*node.Node, int, error) {
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("cxo-preview")
	}
	cfg := node.NewConfig()
	cfg.Config = skyobject.NewConfig()
	cfg.Config.InMemoryDB = conf.DataDir == ""
	if conf.DataDir != "" {
		cfg.Config.DataDir = conf.DataDir
	}
	// Dial-only in both variants: a hardcoded listener would collide with any
	// other CXO node in the same process (a visor runs several), and a reader
	// has nothing to serve. TCP's factory is still created (Listen "" means
	// dial-only) so the native-TCP variant can dial.
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	rt := conf.ResponseTimeout
	if rt <= 0 {
		rt = DefaultResponseTimeout
	}
	cfg.TCP.ResponseTimeout = rt
	cfg.UDP.ResponseTimeout = rt
	if !sk.Null() {
		cfg.SecKey = skycipher.SecKey(sk)
	}

	cxoNode, err := node.NewNode(cfg)
	if err != nil {
		return nil, 0, err
	}

	maxObjects := conf.MaxObjects
	if maxObjects <= 0 {
		maxObjects = DefaultMaxObjects
	}
	return cxoNode, maxObjects, nil
}

// NewTCP is the native-TCP analog of NewDMSG: a dial-only reader that reaches
// a publisher by address, with no dmsg and no discovery. Its purpose is to
// make the preview path testable end to end in-process; production readers use
// NewDMSG.
func NewTCP(sk cipher.SecKey, conf Config) (*Reader, error) {
	cxoNode, maxObjects, err := newNode(sk, conf)
	if err != nil {
		return nil, err
	}
	return &Reader{
		log:        conf.Logger,
		node:       cxoNode,
		maxObjects: maxObjects,
		tcpAddr:    make(map[cipher.PubKey]string),
	}, nil
}

// ConnectTCP dials a publisher by address and returns its public key, which is
// then the handle for Lookup.
func (r *Reader) ConnectTCP(address string) (cipher.PubKey, error) {
	tcp := r.node.TCP()
	if tcp == nil {
		return cipher.PubKey{}, errors.New("cxopreview: node has no tcp transport")
	}
	c, err := tcp.Connect(address)
	if err != nil {
		return cipher.PubKey{}, err
	}
	pk := cipher.PubKey(c.PeerID())
	r.mu.Lock()
	r.tcpAddr[pk] = address
	r.mu.Unlock()
	return pk, nil
}

// Node exposes the underlying CXO node, for tests that need to assert the
// reader really is holding nothing.
func (r *Reader) Node() *node.Node { return r.node }

// Close releases the node and every connection it holds.
func (r *Reader) Close() error {
	if r == nil || r.node == nil {
		return nil
	}
	return r.node.Close()
}

// Connect opens (or reuses) the connection to a publisher. Call it once at
// setup: the whole point of Preview is that the lookup rides a connection that
// is ALREADY open, so a lookup that has to dial first has lost the argument.
func (r *Reader) Connect(ctx context.Context, peerPK cipher.PubKey) error {
	_, err := r.conn(ctx, peerPK)
	return err
}

// conn returns a live connection to peerPK. ConnectPK is idempotent — it
// returns the existing conn when one is alive, evicts and re-dials when it is
// stale — so there is no connection cache to keep here.
func (r *Reader) conn(ctx context.Context, peerPK cipher.PubKey) (*node.Conn, error) {
	if d := r.node.DMSG(); d != nil {
		return d.ConnectPK(ctx, peerPK)
	}
	r.mu.Lock()
	addr := r.tcpAddr[peerPK]
	r.mu.Unlock()
	if addr == "" {
		return nil, fmt.Errorf("cxopreview: no route to %s (call ConnectTCP first)", peerPK)
	}
	// TCP.Connect returns the existing conn for an address it already holds.
	return r.node.TCP().Connect(addr)
}

// Connected reports whether the node already holds a connection to peerPK.
func (r *Reader) Connected(peerPK cipher.PubKey) bool {
	if d := r.node.DMSG(); d != nil {
		for _, pk := range d.Connections() {
			if pk == peerPK {
				return true
			}
		}
		return false
	}
	r.mu.Lock()
	_, ok := r.tcpAddr[peerPK]
	r.mu.Unlock()
	return ok
}

// Lookup previews feed on peerPK's connection and hands walk the root of the
// treestore tree. It NEVER subscribes: the preview callback returns false, so
// nothing about the feed is retained after the call.
//
// The context bounds the whole call. Because (*node.Conn).Preview is a
// blocking API with no context of its own, an expired context returns to the
// caller immediately while the in-flight request unwinds on its own — bounded
// by the node's ResponseTimeout — and its result is discarded.
//
// Two failure modes are retried once, both of them observed live rather than
// imagined, and each retried the way its cause requires:
//
//   - CONNECTION. The CXO node closes a conn after idleWatchdogThreshold with
//     no inbound traffic, and a preview reader is idle by construction between
//     lookups — it subscribes to nothing, so the publisher sends it nothing.
//     The first lookup after a quiet period therefore lands on a conn the
//     local node has already closed. Retried by dropping the conn and
//     re-dialing.
//   - STALE ROOT. Preview pins the peer's LastRoot at the instant of the
//     request, then fetches objects one at a time. A publisher that republishes
//     mid-walk can garbage-collect the previous Root's objects out from under
//     the reader, and the tree-walk helpers cannot tell an object that failed
//     to fetch from a child that is genuinely absent — so the lookup would
//     report a false "not found". Measured at a few percent of lookups against
//     a publisher on a 30-second publish cadence. Retried on the SAME conn,
//     which re-requests the now-current Root.
//
// A walk that completed with every fetch succeeding is NOT retried: its answer,
// found or not found, is the real one.
func (r *Reader) Lookup(ctx context.Context, peerPK, feed cipher.PubKey, walk WalkFunc) (Stats, error) {
	var total Stats
	for attempt := 0; ; attempt++ {
		stats, retry, err := r.lookupOnce(ctx, peerPK, feed, walk)
		total.Objects += stats.Objects
		total.Bytes += stats.Bytes
		total.Elapsed += stats.Elapsed
		if retry == retryNone || attempt >= lookupAttempts-1 || ctx.Err() != nil {
			return total, err
		}
		if retry == retryRedial {
			// Drop the dead conn so ConnectPK dials a fresh one rather than
			// handing back the same corpse.
			if d := r.node.DMSG(); d != nil {
				_ = d.CloseConn(peerPK) //nolint:errcheck
			}
		}
	}
}

// lookupAttempts is the total number of tries one Lookup may make. Three
// rather than two because a single re-read does not close the stale-Root
// window: measured against a publisher on a 30-second cadence, one retry left
// a residual ~1.7% of lookups reporting a false miss, since the second attempt
// can land in the same collection window as the first. Each extra attempt is
// only paid on a failure that is already known to be untrustworthy.
const lookupAttempts = 3

// retryKind says how (and whether) a failed attempt is worth repeating.
type retryKind int

const (
	retryNone   retryKind = iota // the answer is real; repeating costs a round-trip for nothing
	retryReread                  // the Root went stale under us; re-preview on the same conn
	retryRedial                  // the connection is gone; drop it and dial again
)

// lookupOnce is one attempt.
func (r *Reader) lookupOnce(ctx context.Context, peerPK, feed cipher.PubKey, walk WalkFunc) (Stats, retryKind, error) {
	started := time.Now()
	c, err := r.conn(ctx, peerPK)
	if err != nil {
		return Stats{Elapsed: time.Since(started)}, retryRedial, err
	}

	type result struct {
		stats Stats
		err   error
		retry retryKind
	}
	done := make(chan result, 1)

	go func() {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = started.Add(DefaultResponseTimeout)
		}
		bg := &boundedGetter{inner: c.Getter(), max: r.maxObjects, deadline: deadline}

		var walkErr error
		perr := c.Preview(skycipher.PubKey(feed), func(_ registry.Pack, root *registry.Root) bool {
			// Deliberately NOT the pack handed in: this one is wrapped in the
			// object/deadline budget, and counts what the lookup cost.
			pack, e := r.node.Container().Preview(root, bg)
			if e != nil {
				walkErr = fmt.Errorf("preview pack: %w", e)
				return false
			}
			if len(root.Refs) == 0 {
				walkErr = errors.New("cxopreview: previewed root has no refs")
				return false
			}
			var rootNode treestore.TreeNode
			if e := root.Refs[0].Value(pack, &rootNode); e != nil {
				walkErr = fmt.Errorf("root node: %w", e)
				return false
			}
			walkErr = walk(pack, &rootNode)
			return false // never subscribe — this is the whole point
		})
		res := result{
			stats: Stats{Objects: bg.count(), Bytes: bg.byteCount(), Elapsed: time.Since(started)},
			err:   walkErr,
		}
		switch {
		case perr != nil:
			res.err, res.retry = perr, retryRedial
		case walkErr != nil && bg.missed() > 0:
			// The walk failed AND at least one object could not be fetched, so
			// "not found" here is not trustworthy — the Root we pinned has
			// been collected under us. See Lookup's doc.
			res.retry = retryReread
		}
		done <- res
	}()

	select {
	case res := <-done:
		return res.stats, res.retry, res.err
	case <-ctx.Done():
		return Stats{Elapsed: time.Since(started)}, retryNone, ctx.Err()
	}
}

// boundedGetter wraps the connection's on-demand object getter with an object
// count cap and a wall-clock deadline. Past either bound every Get fails with
// ErrBudget, which unwinds the treestore walk cleanly (the walk helpers treat
// an unreadable child as absent) instead of hanging. Mirrors the bound the TPD
// aggregator puts on its targeted discovery-leaf fetch.
type boundedGetter struct {
	inner    skyobject.Getter
	max      int
	deadline time.Time

	mu     sync.Mutex
	n      int
	bytes  int
	misses int
}

func (g *boundedGetter) Get(key skycipher.SHA256) ([]byte, error) {
	g.mu.Lock()
	over := g.n >= g.max || time.Now().After(g.deadline)
	if !over {
		g.n++
	}
	g.mu.Unlock()
	if over {
		return nil, ErrBudget
	}
	val, err := g.inner.Get(key)
	if err != nil {
		g.mu.Lock()
		g.misses++
		g.mu.Unlock()
		return nil, err
	}
	g.mu.Lock()
	g.bytes += len(val)
	g.mu.Unlock()
	return val, nil
}

func (g *boundedGetter) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n
}

func (g *boundedGetter) byteCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bytes
}

// missed reports how many object fetches the peer could not answer. A non-zero
// count during a failed walk means the pinned Root was collected mid-walk, not
// that the record is absent.
func (g *boundedGetter) missed() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.misses
}
