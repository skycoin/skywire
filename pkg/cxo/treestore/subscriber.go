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
	"context"
	"sync"
	"sync/atomic"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// reconnectQuietThreshold is how long the subscriber must see no
// Root activity before the watchdog assumes the connection is dead
// and re-Connects. Issue #2713: a peer's visor restart drops the
// existing dmsg session; the subscriber doesn't observe the close
// (the dmsg layer silently fails to surface it up to treestore) so
// the subscription stays "live" but nothing flows. 60s is well above
// any reasonable pair-batch publish cadence (today ~60s ticker on
// active feeds) but short enough that a peer-restart-stalled
// subscriber heals within ~90s end-to-end.
const reconnectQuietThreshold = 60 * time.Second

// reconnectWatchdogTick is the watchdog's poll interval. Half the
// quiet threshold so a stall is caught within at most threshold +
// tick. Cheap — one map read + one timestamp comparison per tick.
const reconnectWatchdogTick = 30 * time.Second

// reconnectConnectTimeout bounds each watchdog-triggered Connect
// attempt. Matches the deadline pairing.Manager.PairAdd uses for
// the initial Connect; we don't want a hung dial to wedge the
// watchdog goroutine.
const reconnectConnectTimeout = 15 * time.Second

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

	// rootObservedMu guards rootObservedSignal so callers waiting on
	// a Root observation see a consistent channel even when a fresh
	// Connect resets the signal. Without the lock, a reconnect that
	// swaps the channel while handleRootFilled is mid-close would
	// race with a concurrent waiter reading the old reference.
	rootObservedMu sync.Mutex
	// rootObservedSignal closes once when a Root for this subscriber's
	// feedPK is filled. ConnectAndWaitForRoot resets it (creates a
	// fresh channel) before each Connect attempt so each connect/
	// reconnect cycle waits for its own Root observation, not stale
	// success from an earlier attempt.
	//
	// Pre-fix: Subscriber.Connect returned as soon as the underlying
	// Subscribe call queued the frame, with no guarantee a Root would
	// follow. Callers (peerSubs reconnect loop in skychat-group) saw
	// "success" + bumped their peer_last_inbound timestamp on a
	// half-attached state where the publisher never sent or the fill
	// walk never completed. ConnectAndWaitForRoot closes this
	// observation gap by waiting for the actual fill before
	// returning success — and resets per-attempt so an earlier Root
	// observation doesn't fast-path a fresh reconnect that hasn't
	// re-verified the channel.
	rootObservedSignal chan struct{}

	// lastUpdateNs is the unix-nano timestamp of the most recent
	// successful handleRootFilled call (i.e. the last time data
	// flowed end-to-end from publisher → subscriber). The reconnect
	// watchdog reads this; handleRootFilled bumps it. atomic.Int64
	// because reads happen on the watchdog goroutine while writes
	// happen on the CXO filler goroutine.
	lastUpdateNs atomic.Int64

	// publisherPK stores the most recent Connect target PK so the
	// watchdog has somewhere to re-Connect to. nil until first
	// Connect succeeds.
	publisherPK atomic.Pointer[cipher.PubKey]

	// reconnectStop, when closed, terminates the watchdog. Created
	// when Connect is first called; closed by Close. Idempotent
	// close via reconnectOnce.
	reconnectStop chan struct{}
	reconnectOnce sync.Once
	// reconnectStartOnce guards starting the watchdog: we only spawn
	// the goroutine on the first successful Connect, not every
	// subsequent reconnect.
	reconnectStartOnce sync.Once
	// reconnectAttempts counts watchdog-triggered Connect attempts
	// (incremented BEFORE the Connect call regardless of outcome).
	// Surfaced only for tests; treat as opaque metric otherwise.
	reconnectAttempts atomic.Int64

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
		log:                conf.Logger,
		cxoNode:            cxoNode,
		ownsNode:           true,
		feedPK:             feedPK,
		cache:              make(map[string][]byte),
		rootObservedSignal: make(chan struct{}),
	}

	// Use Swap helpers (synchronized) instead of direct Config()
	// mutation. The single-subscriber-per-node case still races with
	// the head goroutine's onRootFilled reader without sync — exposed
	// by treestore TestPeerSubMissesAfterReattach.
	cxoNode.SwapOnRootFilled(func(_ *node.Node, r *registry.Root) {
		s.handleRootFilled(r)
	})
	cxoNode.SwapOnFillingBreaks(func(_ *node.Node, r *registry.Root, err error) {
		s.handleFillingBreaks(r, err)
	})
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
		log:                conf.Logger,
		cxoNode:            cxoNode,
		ownsNode:           false,
		feedPK:             feedPK,
		cache:              make(map[string][]byte),
		rootObservedSignal: make(chan struct{}),
	}
	// SwapOnRootFilled / SwapOnFillingBreaks are atomic under
	// callbackMu, so a re-attach (Close then NewSubscriberOnNode)
	// doesn't race with the head goroutine's read at onRootFilled.
	// Pre-fix: TestPeerSubMissesAfterReattach surfaced the race
	// (treestore/peersub_reattach_test.go:337 re-attach call vs
	// node.go onRootFilled at the same instant).
	var prev func(*node.Node, *registry.Root)
	prev = cxoNode.SwapOnRootFilled(func(n *node.Node, r *registry.Root) {
		s.handleRootFilled(r)
		if prev != nil {
			prev(n, r)
		}
	})
	var prevFB func(*node.Node, *registry.Root, error)
	prevFB = cxoNode.SwapOnFillingBreaks(func(n *node.Node, r *registry.Root, err error) {
		s.handleFillingBreaks(r, err)
		if prevFB != nil {
			prevFB(n, r, err)
		}
	})
	return s, nil
}

