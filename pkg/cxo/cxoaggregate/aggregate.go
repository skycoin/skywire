// Package cxoaggregate pkg/cxo/cxoaggregate/aggregate.go c4-net-discovery
//
// Shared engine for the service-side CXO fan-in aggregators. It is the
// receive-side counterpart to pkg/cxo/treestore (the publisher side):
// visors publish a signed feed under their own PK and AnnounceTo a
// service; the service owns one CXO Node listening on a DMSG port,
// subscribes to each visor's feed on connect, and gets a callback per
// filled Root.
//
// dmsg-discovery (client-entry registration), the address resolver
// (bind registration) and the transport discovery (telemetry +
// transport lists) each ran a private, hand-copied version of this
// lifecycle. The copies drifted, and the drift was invisible: dmsgd's
// built its node from a bare node.NewConfig() with no SecKey, so
// node.NewNode minted a RANDOM keypair and every gated visor refused
// its subscribe — no visor in the deployment registered over CXO until
// #4571. TPD had hit the same class of bug earlier (#4168), and its
// second aggregator had separately failed to construct because the
// node's default TCP/RPC listeners collided (#4152).
//
// So the service identity is NOT an optional config field here. It is a
// required positional argument to New, and New additionally verifies
// that the constructed node's ID is the PK that key derives — a service
// that forgets to bind its key does not compile, and one that binds the
// wrong key fails loudly at startup instead of being silently refused by
// every visor for months.
//
// What is shared: node construction + identity binding, the DMSG
// listener, the connect-driven reconcile/subscribe loop, the grace-gated
// orphan-feed reclaim, cleanup and close. What is NOT shared: everything
// about the feed's payload. Callers supply Root-lifecycle hooks and do
// their own tree walking, decoding and sink dispatch.
package cxoaggregate

