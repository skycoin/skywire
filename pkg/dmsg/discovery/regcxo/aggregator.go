// Package regcxo pkg/dmsg/discovery/regcxo/aggregator.go c1-net-dmsg
//
// Registration-over-CXO aggregator — the dmsg-discovery side of the
// fan-in path. Visors that opt into registration_cxo publish their own
// signed disc.Entry as a single-leaf CXO feed and AnnounceTo this
// service; the aggregator owns one CXO Node listening on
// DmsgDMSGDRegistrationCXOPort, subscribes to each visor's feed on
// connect, and on every filled Root reads the "entry" leaf and hands the
// entry to the Sink (the discovery API's IngestEntryFromCXO).
//
// This moves client-entry registration off the timer-driven HTTP PUT —
// each a fresh dmsg stream with a full Noise + post-quantum handshake,
// the load that dominates dmsg-discovery CPU — onto a persistent CXO
// connection kept warm by the treestore heartbeat. HTTP PUT remains the
// fallback, so ingest is idempotent (see Sink.IngestEntryFromCXO).
//
// Structure mirrors pkg/deployment/tpd/cxoaggregator, trimmed to a
// single "entry" leaf: same connect-driven subscribe, same grace-gated
// orphan-feed reclaim that keeps the in-memory CXDS from growing without
// bound as visor PKs churn.
package regcxo

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// registrationEntryPath is the single leaf path a visor publishes its
// discovery entry at. MUST match pkg/visor's registrationEntryPath.
const registrationEntryPath = "entry"

// Sink ingests entries replicated from visor registration feeds. The
// discovery API satisfies it via (*api.API).IngestEntryFromCXO.
type Sink interface {
	IngestEntryFromCXO(ctx context.Context, entry *disc.Entry, reporter cipher.PubKey)
}

// Config tunes the aggregator loops. Zero values get sane defaults.
type Config struct {
	ReconcileInterval time.Duration
	CleanupInterval   time.Duration
	MaxFillingTime    time.Duration
	Logger            *logging.Logger
	InMemoryDB        bool
	DataDir           string
}

// orphanGraceTicks is how many consecutive cleanup ticks a feed must
// have zero connected conns before it is reclaimed. At the 2-minute
// default CleanupInterval this is a ~4-minute grace — long enough to
// ride out a visor's dmsg reconnect without churning a stable feed.
const orphanGraceTicks = 2

// Aggregator owns one CXO Node listening on DMSG; visors dial in,
// subscribe happens per-conn during reconcile, and OnRootFilled reads
// the entry leaf and forwards it to the Sink.
type Aggregator struct {
	cxoNode *node.Node
	sink    Sink
	conf    Config
	log     *logging.Logger

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

// New constructs an Aggregator: a CXO Node with DMSG enabled on
// DmsgDMSGDRegistrationCXOPort so remote visors can dial in, wired to
// forward each filled Root's entry leaf to sink.
func New(dmsgC *dmsg.Client, sink Sink, conf Config) (*Aggregator, error) {
	if conf.ReconcileInterval <= 0 {
		conf.ReconcileInterval = 30 * time.Second
	}
	if conf.CleanupInterval <= 0 {
		conf.CleanupInterval = 2 * time.Minute
	}
	if conf.MaxFillingTime <= 0 {
		conf.MaxFillingTime = 90 * time.Second
	}
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("dmsgd-registration-cxo")
	}

	cfg := node.NewConfig()
	cfg.MaxFillingTime = conf.MaxFillingTime
	cfg.Config = skyobject.NewConfig()
	cfg.Config.InMemoryDB = conf.InMemoryDB || conf.DataDir == ""
	if conf.DataDir != "" {
		cfg.Config.DataDir = conf.DataDir
	}

	cxoNode, err := node.NewNode(cfg)
	if err != nil {
		return nil, err
	}
	factory := cxotransport.NewDMSGFactory(dmsgC, skyenv.DmsgDMSGDRegistrationCXOPort)
	if err := cxoNode.EnableDMSG(factory); err != nil {
		_ = cxoNode.Close() //nolint:errcheck
		return nil, err
	}

	a := &Aggregator{
		cxoNode:       cxoNode,
		sink:          sink,
		conf:          conf,
		log:           conf.Logger,
		done:          make(chan struct{}),
		nudge:         make(chan struct{}, 1),
		orphanStrikes: make(map[skycipher.PubKey]int),
	}

	// Subscribe the moment a visor dials in, rather than waiting up to a
	// full ReconcileInterval. The conn's handshake completes (peerID set)
	// before OnConnect fires, so the nudged reconcile can subscribe at
	// once; it stays idempotent via alreadySubscribed.
	cxoNode.Config().OnConnect = func(_ *node.Conn) error {
		select {
		case a.nudge <- struct{}{}:
		default:
		}
		return nil
	}
	cxoNode.Config().OnRootFilled = func(_ *node.Node, r *registry.Root) {
		a.handleRootFilled(r)
	}
	cxoNode.Config().OnFillingBreaks = func(_ *node.Node, r *registry.Root, reason error) {
		a.log.WithError(reason).WithField("visor", cipher.PubKey(r.Pub)).
			Debug("registration-cxo aggregator: root filling broke")
	}
	return a, nil
}

