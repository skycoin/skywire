// Package treestore — pkg/cxo/treestore/subscriber.go: hierarchical
// CXO subscriber.
//
// The subscriber maintains a private CXO Node, dials the publisher
// over DMSG, and subscribes to the publisher's feed PK. CXO's Filler
// resolves all referenced objects on each Root update; OnRootFilled
// then walks the tree, decodes leaves, and updates the local
// path→bytes cache. Callers register a callback to learn about
// changes; Get and Walk read the cache directly.
//
// Today the subscriber receives the entire feed regardless of
// declared prefixes (the prefix list is honored only at the local
// API surface — callers Walk-ing or Get-ing under a prefix only see
// matching entries, but unwanted subtrees are still pulled over the
// wire). Filler-level prefix subscription is a follow-up CXO core
// change tracked separately.
package treestore

import (
	"sync"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// UpdateEvent describes a single leaf change observed by the
// subscriber. Value is nil when the leaf was deleted (path missing
// in the new Root that was present in the previous one).
type UpdateEvent struct {
	Path  string
	Value []byte // nil = deleted
}

// UpdateCallback fires once per Root that finishes filling, after
// the local cache has been updated. Receives the slice of changes
// (insertions, modifications, deletions) since the previous Root.
// Implementations should be non-blocking; they're called from the
// CXO node's filler goroutine.
type UpdateCallback func(changes []UpdateEvent)

// Subscriber connects to a TreeStore feed and exposes a path→value
// cache that mirrors the feed contents.
type Subscriber struct {
	log *logging.Logger

	cxoNode  *node.Node
	ownsNode bool // when true, Close releases the node; false = caller owns
	feedPK   cipher.PubKey

	mu       sync.RWMutex
	cache    map[string][]byte
	prefixes []string // declared filter; empty = match all
	callback UpdateCallback

	closed   bool
	closeMu  sync.Mutex
	closeErr error
}

// SubConfig configures a Subscriber. All zero values are reasonable.
type SubConfig struct {
	Logger     *logging.Logger
	InMemoryDB bool
	DataDir    string

	// DmsgPort overrides both the listen port (where this subscriber's
	// CXO node accepts inbound connections, typically unused for
	// subscribe-only flows) and the dial port (where Connect dials
	// the publisher). Zero falls back to cxotransport.DefaultCXOPort,
	// matching the system telemetry feed.
	//
	// Per-pair-feed callers set this to the pair's deterministic
	// publisher port so the subscriber dials the right place; multiple
	// subscribers on one visor must use distinct DmsgPort values to
	// avoid a Listen collision.
	DmsgPort uint16
}

// NewSubscriber creates a Subscriber that will receive updates from
// feedPK once Connect is called. The returned Subscriber owns its
// own CXO Node; Close releases it.
func NewSubscriber(dmsgC *dmsg.Client, feedPK cipher.PubKey, conf SubConfig) (*Subscriber, error) {
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("cxo-treestore-sub")
	}

	cfg := node.NewConfig()
	cfg.Config = skyobject.NewConfig()
	cfg.Config.InMemoryDB = conf.InMemoryDB
	if conf.DataDir != "" {
		cfg.Config.DataDir = conf.DataDir
	}
	// We're DMSG-only — disable the CXO node's default TCP/UDP/RPC
	// listeners (mirrors NewWithDMSG). Hardcoded ports otherwise
	// mean two Subscribers in the same process collide on bind, which
	// is the common case for per-pair-feed callers.
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""

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

	s := &Subscriber{
		log:      conf.Logger,
		cxoNode:  cxoNode,
		ownsNode: true,
		feedPK:   feedPK,
		cache:    make(map[string][]byte),
	}

	cxoNode.Config().OnRootFilled = func(_ *node.Node, r *registry.Root) {
		s.handleRootFilled(r)
	}
	return s, nil
}

// NewSubscriberOnNode attaches a Subscriber to an existing CXO node.
// Caller retains ownership of the node — the Subscriber's Close does
// NOT release it.
//
// Use case: per-pair feeds where one CXO node hosts both a publisher
// (owning its own feed) AND a subscriber (to the peer's feed). Two
// separate nodes don't work there because both would Listen on the
// same deterministic pair port and collide.
//
// The OnRootFilled callback is set on the node's config — the existing
// callback is preserved if the new subscription's feed PK doesn't
// match the incoming Root, so multiple subscribers can coexist on the
// same node by chaining handlers. Today only single-subscriber-per-node
// is exercised; chaining is a future extension.
func NewSubscriberOnNode(cxoNode *node.Node, feedPK cipher.PubKey, conf SubConfig) (*Subscriber, error) {
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("cxo-treestore-sub")
	}
	s := &Subscriber{
		log:      conf.Logger,
		cxoNode:  cxoNode,
		ownsNode: false,
		feedPK:   feedPK,
		cache:    make(map[string][]byte),
	}
	prev := cxoNode.Config().OnRootFilled
	cxoNode.Config().OnRootFilled = func(n *node.Node, r *registry.Root) {
		s.handleRootFilled(r)
		if prev != nil {
			prev(n, r)
		}
	}
	return s, nil
}

// Connect dials the publisher over DMSG and subscribes to the feed
// PK provided at construction. Updates begin flowing on the next
// publish.
func (s *Subscriber) Connect(publisherPK cipher.PubKey) error {
	dmsgT := s.cxoNode.DMSG()
	if dmsgT == nil {
		return node.ErrAlreadyListen
	}

	conn, err := dmsgT.ConnectPK(publisherPK)
	if err != nil {
		return err
	}
	return conn.Subscribe(skycipher.PubKey(s.feedPK))
}