import (
	"context"
	"errors"
	"sync"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// ErrNoServiceKey is returned by New when the service secret key is the
// zero value. Constructing the node anyway is what produced #4569: a
// zero key makes node.NewNode mint a random keypair, the aggregator then
// presents an unknown PK on every handshake, and every gated visor
// rejects its subscribe. Failing here makes that a startup error instead
// of a silent fleet-wide outage.
var ErrNoServiceKey = errors.New("cxoaggregate: a service SecKey is required to bind the CXO node identity")

// ErrIdentityMismatch is returned by New when the constructed node's ID
// is not the PK derived from the supplied SecKey. This should be
// impossible; it is asserted because the consequence of it being wrong
// is invisible in logs and fatal to the feature.
var ErrIdentityMismatch = errors.New("cxoaggregate: CXO node identity does not match the configured service key")

// orphanGraceTicks is how many consecutive cleanup ticks a feed must
// have zero connected conns before it is reclaimed. At the 2-minute
// default CleanupInterval this is a ~4-minute grace — long enough to
// ride out a visor's dmsg reconnect without churning a stable feed.
const orphanGraceTicks = 2

// Options tunes the shared aggregator. Zero values get sane defaults;
// the service identity and DMSG port are NOT here, they are required
// arguments to New.
type Options struct {
	// ReconcileInterval is how often the aggregator re-scans the CXO
	// node's connection list and ensures each conn is subscribed to its
	// peer's feed. Defaults to 30s.
	ReconcileInterval time.Duration
	// CleanupInterval is how often superseded Roots are pruned (keeping
	// only the latest per feed), orphan feeds reclaimed, and freed
	// objects swept. Without this the store grows without bound as visor
	// PKs churn. Defaults to 2m.
	CleanupInterval time.Duration
	// MaxFillingTime is the per-fill STALL cap: the max time a Root fill
	// may go without fetching an object before the node aborts it. The
	// node re-arms it on every fetched object, so a large Root that keeps
	// making progress is never aborted. Defaults to 90s.
	MaxFillingTime time.Duration
	// MaxTotalFillTime is the hard ceiling on one Root's total fill time,
	// independent of progress. Zero leaves the CXO node package default
	// (10m). When set it must exceed MaxFillingTime.
	MaxTotalFillTime time.Duration
	// Logger overrides the default tagged logger.
	Logger *logging.Logger
	// LogTag prefixes the engine's own log lines, so each service's
	// operational log text stays what it was. Defaults to "CXO
	// aggregator".
	LogTag string
	// InMemoryDB / DataDir control the CXO storage. Default is in-memory:
	// each of these services has an authoritative store elsewhere (redis,
	// a discovery DB), and the CXO cache exists only to satisfy CXO's
	// filling protocol.
	InMemoryDB bool
	DataDir    string

	// NodeConfig, when set, is called with the fully prepared node
	// config immediately before node.NewNode. It is the escape hatch for
	// per-service node tuning that is not worth a field here (TPD wires
	// the cxo node's internal logger pins through it). It MUST NOT change
	// SecKey — the identity assertion in New would reject that anyway.
	NodeConfig func(cfg *node.Config)

	// OnRootReceived, when set, is invoked when a Root payload arrives
	// from a subscribed feed, before filling. Runs on the CXO node's
	// per-head event loop: it must not block.
	OnRootReceived func(conn *node.Conn, r *registry.Root) error
	// OnRootFilled is invoked once a Root and all its referenced objects
	// have replicated. This is the main dispatch trigger — a nil hook
	// makes the aggregator a no-op sink, which is only ever a caller bug.
	OnRootFilled func(r *registry.Root)
	// OnFillingBreaks, when set, is invoked when replication of a Root's
	// objects fails. Services log this themselves (at their own level)
	// and TPD additionally attempts a partial-fill recovery.
	OnFillingBreaks func(r *registry.Root, reason error)
	// OnFeedReclaimed, when set, is invoked after a feed has been
	// un-shared and deleted by the orphan reclaim, so a service can drop
	// any per-feed state it caches.
	OnFeedReclaimed func(feed skycipher.PubKey)
}

// Core is the shared aggregator engine: one CXO Node listening on DMSG,
// a reconcile loop that subscribes each connected peer's own feed, and a
// cleanup loop that prunes Roots and reclaims orphan feeds.
type Core struct {
	cxoNode *node.Node
	opts    Options
	log     *logging.Logger
	tag     string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	// nudge triggers an immediate reconcile out of band from the ticker.
	// Buffered(1) so a burst of connects coalesces into one pending
	// reconcile (idempotent — it walks the full conn set anyway).
	nudge chan struct{}

	// orphanStrikes counts consecutive cleanup ticks a feed has had no
	// connected conn. Touched only from cleanup() (single goroutine).
	orphanStrikes map[skycipher.PubKey]int
}

// New constructs the shared aggregator engine.
//
// sk is the SERVICE secret key and is required: the node's
// handshake-advertised PK must be the PK gated visors allowlist for this
// service (dmsg-discovery's, the AR's, TPD's). Passing it positionally
// is deliberate — as an optional Config field it was omitted for months
// on dmsg-discovery and every visor's subscribe was refused (#4569).
//
// dmsgPort is the DMSG port the node listens on (and dials visors back
// on); it is required for the same reason — a zero port silently listens
// somewhere the visors are not announcing.
func New(dmsgC *dmsg.Client, sk cipher.SecKey, dmsgPort uint16, opts Options) (*Core, error) {
	if sk == (cipher.SecKey{}) {
		return nil, ErrNoServiceKey
	}
	wantPK, err := sk.PubKey()
	if err != nil {
		return nil, err
	}
	if dmsgPort == 0 {
		return nil, errors.New("cxoaggregate: a non-zero DMSG port is required")
	}
	if dmsgC == nil {
		return nil, errors.New("cxoaggregate: a dmsg client is required")
	}

	if opts.ReconcileInterval <= 0 {
		opts.ReconcileInterval = 30 * time.Second
	}
	if opts.CleanupInterval <= 0 {
		opts.CleanupInterval = 2 * time.Minute
	}
	if opts.MaxFillingTime <= 0 {
		opts.MaxFillingTime = 90 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = logging.MustGetLogger("cxo-aggregator")
	}
	if opts.LogTag == "" {
		opts.LogTag = "CXO aggregator"
	}

	cfg := node.NewConfig()
	// Bind the node identity to the service key. This is the whole point
	// of the package: without it node.NewNode mints a random keypair and
	// every gated visor refuses the subscribe.
	cfg.SecKey = skycipher.SecKey(sk)
	// DMSG-only — disable the node's default TCP/UDP/RPC listeners.
	// node.NewConfig defaults TCP.Listen to ":8870" and RPC to ":8871",
	// hardcoded, so two Nodes in one process collide on bind and the
	// SECOND NewNode fails with "address already in use" (#4152: TPD's
	// tp-list aggregator never came up). None of these listeners are
	// reachable over DMSG anyway, and the publisher path
	// (treestore.NewWithDMSG) has always zeroed them.
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	cfg.MaxFillingTime = opts.MaxFillingTime
	if opts.MaxTotalFillTime > 0 {
		cfg.MaxTotalFillTime = opts.MaxTotalFillTime
	}
	cfg.Config = skyobject.NewConfig()
	cfg.Config.InMemoryDB = opts.InMemoryDB || opts.DataDir == ""
	if opts.DataDir != "" {
		cfg.Config.DataDir = opts.DataDir
	}
	if opts.NodeConfig != nil {
		opts.NodeConfig(cfg)
		// Re-assert: the escape hatch must not be able to unbind the
		// identity, which is the one thing this constructor guarantees.
		cfg.SecKey = skycipher.SecKey(sk)
	}

	cxoNode, err := node.NewNode(cfg)
	if err != nil {
		return nil, err
	}
	// Assert the binding actually took. Cheap, and the failure it guards
	// is invisible in logs and fatal to the feature.
	if cipher.PubKey(cxoNode.ID()) != wantPK {
		_ = cxoNode.Close() //nolint:errcheck
		return nil, ErrIdentityMismatch
	}

	factory := cxotransport.NewDMSGFactory(dmsgC, dmsgPort)
	if err := cxoNode.EnableDMSG(factory); err != nil {
		_ = cxoNode.Close() //nolint:errcheck
		return nil, err
	}

	c := &Core{
		cxoNode:       cxoNode,
		opts:          opts,
		log:           opts.Logger,
		tag:           opts.LogTag,
		done:          make(chan struct{}),
		nudge:         make(chan struct{}, 1),
		orphanStrikes: make(map[skycipher.PubKey]int),
	}

	// Subscribe the moment a visor dials in, rather than waiting up to a
	// full ReconcileInterval. The conn's handshake completes (peerID set)
	// before OnConnect fires, so the nudged reconcile can subscribe at
	// once; it stays idempotent via alreadySubscribed.
	cxoNode.Config().OnConnect = func(_ *node.Conn) error {
		c.Nudge()
		return nil
	}
	if h := opts.OnRootReceived; h != nil {
		cxoNode.Config().OnRootReceived = h
	}
	if h := opts.OnRootFilled; h != nil {
		cxoNode.Config().OnRootFilled = func(_ *node.Node, r *registry.Root) { h(r) }
	}
	if h := opts.OnFillingBreaks; h != nil {
		cxoNode.Config().OnFillingBreaks = func(_ *node.Node, r *registry.Root, reason error) { h(r, reason) }
	}
	return c, nil
}

// Node returns the underlying CXO node, for services that need the
// container, the DMSG transport or the connection set.
func (c *Core) Node() *node.Node { return c.cxoNode }

// Container returns the node's object container.
func (c *Core) Container() *skyobject.Container { return c.cxoNode.Container() }

// FeedPK returns the aggregator's own CXO node identity — by
// construction the service PK derived from the SecKey given to New.
func (c *Core) FeedPK() cipher.PubKey { return cipher.PubKey(c.cxoNode.ID()) }

// Nudge requests an out-of-band reconcile. Non-blocking and coalescing;
// safe to call from any goroutine.
func (c *Core) Nudge() {
	select {
	case c.nudge <- struct{}{}:
	default:
	}
}

// Run starts the reconcile + cleanup loops. Returns immediately; the
// loops run until ctx is canceled or Close is called. Idempotent.
func (c *Core) Run(ctx context.Context) {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.mu.Unlock()
	go c.loop(loopCtx)
}

// Close stops the loops and tears down the CXO node. Idempotent.
func (c *Core) Close() error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		<-c.done
	}
	return c.cxoNode.Close()
}