// Run starts the reconcile + cleanup loops. Returns immediately; the loop
// runs until ctx is canceled or Close is called. Idempotent.
func (a *Aggregator) Run(ctx context.Context) {
	a.mu.Lock()
	if a.cancel != nil {
		a.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()
	go a.loop(loopCtx)
}

// Close stops the loops and tears down the CXO node. Idempotent.
func (a *Aggregator) Close() error {
	a.mu.Lock()
	cancel := a.cancel
	a.cancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		<-a.done
	}
	return a.cxoNode.Close()
}

// FeedPK returns the aggregator's own CXO node identity.
func (a *Aggregator) FeedPK() cipher.PubKey { return cipher.PubKey(a.cxoNode.ID()) }

func (a *Aggregator) loop(ctx context.Context) {
	defer close(a.done)
	t := time.NewTicker(a.conf.ReconcileInterval)
	defer t.Stop()
	ct := time.NewTicker(a.conf.CleanupInterval)
	defer ct.Stop()

	a.reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.reconcile()
		case <-ct.C:
			a.cleanup()
		case <-a.nudge:
			a.reconcile()
		}
	}
}

// reconcile subscribes to every connected visor's own feed (feed PK ==
// peer PK) that isn't already subscribed. Dropped conns simply aren't in
// the list anymore.
func (a *Aggregator) reconcile() {
	for _, conn := range a.cxoNode.Connections() {
		peerPK := conn.PeerID()
		if peerPK == (skycipher.PubKey{}) {
			continue
		}
		if alreadySubscribed(conn, peerPK) {
			continue
		}
		if err := conn.Subscribe(peerPK); err != nil {
			a.log.WithError(err).WithField("visor", cipher.PubKey(peerPK)).
				Debug("registration-cxo aggregator: Subscribe failed; will retry next reconcile")
			continue
		}
		a.log.WithField("visor", cipher.PubKey(peerPK)).
			Debug("registration-cxo aggregator: subscribed to visor feed")
	}
}

func alreadySubscribed(conn *node.Conn, feed skycipher.PubKey) bool {
	for _, f := range conn.Feeds() {
		if f == feed {
			return true
		}
	}
	return false
}

// cleanup prunes superseded Roots (keeping only the latest per feed) and
// reclaims feeds whose visor has gone away, then sweeps ownerless
// objects. Without this the in-memory store grows without bound as visor
// PKs churn (each keeps its final Root tree pinned).
func (a *Aggregator) cleanup() {
	c := a.cxoNode.Container()
	if err := cxoutils.RemoveRootObjects(c, 1); err != nil {
		a.log.WithError(err).Debug("registration-cxo aggregator: RemoveRootObjects failed; will retry next tick")
		return
	}
	a.reclaimOrphanFeeds(c)
	if err := cxoutils.RemoveObjects(c); err != nil {
		a.log.WithError(err).Debug("registration-cxo aggregator: RemoveObjects failed; will retry next tick")
	}
}

