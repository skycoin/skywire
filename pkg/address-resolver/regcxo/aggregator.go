// Package regcxo pkg/address-resolver/regcxo/aggregator.go c4-net-discovery
//
// AR-bind-over-CXO aggregator — the address-resolver side of the fan-in
// path. Visors publish their AR bindings (the stcpr/sudph/quic/wt
// reachable-address payloads they POST to /bind) as a CXO feed and
// AnnounceTo this service; the aggregator owns one CXO Node listening on
// DmsgVisorARBindCXOPort, subscribes to each visor's feed on connect, and
// on every filled Root reads the per-type bind leaves and hands each to
// the Sink (the AR API's IngestBindFromCXO).
//
// This moves address binding off the timer-driven re-registration — each a
// fresh dmsg stream with a full Noise handshake (the secp256k1
// handshakeResponder that dominates AR CPU) — onto a persistent CXO
// connection kept warm by the treestore heartbeat. The HTTP/UDP bind path
// remains authoritative, so ingest is idempotent (see IngestBindFromCXO).
//
// Structure mirrors pkg/dmsg/discovery/regcxo, widened to several typed
// leaves: same connect-driven subscribe, same grace-gated orphan-feed
// reclaim that keeps the in-memory CXDS from growing without bound as visor
// PKs churn. The node identity is bound to the AR's service SecKey so its
// handshake PK is the AR PK gated visors allowlist (mirrors #4168).
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
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// cxoBindLeaf maps a CXO leaf name to the transport type it carries. The leaf
// name is the type's canonical wire string, matching what the visor's AR-bind
// publisher Puts (see addrresolver bind hooks + pkg/visor/init_ar_bind_cxo.go).
type cxoBindLeaf struct {
	name string
	t    types.Type
}

// cxoBindLeaves is the fixed set of per-type leaves the AR-bind feed carries.
var cxoBindLeaves = []cxoBindLeaf{
	{"stcpr", types.STCPR},
	{"sudph", types.SUDPH},
	{"squicr", types.QUIC},
	{"swtr", types.WT},
}

// Sink ingests bindings replicated from visor AR-bind feeds. The AR API
// satisfies it via (*api.API).IngestBindFromCXO.
type Sink interface {
	IngestBindFromCXO(ctx context.Context, reporter cipher.PubKey, tpType types.Type, la addrresolver.LocalAddresses)
}

// Config tunes the aggregator loops. Zero values get sane defaults.
type Config struct {
	ReconcileInterval time.Duration
	CleanupInterval   time.Duration
	MaxFillingTime    time.Duration
	Logger            *logging.Logger
	InMemoryDB        bool
	DataDir           string
	// SecKey binds the aggregator's CXO node identity to the AR's service
	// secret key so the node's handshake-advertised PK is the AR's KNOWN PK.
	// This matters because a visor gates its feed subscriber allowlist on the
	// CXO node's PeerID: it allows the AR PK it holds in
	// transport.address_resolver_dmsg. Left zero, node.NewNode generates a
	// RANDOM keypair, so the aggregator dials every gated visor as an unknown
	// PK and is rejected (the #4168 bug on TPD's aggregator). The publisher
	// path (treestore.NewWithDMSG) binds the same way for the same reason.
	SecKey cipher.SecKey
}

// orphanGraceTicks is how many consecutive cleanup ticks a feed must have
// zero connected conns before it is reclaimed. At the 2-minute default
// CleanupInterval this is a ~4-minute grace — long enough to ride out a
// visor's dmsg reconnect without churning a stable feed.
const orphanGraceTicks = 2

// Aggregator owns one CXO Node listening on DMSG; visors dial in, subscribe
// happens per-conn during reconcile, and OnRootFilled reads the bind leaves
// and forwards them to the Sink.
type Aggregator struct {
	cxoNode *node.Node
	sink    Sink
	conf    Config
	log     *logging.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	// nudge triggers an immediate reconcile out of band from the ticker.
	// Buffered(1) so a burst of connects coalesces into one pending reconcile
	// (idempotent — it walks the full conn set anyway).
	nudge chan struct{}

	// orphanStrikes counts consecutive cleanup ticks a feed has had no
	// connected conn. Touched only from cleanup() (single goroutine).
	orphanStrikes map[skycipher.PubKey]int
}

