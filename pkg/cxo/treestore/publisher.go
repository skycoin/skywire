// Package treestore — pkg/cxo/treestore/publisher.go: hierarchical
// CXO publisher.
//
// The publisher maintains an in-memory tree of named entries. Each
// Put / Delete mutates the in-memory tree and (after a debounce
// window) re-builds the corresponding CXO object graph and saves a
// new Root. Content-addressing means unchanged subtrees keep the
// same hashes and are not re-fetched by subscribers — only the
// O(depth) chain of objects from the modified leaf up to the root
// gets re-encoded and re-broadcast.
package treestore

import (
	"errors"
	"sort"
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

// Publisher is a hierarchical CXO publisher. The in-memory tree
// is the authoritative state; CXO is the broadcast layer. Concurrent
// Put/Delete are safe; the publisher serializes its publish loop on
// a single goroutine so encoding always sees a consistent snapshot.
type Publisher struct {
	log *logging.Logger

	cxoNode  *node.Node
	ownsNode bool // true when New constructed the node (NewWithDMSG path); Close should release it
	pk       cipher.PubKey
	sk       cipher.SecKey

	mu   sync.Mutex
	root *memNode
	// dirty is set whenever the in-memory tree has unpublished
	// changes; cleared by the publish loop after a successful Save.
	dirty bool

	// allow gates which subscriber PKs may connect. nil-set means the
	// allowlist is disabled (any subscriber is accepted). NewWithDMSG
	// wires this state into the CXO node's OnSubscribeRemote hook
	// pre-NewNode so SetAllowlist takes effect immediately and there
	// is no window where the gate is unconfigured.
	allow *allowState

	batchWindow time.Duration
	wakeup       chan struct{}
	done         chan struct{}
	cleanupNudge chan struct{} // 1-buffered; non-blocking send from publishRoot
	cleanupDone  chan struct{} // closed when the cleanup goroutine exits
	stopOnce     sync.Once
}

// memNode is the in-memory mirror of a TreeNode. Children are kept
// sorted by name on demand (via sortedNames) so the on-the-wire
// encoding is deterministic and unchanged subtrees produce stable
// hashes across publishes.
type memNode struct {
	leaves map[string][]byte   // name → leaf value bytes (nil = not a leaf)
	subs   map[string]*memNode // name → child node (nil = not a sub-tree)

	// Per-publish encoding cache. pubHash is the hash this sub-node
	// resolved to in its last successful publish. cached is true when
	// the in-memory state has not changed since that publish, so the
	// next publish can place pubHash directly into the parent's
	// TreeEntry and skip both the recursive encodeNode walk and the
	// encoder.Serialize/Cache.Set chain entirely. Container.Save's
	// reference walk increments the rc on the cached hash without
	// recursing into it (lines 156-167 of skyobject/unpack.go), so the
	// underlying CXO objects stay alive across publishes as long as
	// each successive Root references the same hash.
	//
	// Any mutation that touches this node OR any descendant clears
	// cached on every node along the path from the root.
	pubHash skycipher.SHA256
	cached  bool
}

func newMemNode() *memNode {
	return &memNode{
		leaves: make(map[string][]byte),
		subs:   make(map[string]*memNode),
	}
}

// Config bundles publisher tunables. Zero values use defaults.
type Config struct {
	// BatchWindow is how long to wait after a mutation before
	// publishing. Zero or negative means publish immediately on
	// every change. The default is 1 second — short enough to feel
	// live, long enough to coalesce bursts.
	BatchWindow time.Duration

	// Logger overrides the default tag-based logger.
	Logger *logging.Logger

	// SubscriberAllowlist, when non-nil, restricts which remote PKs
	// may subscribe to the publisher's feed. nil means the gate is
	// disabled (any subscriber accepted, the historical default). An
	// empty non-nil slice means "no subscribers allowed yet" — useful
	// when staging a feed before authorizing peers via SetAllowlist.
	//
	// Callers using New directly with their own CXO node are
	// responsible for wiring the node's OnSubscribeRemote hook to
	// consult Publisher.AllowsSubscriber. NewWithDMSG wires it
	// automatically.
	SubscriberAllowlist []cipher.PubKey

	// sharedAllow is set internally by NewWithDMSG when it builds the
	// allowState before constructing the CXO node, so the OnSubscribeRemote
	// closure and the resulting Publisher share the same state. Not
	// exported.
	sharedAllow *allowState
}

// PubConfig configures NewWithDMSG. Mirrors the relevant CXO knobs
// so callers don't need to reach into the lower-level node config
// just to set a data directory.
type PubConfig struct {
	BatchWindow time.Duration   // see Config.BatchWindow
	Logger      *logging.Logger // see Config.Logger
	InMemoryDB  bool            // forwarded to skyobject.Config
	DataDir     string          // forwarded to skyobject.Config (ignored if InMemoryDB)
	// DmsgPort overrides the listener port; zero falls back to
	// cxotransport.DefaultCXOPort. Multiple publishers under the same
	// PK can coexist as long as they use distinct ports — useful for
	// hosting independent CXO feeds on a single visor.
	DmsgPort uint16

	// SubscriberAllowlist, when non-nil, gates the OnSubscribeRemote
	// hook so only listed PKs may subscribe. See Config.SubscriberAllowlist.
	SubscriberAllowlist []cipher.PubKey
}

// NewWithDMSG is a convenience wrapper around New: it constructs a
// CXO node attached to dmsgC, enables DMSG transport, and hands back
// a Publisher with the resulting node already wired. The Publisher
// owns the node — Close releases both.
func NewWithDMSG(dmsgC *dmsg.Client, sk cipher.SecKey, conf PubConfig) (*Publisher, error) {
	cfg := node.NewConfig()
	// Bind the CXO node's identity to the publisher's keypair so the
	// node's handshake-advertised PK matches the feed PK. Otherwise
	// the CXO node generates a random keypair and remote peers see a
	// different PK than they expect from the feed metadata — which
	// breaks any subscriber-allowlist that uses the feed PK as the
	// gating identity (the per-pair-feed pattern relies on this).
	cfg.SecKey = skycipher.SecKey(sk)
	cfg.Config = skyobject.NewConfig()
	cfg.Config.InMemoryDB = conf.InMemoryDB
	if conf.DataDir != "" {
		cfg.Config.DataDir = conf.DataDir
	}
	// We're DMSG-only — disable the CXO node's default TCP/UDP/RPC
	// listeners. They default to :8870 / :8871 / "" respectively, and
	// hardcoded ports mean two Publishers in the same process collide
	// on bind. None of those listeners are reachable through DMSG
	// anyway.
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""

	// Build the allowState before NewNode so the OnSubscribeRemote
	// closure captures live state. Wired even when the allowlist is
	// disabled (nil set) so SetAllowlist can later raise the gate
	// without restarting the node.
	allow := newAllowState(conf.SubscriberAllowlist)
	cfg.OnSubscribeRemote = subscribeHook(allow)

	cxoNode, err := node.NewNode(cfg)
	if err != nil {
		return nil, err
	}
	port := conf.DmsgPort
	if port == 0 {
		port = cxotransport.DefaultCXOPort
	}
	factory := cxotransport.NewDMSGFactory(dmsgC, port)
	if err := cxoNode.EnableDMSG(factory); err != nil {
		_ = cxoNode.Close() //nolint:errcheck
		return nil, err
	}

	p, err := New(cxoNode, sk, Config{
		BatchWindow: conf.BatchWindow,
		Logger:      conf.Logger,
		sharedAllow: allow,
	})
	if err != nil {
		_ = cxoNode.Close() //nolint:errcheck
		return nil, err
	}
	p.ownsNode = true
	return p, nil
}

// New constructs a Publisher backed by the given CXO node. The feed
// PK is derived from sk — the node's own keypair, by convention.
// The publish loop starts immediately; call Close to stop it.
//
// If a previous run published Roots under this PK, New does NOT
// repopulate the in-memory tree from them. Callers that need to
// resume from on-disk state should re-Put the entries themselves.
// (For the visor stats use case, the bbolt store IS the source of
// truth — the publisher is regenerated from it on each startup.)
func New(cxoNode *node.Node, sk cipher.SecKey, conf Config) (*Publisher, error) {
	if conf.BatchWindow <= 0 {
		conf.BatchWindow = time.Second
	}
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("cxo-treestore")
	}

	pk, err := sk.PubKey()
	if err != nil {
		return nil, err
	}

	skyPK := skycipher.PubKey(pk)
	if !cxoNode.IsSharing(skyPK) {
		if err := cxoNode.Share(skyPK); err != nil {
			return nil, err
		}
	}

	allow := conf.sharedAllow
	if allow == nil {
		allow = newAllowState(conf.SubscriberAllowlist)
	}

	p := &Publisher{
		log:          conf.Logger,
		cxoNode:      cxoNode,
		pk:           pk,
		sk:           sk,
		root:         newMemNode(),
		allow:        allow,
		batchWindow:  conf.BatchWindow,
		wakeup:       make(chan struct{}, 1),
		done:         make(chan struct{}),
		cleanupNudge: make(chan struct{}, 1),
		cleanupDone:  make(chan struct{}),
	}
	go p.runLoop()
	go p.runCleanupLoop()
	return p, nil
}