// SetPrefixes restricts which paths surface via Get / Walk / the
// OnUpdate callback. An empty slice (or nil) means "no filter".
//
// Filtering is currently subscriber-local — unwanted subtrees still
// arrive over the wire. The API surface is in place so callers can
// declare intent today and benefit transparently when Filler-level
// pruning ships.
func (s *Subscriber) SetPrefixes(prefixes []string) {
	cleaned := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		segs, err := SplitPath(p)
		if err != nil {
			continue
		}
		cleaned = append(cleaned, joinSegs(segs))
	}
	s.mu.Lock()
	s.prefixes = cleaned
	s.mu.Unlock()
}

// Get returns the cached value for path. Returns (nil, false) if
// the path is not present in the cache, or if a prefix filter is
// active and path doesn't match any declared prefix.
func (s *Subscriber) Get(path string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.matchesPrefixLocked(path) {
		return nil, false
	}
	v, ok := s.cache[path]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true
}

// Walk invokes fn for every cached leaf at-or-under prefix. If a
// declared prefix filter is active, only paths that match BOTH the
// declared filter and the supplied walk prefix are visited.
// Visitation order is unspecified (cache map iteration); callers
// that need sorted output should accumulate and sort.
func (s *Subscriber) Walk(prefix string, fn func(path string, value []byte) bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for path, value := range s.cache {
		if !HasPrefix(path, prefix) {
			continue
		}
		if !s.matchesPrefixLocked(path) {
			continue
		}
		cp := make([]byte, len(value))
		copy(cp, value)
		if !fn(path, cp) {
			return
		}
	}
}

// OnUpdate registers (or replaces) the change callback. Pass nil to
// clear. The callback is invoked under the subscriber's lock — keep
// it short or copy state out before blocking.
func (s *Subscriber) OnUpdate(cb UpdateCallback) {
	s.mu.Lock()
	s.callback = cb
	s.mu.Unlock()
}

// Close stops the subscriber. When the subscriber owns its CXO node
// (the NewSubscriber path), the node is also released; in
// NewSubscriberOnNode mode the caller retains ownership.
// Idempotent.
func (s *Subscriber) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return s.closeErr
	}
	s.closed = true
	if s.ownsNode {
		s.closeErr = s.cxoNode.Close()
	}
	return s.closeErr
}

// matchesPrefixLocked reports whether path matches any of the
// declared prefixes. Caller must hold s.mu (read or write).
func (s *Subscriber) matchesPrefixLocked(path string) bool {
	if len(s.prefixes) == 0 {
		return true
	}
	for _, p := range s.prefixes {
		if HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// handleRootFilled is the OnRootFilled callback. Walks the tree
// rooted at r, builds the new path→value snapshot, diffs against
// the previous cache, and dispatches changes.
func (s *Subscriber) handleRootFilled(r *registry.Root) {
	if r == nil || len(r.Refs) == 0 {
		// Root with no payload — could happen on startup before
		// the publisher has put anything. Treat as empty.
		s.applySnapshot(nil)
		return
	}

	c := s.cxoNode.Container()
	pack, err := c.Pack(r, Registry)
	if err != nil {
		s.log.WithError(err).Warn("treestore-sub: get pack failed")
		return
	}

	var rootNode TreeNode
	if err := r.Refs[0].Value(pack, &rootNode); err != nil {
		s.log.WithError(err).Warn("treestore-sub: decode root TreeNode failed")
		return
	}

	snap := make(map[string][]byte)
	if err := walkTree(pack, &rootNode, "", snap); err != nil {
		s.log.WithError(err).Warn("treestore-sub: walk tree failed")
		return
	}

	s.applySnapshot(snap)
}

// applySnapshot replaces the cache with snap and fires the change
// callback for inserted / modified / deleted leaves relative to the
// previous cache contents.
func (s *Subscriber) applySnapshot(snap map[string][]byte) {
	s.mu.Lock()
	prev := s.cache
	s.cache = snap
	cb := s.callback
	prefixes := s.prefixes
	s.mu.Unlock()

	if cb == nil {
		return
	}

	var changes []UpdateEvent
	for path, newVal := range snap {
		if !pathMatchesAny(path, prefixes) {
			continue
		}
		if oldVal, had := prev[path]; !had || !bytesEqual(oldVal, newVal) {
			changes = append(changes, UpdateEvent{Path: path, Value: append([]byte(nil), newVal...)})
		}
	}
	for path := range prev {
		if _, stillThere := snap[path]; stillThere {
			continue
		}
		if !pathMatchesAny(path, prefixes) {
			continue
		}
		changes = append(changes, UpdateEvent{Path: path, Value: nil})
	}
	if len(changes) > 0 {
		cb(changes)
	}
}

// walkTree recursively decodes TreeNode → TreeEntries → leaves,
// populating out with full-path → value bytes.
func walkTree(pack registry.Pack, node *TreeNode, basePath string, out map[string][]byte) error {
	n, err := node.Children.Len(pack)
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		var entry TreeEntry
		if _, err := node.Children.ValueByIndex(pack, i, &entry); err != nil {
			return err
		}
		fullPath := entry.Name
		if basePath != "" {
			fullPath = basePath + "/" + entry.Name
		}
		if len(entry.Leaf) > 0 {
			out[fullPath] = append([]byte(nil), entry.Leaf...)
			continue
		}
		// Sub-tree: decode and recurse.
		if entry.Sub.Hash == (skycipher.SHA256{}) {
			continue
		}
		var sub TreeNode
		if err := entry.Sub.Value(pack, &sub); err != nil {
			return err
		}
		if err := walkTree(pack, &sub, fullPath, out); err != nil {
			return err
		}
	}
	return nil
}

// pathMatchesAny is a non-locking variant for use after the prefix
// list has been copied out from under s.mu.
func pathMatchesAny(path string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