func (c *Core) loop(ctx context.Context) {
	defer close(c.done)
	t := time.NewTicker(c.opts.ReconcileInterval)
	defer t.Stop()
	ct := time.NewTicker(c.opts.CleanupInterval)
	defer ct.Stop()

	c.reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.reconcile()
		case <-ct.C:
			c.cleanup()
		case <-c.nudge:
			// A visor just connected (OnConnect) or a dial-back landed.
			// Subscribe now instead of waiting for the next tick;
			// reconcile is idempotent.
			c.reconcile()
		}
	}
}

// reconcile subscribes to every connected peer's own feed (feed PK ==
// peer PK) that isn't already subscribed. Conns that dropped since the
// last reconcile simply aren't in the list anymore.
func (c *Core) reconcile() {
	for _, conn := range c.cxoNode.Connections() {
		peerPK := conn.PeerID()
		if peerPK == (skycipher.PubKey{}) {
			// Conn handshake hasn't completed; skip until next tick.
			continue
		}
		if alreadySubscribed(conn, peerPK) {
			continue
		}
		if err := conn.Subscribe(peerPK); err != nil {
			c.log.WithError(err).WithField("visor", cipher.PubKey(peerPK)).
				Debug(c.tag + ": Subscribe failed; will retry next reconcile")
			continue
		}
		// Info, not Debug: this is the state change that answers "is
		// registration-over-CXO actually working for this service", it fires once
		// per visor feed (alreadySubscribed guards the retry), and the services
		// that run it are NOT started with --loglvl debug. At Debug the answer was
		// unobtainable in production -- AR carried 464 live CXO connections while
		// its log showed no aggregator activity at all, which reads identically to
		// the feature being switched off.
		c.log.WithField("visor", cipher.PubKey(peerPK)).
			Info(c.tag + ": subscribed to visor feed")
	}
}