// Feed returns the public key of the feed. Subscribers connect using
// this PK.
func (p *Publisher) Feed() cipher.PubKey {
	return p.pk
}

// Node returns the underlying CXO node. Exposed so callers can
// attach a Subscriber to the same node via NewSubscriberOnNode —
// the per-pair-feed pattern uses one node per pair side, listening
// on the deterministic pair port, hosting both the publisher's own
// feed and a subscriber to the peer's feed. Mutating the returned
// node directly is unsupported; treat it as read-only state.
func (p *Publisher) Node() *node.Node {
	return p.cxoNode
}

// SetAllowlist atomically replaces the subscriber allowlist. nil
// disables the gate (any subscriber accepted). An empty non-nil
// slice closes the gate to all subscribers — useful for staging a
// feed before authorizing peers.
//
// Existing subscribers keep their connections; the change applies
// to subsequent subscribe attempts.
func (p *Publisher) SetAllowlist(pks []cipher.PubKey) {
	p.allow.replace(pks)
}

// Allowlist returns a snapshot of the current allowed PKs. nil means
// the gate is disabled (open to all).
func (p *Publisher) Allowlist() []cipher.PubKey {
	return p.allow.list()
}

// AllowsSubscriber reports whether a subscribe request from pk would
// pass the gate. NewWithDMSG wires this into the underlying CXO
// node's OnSubscribeRemote hook automatically. Callers using New
// directly with their own CXO node should consult this method from
// their own hook implementation.
func (p *Publisher) AllowsSubscriber(pk cipher.PubKey) bool {
	return p.allow.permits(pk)
}