// New constructs an Aggregator: a CXO Node with DMSG enabled on
// DmsgVisorARBindCXOPort so remote visors can dial in, wired to forward each
// filled Root's bind leaves to sink.
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
		conf.Logger = logging.MustGetLogger("ar-bind-cxo")
	}

	cfg := node.NewConfig()
	cfg.MaxFillingTime = conf.MaxFillingTime
	cfg.Config = skyobject.NewConfig()
	cfg.Config.InMemoryDB = conf.InMemoryDB || conf.DataDir == ""
	if conf.DataDir != "" {
		cfg.Config.DataDir = conf.DataDir
	}
	// Bind the node identity to the AR's service key (when provided) so its
	// handshake PK is the AR's known PK, matching what gated visors allowlist.
	// Zero SecKey => node.NewNode mints a random keypair => gated visors reject
	// the aggregator's subscribe. See Config.SecKey.
	if conf.SecKey != (cipher.SecKey{}) {
		cfg.SecKey = skycipher.SecKey(conf.SecKey)
	}
	// We're DMSG-only — disable the CXO node's default TCP/RPC listeners.
	// node.NewConfig defaults TCP.Listen to ":8870" and RPC to ":8871"; those
	// hardcoded ports would collide with any other CXO node in the same process
	// (e.g. when the multi-service supervisor runs AR alongside another CXO
	// service). DMSG is enabled separately below.
	cfg.TCP.Listen = ""
	cfg.RPC = ""

	cxoNode, err := node.NewNode(cfg)
	if err != nil {
		return nil, err
	}
	factory := cxotransport.NewDMSGFactory(dmsgC, skyenv.DmsgVisorARBindCXOPort)
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

	// Subscribe the moment a visor dials in, rather than waiting up to a full
	// ReconcileInterval. The conn's handshake completes (peerID set) before
	// OnConnect fires, so the nudged reconcile can subscribe at once; it stays
	// idempotent via alreadySubscribed.
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
			Debug("ar-bind-cxo aggregator: root filling broke")
	}
	return a, nil
}

// Run starts the reconcile + cleanup loops. Returns immediately; the loop runs
// until ctx is canceled or Close is called. Idempotent.
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

// FeedPK returns the aggregator's own CXO node identity (the AR PK when SecKey
// is bound).
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

// reconcile subscribes to every connected visor's own feed (feed PK == peer
// PK) that isn't already subscribed. Dropped conns simply aren't in the list
// anymore.
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
				Debug("ar-bind-cxo aggregator: Subscribe failed; will retry next reconcile")
			continue
		}
		a.log.WithField("visor", cipher.PubKey(peerPK)).
			Debug("ar-bind-cxo aggregator: subscribed to visor feed")
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
// reclaims feeds whose visor has gone away, then sweeps ownerless objects.
// Without this the in-memory store grows without bound as visor PKs churn.
func (a *Aggregator) cleanup() {
	c := a.cxoNode.Container()
	if err := cxoutils.RemoveRootObjects(c, 1); err != nil {
		a.log.WithError(err).Debug("ar-bind-cxo aggregator: RemoveRootObjects failed; will retry next tick")
		return
	}
	a.reclaimOrphanFeeds(c)
	if err := cxoutils.RemoveObjects(c); err != nil {
		a.log.WithError(err).Debug("ar-bind-cxo aggregator: RemoveObjects failed; will retry next tick")
	}
}

// reclaimOrphanFeeds un-shares and hard-deletes feeds that no longer have a
// connected conn, grace-gated by orphanGraceTicks so a brief drop + redial
// keeps the feed.
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
				Debug("ar-bind-cxo aggregator: DontShare orphan feed failed; will retry next tick")
			continue
		}
		if err := c.DelFeed(feed); err != nil {
			a.log.WithError(err).WithField("visor", cipher.PubKey(feed)).
				Debug("ar-bind-cxo aggregator: DelFeed orphan feed failed; will retry next tick")
			continue
		}
		a.log.WithField("visor", cipher.PubKey(feed)).
			Debug("ar-bind-cxo aggregator: reclaimed orphan feed (no connected conn)")
	}
}

// handleRootFilled reads every per-type bind leaf from a filled Root and
// forwards each decoded LocalAddresses to the Sink. r.Pub is the visor whose
// feed produced this Root — the reporter PK the ingest stores the binding
// under.
func (a *Aggregator) handleRootFilled(r *registry.Root) {
	if r == nil || len(r.Refs) == 0 {
		return
	}
	reporter := cipher.PubKey(r.Pub)
	if reporter == (cipher.PubKey{}) {
		a.log.Debug("ar-bind-cxo aggregator: dropping root with zero publisher PK")
		return
	}
	pack, err := a.cxoNode.Container().Pack(r, treestore.Registry)
	if err != nil {
		a.log.WithError(err).Debug("ar-bind-cxo aggregator: get pack failed")
		return
	}
	var rootNode treestore.TreeNode
	if err := r.Refs[0].Value(pack, &rootNode); err != nil {
		a.log.WithError(err).Debug("ar-bind-cxo aggregator: decode root TreeNode failed")
		return
	}

	for _, bl := range cxoBindLeaves {
		leaf, ok := findLeaf(pack, &rootNode, bl.name)
		if !ok || len(leaf) == 0 {
			continue
		}
		var la addrresolver.LocalAddresses
		if err := json.Unmarshal(leaf, &la); err != nil {
			a.log.WithError(err).WithField("visor", reporter).WithField("type", bl.t).
				Debug("ar-bind-cxo aggregator: bind leaf decode failed")
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		a.sink.IngestBindFromCXO(ctx, reporter, bl.t, la)
		cancel()
	}
}

// findLeaf returns the leaf value stored directly under n at the given
// top-level name. The AR-bind feed is flat (one leaf per type), so a single
// level of lookup suffices — no recursive walk.
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