// Connect dials the publisher over DMSG and subscribes to the feed
// PK provided at construction. Updates begin flowing on the next
// publish.
//
// ctx bounds the dial — when ctx is canceled or its deadline elapses
// before the underlying DMSG handshake completes, Connect returns
// ctx.Err() promptly instead of blocking on a half-dead transport.
// Cancellation is plumbed all the way to dmsg.Client.Dial via the
// (*DMSG).ConnectPK → (*DMSGFactory).Connect chain.
func (s *Subscriber) Connect(ctx context.Context, publisherPK cipher.PubKey) error {
	dmsgT := s.cxoNode.DMSG()
	if dmsgT == nil {
		return node.ErrAlreadyListen
	}

	conn, err := dmsgT.ConnectPK(ctx, publisherPK)
	if err != nil {
		return err
	}
	if err := conn.Subscribe(skycipher.PubKey(s.feedPK)); err != nil {
		return err
	}

	// Remember the publisher for the watchdog. Stored as a copy so
	// the watchdog goroutine doesn't alias a caller-owned variable.
	pk := publisherPK
	s.publisherPK.Store(&pk)
	// Reset the activity clock — a fresh subscribe should not count
	// as "stale" for the watchdog. handleRootFilled will bump it on
	// real updates.
	s.lastUpdateNs.Store(time.Now().UnixNano())
	// Start the watchdog on the FIRST successful Connect only.
	s.reconnectStartOnce.Do(func() {
		s.reconnectStop = make(chan struct{})
		go s.runReconnectWatchdog()
	})
	return nil
}

// runReconnectWatchdog polls for stalled subscriptions and re-Connects
// when the local subscriber has gone quiet for longer than the
// threshold. Fix for issue #2713: when a peer restarts, the dmsg
// session backing this subscription silently dies — the subscriber
// keeps thinking it's attached but no Root push arrives. The watchdog
// detects the silence and forces a fresh Connect to the peer's new
// publisher.
//
// Lifecycle: started on first successful Connect, stopped by Close.
// Safe to run on naturally-quiet feeds — Connect is idempotent
// (Subscribe-frame is harmless on a healthy connection), so a
// wasted reconnect on a feed that's just sleeping costs one dmsg
// dial + one Subscribe frame, no data loss.
func (s *Subscriber) runReconnectWatchdog() {
	s.runReconnectWatchdogWith(reconnectWatchdogTick, reconnectQuietThreshold)
}