// AnnounceTo brings up (or refreshes) a CXO conn to peerPK over the
// publisher's underlying DMSG transport. This is a "say hello" call:
// it doesn't subscribe peerPK to anything, just establishes the
// conn so the peer's CXO node can see this publisher and subscribe
// to the publisher's feed in the reverse direction.
//
// Used by the visor-stats subsystem to dial the Transport Discovery
// once at boot and on each reconcile tick — TPD's CXO subscriber
// then sees the inbound conn and subscribes to the visor's feed
// (PK = visor PK = the publisher feed). Idempotent: returns nil for
// an existing live conn, redials if the previous conn has dropped.
func (p *Publisher) AnnounceTo(peerPK cipher.PubKey) error {
	if p.cxoNode == nil {
		return errors.New("treestore: publisher has no cxo node")
	}
	dmsgT := p.cxoNode.DMSG()
	if dmsgT == nil {
		return errors.New("treestore: publisher has no dmsg transport")
	}
	_, err := dmsgT.ConnectPK(peerPK)
	return err
}

// Put stores a value at the given path. The path is split on '/'
// into segments; intermediate sub-nodes are created as needed.
// Putting at a path currently held by a sub-tree (or vice versa)
// returns ErrPathConflict — the caller must Delete the existing
// entry first.
func (p *Publisher) Put(path string, value []byte) error {
	segs, err := SplitPath(path)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return errors.New("treestore: cannot Put at empty path (root)")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := putAt(p.root, segs, append([]byte(nil), value...)); err != nil {
		return err
	}
	p.markDirty()
	return nil
}