// reclaimOrphanFeeds un-shares and hard-deletes feeds that no longer have
// a connected conn, grace-gated by orphanGraceTicks so a brief drop +
// redial keeps the feed.
func (a *Aggregator) reclaimOrphanFeeds(c *skyobject.Container) {
	connected := make(map[skycipher.PubKey]struct{})
	for _, conn := range a.cxoNode.Connections() {
		if pk := conn.PeerID(); pk != (skycipher.PubKey{}) {
			connected[pk] = struct{}{}
		}
	}
	self := a.cxoNode.ID()
	for _, feed := range c.Feeds() {
		if feed == self {
			delete(a.orphanStrikes, feed)
			continue
		}
		if _, ok := connected[feed]; ok {
			delete(a.orphanStrikes, feed)
			continue
		}
		a.orphanStrikes[feed]++
		if a.orphanStrikes[feed] < orphanGraceTicks {
			continue
		}
		delete(a.orphanStrikes, feed)
		if err := a.cxoNode.DontShare(feed); err != nil {
			a.log.WithError(err).WithField("visor", cipher.PubKey(feed)).
				Debug("registration-cxo aggregator: DontShare orphan feed failed; will retry next tick")
			continue
		}
		if err := c.DelFeed(feed); err != nil {
			a.log.WithError(err).WithField("visor", cipher.PubKey(feed)).
				Debug("registration-cxo aggregator: DelFeed orphan feed failed; will retry next tick")
			continue
		}
		a.log.WithField("visor", cipher.PubKey(feed)).
			Debug("registration-cxo aggregator: reclaimed orphan feed (no connected conn)")
	}
}

// handleRootFilled reads the "entry" leaf from a filled Root and forwards
// the decoded disc.Entry to the Sink. r.Pub is the visor whose feed
// produced this Root — the reporter PK the ingest checks against
// entry.Static.
func (a *Aggregator) handleRootFilled(r *registry.Root) {
	if r == nil || len(r.Refs) == 0 {
		return
	}
	reporter := cipher.PubKey(r.Pub)
	if reporter == (cipher.PubKey{}) {
		a.log.Debug("registration-cxo aggregator: dropping root with zero publisher PK")
		return
	}
	pack, err := a.cxoNode.Container().Pack(r, treestore.Registry)
	if err != nil {
		a.log.WithError(err).Debug("registration-cxo aggregator: get pack failed")
		return
	}
	var rootNode treestore.TreeNode
	if err := r.Refs[0].Value(pack, &rootNode); err != nil {
		a.log.WithError(err).Debug("registration-cxo aggregator: decode root TreeNode failed")
		return
	}

	leaf, ok := findLeaf(pack, &rootNode, registrationEntryPath)
	if !ok || len(leaf) == 0 {
		return
	}
	entry := new(disc.Entry)
	if err := json.Unmarshal(leaf, entry); err != nil {
		a.log.WithError(err).WithField("visor", reporter).
			Debug("registration-cxo aggregator: entry leaf decode failed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.sink.IngestEntryFromCXO(ctx, entry, reporter)
}

// findLeaf returns the leaf value stored directly under n at the given
// top-level name. The registration feed is flat (one "entry" leaf), so a
// single level of lookup suffices — no recursive walk.
func findLeaf(pack registry.Pack, n *treestore.TreeNode, name string) ([]byte, bool) {
	count, err := n.Children.Len(pack)
	if err != nil {
		return nil, false
	}
	for i := 0; i < count; i++ {
		var entry treestore.TreeEntry
		if _, err := n.Children.ValueByIndex(pack, i, &entry); err != nil {
			continue
		}
		if entry.Name == name && len(entry.Leaf) > 0 {
			return entry.Leaf, true
		}
	}
	return nil, false
}