// alreadySubscribed reports whether conn is already subscribed to the
// given feed. Keeps reconcile idempotent across repeat ticks.
func alreadySubscribed(conn *node.Conn, feed skycipher.PubKey) bool {
	for _, f := range conn.Feeds() {
		if f == feed {
			return true
		}
	}
	return false
}

// cleanup prunes superseded Roots (keeping only the latest per feed),
// reclaims feeds whose visor has gone away, then sweeps ownerless
// objects. Without this the store grows without bound as visor PKs churn
// (each keeps its final Root tree pinned at rc>0).
func (c *Core) cleanup() {
	cont := c.cxoNode.Container()
	if err := cxoutils.RemoveRootObjects(cont, 1); err != nil {
		c.log.WithError(err).Debug(c.tag + ": RemoveRootObjects failed; will retry next tick")
		return
	}
	c.reclaimOrphanFeeds(cont)
	if err := cxoutils.RemoveObjects(cont); err != nil {
		c.log.WithError(err).Debug(c.tag + ": RemoveObjects failed; will retry next tick")
	}
}

// reclaimOrphanFeeds un-shares and hard-deletes feeds that no longer
// have a connected conn, grace-gated by orphanGraceTicks so a brief drop
// plus redial keeps the feed. DontShare unsubscribes the conns and drops
// the feed from the node's share set; DelFeed deletes its Roots and
// decrements the referenced objects to rc==0 so the following
// RemoveObjects sweep can reclaim them.
func (c *Core) reclaimOrphanFeeds(cont *skyobject.Container) {
	connected := make(map[skycipher.PubKey]struct{})
	for _, conn := range c.cxoNode.Connections() {
		if pk := conn.PeerID(); pk != (skycipher.PubKey{}) {
			connected[pk] = struct{}{}
		}
	}
	self := c.cxoNode.ID()
	for _, feed := range cont.Feeds() {
		if feed == self {
			// The node's own identity feed — never reclaim it.
			delete(c.orphanStrikes, feed)
			continue
		}
		if _, ok := connected[feed]; ok {
			// Live feed — clear any accumulated strikes.
			delete(c.orphanStrikes, feed)
			continue
		}
		c.orphanStrikes[feed]++
		if c.orphanStrikes[feed] < orphanGraceTicks {
			continue
		}
		delete(c.orphanStrikes, feed)
		if err := c.cxoNode.DontShare(feed); err != nil {
			c.log.WithError(err).WithField("visor", cipher.PubKey(feed)).
				Debug(c.tag + ": DontShare orphan feed failed; will retry next tick")
			continue
		}
		if err := cont.DelFeed(feed); err != nil {
			c.log.WithError(err).WithField("visor", cipher.PubKey(feed)).
				Debug(c.tag + ": DelFeed orphan feed failed; will retry next tick")
			continue
		}
		if h := c.opts.OnFeedReclaimed; h != nil {
			h(feed)
		}
		c.log.WithField("visor", cipher.PubKey(feed)).
			Debug(c.tag + ": reclaimed orphan feed (no connected conn)")
	}
}