// Get reads the value at path. Returns (nil, false) if the path is
// absent or addresses a sub-tree rather than a leaf.
func (p *Publisher) Get(path string) ([]byte, bool) {
	segs, err := SplitPath(path)
	if err != nil || len(segs) == 0 {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := getAt(p.root, segs)
	if !ok {
		return nil, false
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true
}

// Delete removes a single leaf. If the path addresses a sub-tree, it
// is left unchanged and ErrPathIsSubtree is returned (use
// PrunePrefix to remove a sub-tree). Empty intermediate sub-nodes
// left behind by a delete are pruned automatically.
func (p *Publisher) Delete(path string) error {
	segs, err := SplitPath(path)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return errors.New("treestore: cannot Delete root")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	removed := deleteAt(p.root, segs)
	if removed {
		p.markDirty()
	}
	return nil
}

// PrunePrefix removes every leaf at-or-under prefix and the
// containing sub-tree. Used by rolling-window cleanup. The empty
// prefix clears the entire tree.
func (p *Publisher) PrunePrefix(prefix string) error {
	segs, err := SplitPath(prefix)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(segs) == 0 {
		p.root = newMemNode()
		p.markDirty()
		return nil
	}
	if pruneAt(p.root, segs) {
		p.markDirty()
	}
	return nil
}

// Walk invokes fn for every leaf at-or-under prefix. Visitation
// order is sorted by full path. Returning false from fn aborts
// iteration. The visitor receives copies of the value bytes — it's
// safe to retain them across calls.
func (p *Publisher) Walk(prefix string, fn func(path string, value []byte) bool) {
	segs, err := SplitPath(prefix)
	if err != nil {
		return
	}
	canonical := joinSegs(segs) // strip any trailing "/" the caller passed
	p.mu.Lock()
	defer p.mu.Unlock()

	startNode := p.root
	for _, seg := range segs {
		next, ok := startNode.subs[seg]
		if !ok {
			return
		}
		startNode = next
	}
	walkLeaves(startNode, canonical, fn)
}

// Flush blocks until pending changes have been published, or
// returns nil immediately if there are none. Useful in tests where
// the caller wants deterministic post-Put state.
func (p *Publisher) Flush() error {
	for {
		p.mu.Lock()
		dirty := p.dirty
		p.mu.Unlock()
		if !dirty {
			return nil
		}
		// Trigger a wakeup and yield. The publish loop will clear
		// dirty if Save succeeds.
		select {
		case p.wakeup <- struct{}{}:
		default:
		}
		time.Sleep(time.Millisecond)
	}
}

// Close stops the publish loop. Pending changes are flushed before
// returning. If the publisher owns its CXO node (NewWithDMSG path),
// the node is also closed. Idempotent.
func (p *Publisher) Close() error {
	var firstErr error
	p.stopOnce.Do(func() {
		// Best-effort flush before signaling stop.
		firstErr = p.Flush()
		close(p.done)
		// Wait for the cleanup goroutine to drain its final sweep.
		// Bounded since RemoveObjects is O(CXDS size), but for
		// in-memory CXDS this completes in milliseconds.
		<-p.cleanupDone
		if p.ownsNode && p.cxoNode != nil {
			if err := p.cxoNode.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

// runCleanupLoop drops superseded Roots and frees CXDS entries whose
// reference count fell to zero. CXO never auto-deletes — every
// publish leaves the previous Root and its tree at rc≥1, and the
// changed leaves from the prior tree become orphaned (rc=0 but
// kept in CXDS). Without this sweep, in-process memory grows by
// roughly one published payload per publish (60s tick × ~5 MB/publish
// = ~5 GB / day for the TPD metrics+uptime publishers — exactly the
// leak that hit production with 96.8% of TPD's heap living under
// treestore.Publisher.publishIfDirty / encoder.Serialize).
//
// The loop runs OUTSIDE p.mu so it doesn't extend Flush's polling
// window, and at most once per nudge (publishRoot fires a
// non-blocking send into cleanupNudge after each successful Save).
// Errors are logged and discarded — a missed sweep is recovered on
// the next nudge. RemoveRootObjects (keepLast=1) decrements rc
// throughout the previous Root's tree; RemoveObjects then deletes
// every CXDS entry whose rc reached zero AND isn't currently in
// the LRU cache.
func (p *Publisher) runCleanupLoop() {
	defer close(p.cleanupDone)
	for {
		select {
		case <-p.done:
			// One last sweep to catch the final publish.
			p.runCleanup()
			return
		case <-p.cleanupNudge:
			p.runCleanup()
		}
	}
}

func (p *Publisher) runCleanup() {
	c := p.cxoNode.Container()
	if err := cxoutils.RemoveRootObjects(c, 1); err != nil {
		p.log.WithError(err).Debug("treestore: RemoveRootObjects failed; will retry next publish")
		return
	}
	if err := cxoutils.RemoveObjects(c); err != nil {
		p.log.WithError(err).Debug("treestore: RemoveObjects failed; will retry next publish")
	}
}

// markDirty notes that the in-memory tree has unpublished changes
// and nudges the publish loop. Must be called with p.mu held.
func (p *Publisher) markDirty() {
	p.dirty = true
	select {
	case p.wakeup <- struct{}{}:
	default:
	}
}

// runLoop is the dedicated publish goroutine. It coalesces
// mutations within batchWindow and writes one Root per batch.
func (p *Publisher) runLoop() {
	for {
		select {
		case <-p.done:
			// Final publish attempt to flush any in-flight dirty
			// state (Close already flushed but be defensive).
			_ = p.publishIfDirty() //nolint:errcheck
			return
		case <-p.wakeup:
			// Coalesce: wait the batch window, then publish.
			timer := time.NewTimer(p.batchWindow)
			select {
			case <-timer.C:
			case <-p.done:
				timer.Stop()
				_ = p.publishIfDirty() //nolint:errcheck
				return
			}
			if err := p.publishIfDirty(); err != nil {
				p.log.WithError(err).Warn("treestore: publish failed")
			}
		}
	}
}

func (p *Publisher) publishIfDirty() error {
	// The publish path walks the in-memory tree (encodeNode) and
	// must do so under p.mu, since concurrent Put / Delete /
	// PrunePrefix mutate the same memNode maps. The CXO encode/Save
	// work is in-memory and bounded; for the visor stats workload
	// (1-min sample cadence, hundreds of entries max) the
	// lock-hold time is well under a millisecond.
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dirty {
		return nil
	}
	if err := p.publishRoot(p.root); err != nil {
		return err
	}
	p.dirty = false
	return nil
}

// publishRoot encodes the in-memory tree as CXO objects and saves
// the resulting Root under the feed's PK.
func (p *Publisher) publishRoot(root *memNode) error {
	c := p.cxoNode.Container()

	skyfromCipher := skycipher.SecKey(p.sk)

	up, err := c.Unpack(skyfromCipher, Registry)
	if err != nil {
		return err
	}
	defer up.Close() //nolint:errcheck

	// Build the root TreeNode bottom-up. encodeNode writes leaf
	// objects and TreeEntries via up.Add, returning an unsaved
	// TreeNode value that we then wrap in a Dynamic for Root.Refs.
	// freshSubs collects every sub-node we just freshly encoded so we
	// can promote them into the per-memNode cache after Save succeeds
	// — never before, otherwise an aborted publish would leave the
	// cache pointing at hashes that up.Close has rolled back.
	var freshSubs []freshSub
	rootNode, err := encodeNode(up, root, &freshSubs)
	if err != nil {
		return err
	}

	rootSchema, err := Registry.SchemaByName("cxo.treestore.TreeNode")
	if err != nil {
		return err
	}

	var rootDyn registry.Dynamic
	if err := rootDyn.SetValue(up, &rootNode); err != nil {
		return err
	}
	rootDyn.Schema = rootSchema.Reference()

	nonce := c.ActiveHead(skycipher.PubKey(p.pk))
	if nonce == 0 {
		nonce = 1 // CXO requires a non-zero nonce
	}

	r := &registry.Root{
		Pub:   skycipher.PubKey(p.pk),
		Nonce: nonce,
		Reg:   Registry.Reference(),
		Refs:  []registry.Dynamic{rootDyn},
	}

	if err := c.Save(up, r); err != nil {
		return err
	}
	p.cxoNode.Publish(r)
	for _, fs := range freshSubs {
		fs.n.pubHash = fs.hash
		fs.n.cached = true
	}

	// Nudge the cleanup goroutine. Non-blocking: if cleanup is already
	// running or pending, we don't need to enqueue another. The cleanup
	// runs outside the publisher mutex so it doesn't extend Flush's
	// polling window.
	select {
	case p.cleanupNudge <- struct{}{}:
	default:
	}
	return nil
}

// freshSub records a sub-node whose TreeNode was freshly serialized
// during the current publishRoot call. After Save succeeds the
// publisher promotes these into the per-memNode encode-cache so the
// next publish can skip both the recursive walk and the
// encoder.Serialize / Cache.Set chain for any subtree that has not
// been mutated since.
type freshSub struct {
	n    *memNode
	hash skycipher.SHA256
}

// encodeNode walks a memNode and produces a TreeNode whose Children
// Refs contains TreeEntries for every leaf and sub-node, sorted by
// name for deterministic encoding.
//
// For each sub-node it consults memNode.cached: if the sub-node has
// not been mutated since its last successful publish, encodeNode reuses
// the cached pubHash directly in the parent's TreeEntry without
// recursing or re-serializing. Container.Save's reference walk then
// increments the rc on the cached hash without descending into it
// (skyobject/unpack.go:156-167), so the underlying CXO objects stay
// alive across publishes as long as each successive Root references
// the same hash. Mutated sub-trees are re-encoded via the slow path
// and recorded in freshSubs so the caller can promote them after
// Save returns nil.
func encodeNode(up registry.Pack, n *memNode, freshSubs *[]freshSub) (TreeNode, error) {
	var node TreeNode

	names := sortedNames(n)
	if len(names) == 0 {
		return node, nil
	}

	entries := make([]interface{}, 0, len(names))
	for _, name := range names {
		entry := TreeEntry{Name: name}
		if leaf, ok := n.leaves[name]; ok {
			entry.Leaf = append([]byte(nil), leaf...)
		} else if sub, ok := n.subs[name]; ok {
			if sub.cached {
				// Fast path: subtree unchanged since last publish.
				entry.Sub = registry.Ref{Hash: sub.pubHash}
			} else {
				subNode, err := encodeNode(up, sub, freshSubs)
				if err != nil {
					return TreeNode{}, err
				}
				if err := entry.Sub.SetValue(up, &subNode); err != nil {
					return TreeNode{}, err
				}
				*freshSubs = append(*freshSubs, freshSub{n: sub, hash: entry.Sub.Hash})
			}
		} else {
			// Should not happen — memNode invariants keep names in
			// either leaves or subs but not neither.
			continue
		}
		entries = append(entries, entry)
	}

	if err := node.Children.AppendValues(up, entries...); err != nil {
		return TreeNode{}, err
	}
	return node, nil
}

// sortedNames returns the union of leaf and sub-node names at this
// level, sorted ascending.
func sortedNames(n *memNode) []string {
	names := make([]string, 0, len(n.leaves)+len(n.subs))
	for name := range n.leaves {
		names = append(names, name)
	}
	for name := range n.subs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// putAt walks segs into root, creating sub-nodes as needed, and
// installs the leaf value at the final segment. Returns
// ErrPathConflict if the final segment is held by a sub-tree, or if
// any intermediate segment is held by a leaf.
//
// On a successful put, the encode-cache (memNode.cached) is cleared on
// every node along the touched path so the next publish re-encodes
// only the changed branch.
func putAt(root *memNode, segs []string, value []byte) error {
	cur := root
	chain := make([]*memNode, 0, len(segs))
	chain = append(chain, cur)
	mutated := false
	defer func() {
		if mutated {
			invalidatePath(chain)
		}
	}()
	for i, seg := range segs[:len(segs)-1] {
		if _, isLeaf := cur.leaves[seg]; isLeaf {
			return &PathConflictError{Path: joinSegs(segs[:i+1]), Existing: "leaf"}
		}
		next, ok := cur.subs[seg]
		if !ok {
			next = newMemNode()
			cur.subs[seg] = next
			// Creating a child structurally mutates the chain even if
			// a deeper conflict aborts the put — invalidate.
			mutated = true
		}
		cur = next
		chain = append(chain, cur)
	}
	last := segs[len(segs)-1]
	if _, isSub := cur.subs[last]; isSub {
		return &PathConflictError{Path: joinSegs(segs), Existing: "sub-tree"}
	}
	cur.leaves[last] = value
	mutated = true
	return nil
}

// getAt returns the leaf bytes at the given path, or (nil, false) if
// the path is absent or addresses a sub-tree.
func getAt(root *memNode, segs []string) ([]byte, bool) {
	cur := root
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur.subs[seg]
		if !ok {
			return nil, false
		}
		cur = next
	}
	v, ok := cur.leaves[segs[len(segs)-1]]
	return v, ok
}

// deleteAt removes the leaf at the given path. Empty parent
// sub-nodes left behind are pruned. Returns true if anything was
// removed.
func deleteAt(root *memNode, segs []string) bool {
	// Walk to the parent, recording the chain so we can prune empty
	// intermediates on the way back.
	chain := make([]*memNode, 0, len(segs))
	cur := root
	chain = append(chain, cur)
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur.subs[seg]
		if !ok {
			return false
		}
		cur = next
		chain = append(chain, cur)
	}
	last := segs[len(segs)-1]
	if _, ok := cur.leaves[last]; !ok {
		return false
	}
	delete(cur.leaves, last)
	// Prune empty intermediates upward (skip root at index 0).
	for i := len(chain) - 1; i >= 1; i-- {
		n := chain[i]
		if len(n.leaves) > 0 || len(n.subs) > 0 {
			break
		}
		parent := chain[i-1]
		delete(parent.subs, segs[i-1])
	}
	invalidatePath(chain)
	return true
}

// pruneAt removes the entire sub-tree (and any leaf at the same
// position) addressed by segs. Returns true if anything was removed.
func pruneAt(root *memNode, segs []string) bool {
	chain := make([]*memNode, 0, len(segs))
	parent := root
	chain = append(chain, parent)
	for _, seg := range segs[:len(segs)-1] {
		next, ok := parent.subs[seg]
		if !ok {
			return false
		}
		parent = next
		chain = append(chain, parent)
	}
	last := segs[len(segs)-1]
	_, hadLeaf := parent.leaves[last]
	_, hadSub := parent.subs[last]
	if !hadLeaf && !hadSub {
		return false
	}
	delete(parent.leaves, last)
	delete(parent.subs, last)
	invalidatePath(chain)
	return true
}

// invalidatePath clears the encode-cache flag on every node in chain.
// Called by the mutators after any successful structural change so the
// next publish re-encodes the affected nodes (and re-uses cached
// hashes on every untouched sibling subtree).
func invalidatePath(chain []*memNode) {
	for _, n := range chain {
		n.cached = false
	}
}

// walkLeaves visits every leaf at-or-under the given node, prefixing
// reported paths with the supplied path string.
func walkLeaves(n *memNode, path string, fn func(string, []byte) bool) bool {
	for _, name := range sortedNames(n) {
		if leaf, ok := n.leaves[name]; ok {
			full := name
			if path != "" {
				full = path + "/" + name
			}
			cp := make([]byte, len(leaf))
			copy(cp, leaf)
			if !fn(full, cp) {
				return false
			}
			continue
		}
		if sub, ok := n.subs[name]; ok {
			full := name
			if path != "" {
				full = path + "/" + name
			}
			if !walkLeaves(sub, full, fn) {
				return false
			}
		}
	}
	return true
}

// joinSegs is a private helper that joins segs without revalidating
// (the caller already validated via SplitPath upstream).
func joinSegs(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	out := segs[0]
	for _, s := range segs[1:] {
		out += "/" + s
	}
	return out
}

// PathConflictError is returned when a Put or Delete cannot proceed
// because the position is held by an entry of the wrong kind.
type PathConflictError struct {
	Path     string
	Existing string // "leaf" or "sub-tree"
}

func (e *PathConflictError) Error() string {
	return "treestore: path " + e.Path + " conflicts with existing " + e.Existing
}