// runReconnectWatchdogWith is the tickable inner loop used by both
// the production watchdog and tests that need to drive the loop with
// small intervals.
func (s *Subscriber) runReconnectWatchdogWith(tickEvery, quietThreshold time.Duration) {
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	for {
		select {
		case <-s.reconnectStop:
			return
		case <-tick.C:
		}
		// Snapshot last activity; if zero (no Root ever seen) we
		// skip the reconnect — the initial subscription may still
		// be settling.
		last := s.lastUpdateNs.Load()
		if last == 0 {
			continue
		}
		quiet := time.Since(time.Unix(0, last))
		if quiet < quietThreshold {
			continue
		}
		pkPtr := s.publisherPK.Load()
		if pkPtr == nil {
			continue
		}
		// Bounded Connect so a hung dial doesn't wedge the watchdog
		// past the next tick. ConnectAndWaitForRoot would be more
		// thorough (it confirms the new subscription actually
		// delivers a Root) but here we want a non-blocking refresh —
		// the next tick will catch a still-quiet feed.
		s.reconnectAttempts.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), reconnectConnectTimeout)
		err := s.Connect(ctx, *pkPtr)
		cancel()
		if err != nil {
			if s.log != nil {
				s.log.WithError(err).WithField("publisher_pk", pkPtr.Hex()[:8]).
					WithField("quiet_seconds", quiet.Seconds()).
					Debug("treestore: reconnect-watchdog Connect failed; will retry next tick")
			}
			continue
		}
		if s.log != nil {
			s.log.WithField("publisher_pk", pkPtr.Hex()[:8]).
				WithField("quiet_seconds", quiet.Seconds()).
				Info("treestore: reconnect-watchdog re-Connected after quiet period")
		}
	}
}

// ConnectAndWaitForRoot is the atomic-subscribe variant of Connect:
// it dials, sends the subscribe frame, AND waits until the first Root
// for this subscriber's feedPK is filled (success) or ctx expires
// (failure). Returns nil only when a Root has actually been received
// and walked — the subscription is verifiably live at that point, not
// just "subscribe frame queued."
//
// Why this exists: plain Connect returns as soon as the underlying
// Subscribe call queues a frame. There's no guarantee the publisher
// ever responds, that the response reaches a healthy Conn, or that
// the fill walk completes. Callers that need a verified-live
// subscription (notably the per-peer reconnect loop in skychat-group's
// session.go, where a Connect-success bumps peer_last_inbound and is
// then assumed to mean "data is flowing") get half-attached zombies
// otherwise — the symptom seen across 2026-05-16/17 reliability runs
// where peerSub.Connect returned success but no events flowed for
// minutes after, until something kicked the state machine.
//
// Cancellation: if ctx expires before the first Root fills, the
// subscription frame may already be in-flight (we can't unsend it),
// but Connect-side bookkeeping won't have observed a successful
// re-attach. The caller should treat ctx-err returns as failure and
// retry via the standard reconnect backoff. If the late Root arrives
// after ctx-err return, it still fires handleRootFilled normally —
// it's only the synchronous-wait side that gave up.
func (s *Subscriber) ConnectAndWaitForRoot(ctx context.Context, publisherPK cipher.PubKey) error {
	// Reset the signal for THIS attempt before subscribing. Each
	// reconnect cycle expects a fresh Root push (post-#2647 the
	// publisher pushes a Root on every Subscribe); the previous
	// attempt's signal may have already closed, which would let
	// this attempt fast-path success without verifying the new
	// subscribe handshake actually completed.
	s.rootObservedMu.Lock()
	s.rootObservedSignal = make(chan struct{})
	signal := s.rootObservedSignal
	s.rootObservedMu.Unlock()

	if err := s.Connect(ctx, publisherPK); err != nil {
		return err
	}
	select {
	case <-signal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

// CacheSizeAndSampleKeys returns (totalCacheEntries, firstMaxSampleKeys).
// Operator-visibility helper: a non-zero cache total combined with a
// zero post-Walk snapshot means either the manager's prefix filter
// dropped everything (subscriber declared a too-narrow prefix), or
// the snapshot copy on the manager side has a bug. A zero cache
// total means the publisher's Root really was empty (or only filling
// reached this subscriber before the inspection ran).
func (s *Subscriber) CacheSizeAndSampleKeys(maxSample int) (int, []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	size := len(s.cache)
	if maxSample <= 0 {
		return size, nil
	}
	if maxSample > size {
		maxSample = size
	}
	out := make([]string, 0, maxSample)
	for k := range s.cache {
		if len(out) >= maxSample {
			break
		}
		out = append(out, k)
	}
	return size, out
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
	// Stop the reconnect watchdog before tearing down the node so a
	// concurrent reconnect attempt doesn't observe a half-closed
	// node mid-Connect. Idempotent — channel may already be nil if
	// Connect was never called.
	s.reconnectOnce.Do(func() {
		if s.reconnectStop != nil {
			close(s.reconnectStop)
		}
	})
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
//
// FeedPK gate: when multiple subscribers share a CXO node (each
// for a different feed PK — e.g. one peerSub per group member on
// the owner's node), every subscriber's handleRootFilled fires on
// every Root regardless of which feed produced it. Without this
// gate, a Root for feed A causes subscriber B (which targets a
// different feed) to walk A's tree and apply A's leaves to B's
// snapshot cache. The next Root for feed B then diffs against the
// polluted cache and emits spurious deletes for A's leaves plus
// "new" events for B's leaves. The on-the-wire messages are still
// correct (sender PK on the leaf JSON drives application routing),
// but per-peer cache invariants and per-peer-staleness signals
// are scrambled.
//
// Skip non-matching Roots cleanly. Same callback chain remains in
// place so the next subscriber in the chain still receives the
// Root (it'll do its own match check).
func (s *Subscriber) handleRootFilled(r *registry.Root) {
	if r == nil || len(r.Refs) == 0 {
		// Root with no payload — could happen on startup before
		// the publisher has put anything. Treat as empty AND
		// signal first-root so ConnectAndWaitForRoot doesn't
		// block forever on a publisher whose tree is genuinely
		// empty (the subscription IS live; there's just nothing
		// to fill yet).
		s.applySnapshot(nil)
		if r != nil && r.Pub == skycipher.PubKey(s.feedPK) {
			s.signalRootObserved()
		}
		return
	}
	if r.Pub != skycipher.PubKey(s.feedPK) {
		// Root is for a different feed than this subscriber tracks.
		// The chained callback dispatcher fires every subscriber on
		// every Root; without this check each subscriber would walk
		// every feed's tree and corrupt its own cache.
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
	// Mark "data flowed" for the reconnect watchdog (#2713). Bumped
	// only on a feed-matching Root that walked successfully — non-
	// matching Roots or walk-failures don't count as live activity
	// for THIS subscriber's purposes.
	s.lastUpdateNs.Store(time.Now().UnixNano())
	// Signal any ConnectAndWaitForRoot caller blocked on this
	// subscriber that a Root was received and the fill walk
	// completed — the subscription is verifiably live now.
	s.signalRootObserved()
}

// signalRootObserved closes the current rootObservedSignal channel
// if not already closed. Idempotent per-channel: a reconnect that
// reset the signal via ConnectAndWaitForRoot would have already
// swapped to a fresh channel by the time the previous Root's late
// fill triggered handleRootFilled, so closing the old channel here
// is harmless. Used by handleRootFilled's match-feed branches.
func (s *Subscriber) signalRootObserved() {
	s.rootObservedMu.Lock()
	defer s.rootObservedMu.Unlock()
	select {
	case <-s.rootObservedSignal:
		// already closed; nothing to do
	default:
		close(s.rootObservedSignal)
	}
}

// handleFillingBreaks is the OnFillingBreaks callback. Fires when a
// Root the subscriber received fails to fill — typically because a
// transient dmsg blip during the post-Subscribe fetch interrupted the
// recursive walk that resolves child objects. Before this hook was
// wired, the failure was completely silent: the subscriber's filler
// dropped the Root, no UpdateEvents fired, and the subscriber stayed
// empty (or out-of-date) until the publisher pushed a new Root.
//
// FeedPK gate matches handleRootFilled — when multiple subscribers
// share a CXO node, only the one tracking this Root's feed should
// take the failure as its own.
//
// Today's behavior is log-only. Pairing this with #2647 (publisher
// always pushes Root on duplicate Subscribe) covers the common
// recovery path: subscriber's next reconnect-driven Subscribe gets
// the Root pushed again. A more proactive recovery (subscriber
// requests an immediate re-push from its CXO node here) is a future
// follow-up.
func (s *Subscriber) handleFillingBreaks(r *registry.Root, err error) {
	if r == nil {
		return
	}
	if r.Pub != skycipher.PubKey(s.feedPK) {
		return
	}
	s.log.WithError(err).
		WithField("feed", s.feedPK.Hex()).
		WithField("seq", r.Seq).
		Warn("treestore-sub: Root filling aborted; subscriber will miss this Root until publisher re-pushes")
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
