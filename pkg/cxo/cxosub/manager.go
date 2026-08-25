// Package cxosub pkg/cxo/cxosub/manager.go c2-net-cxo
//
// Cycle-based on-demand CXO sync manager.
//
// The hypervisor's network visualizer / metrics tabs, the visor's
// own autoconnect / CLI listing commands, and standalone services
// (e.g. the reward server) all want a recent snapshot of network-wide
// state from one of the deployment-side CXO publishers (TPD's metrics
// + uptime aggregates, SD's services tree, DMSG-D's clients-by-server
// tree). Keeping a long-lived subscription alive is wasteful — most
// of those updates are noise to a UI that might be open for a few
// seconds at a time, and a subscription that was open across a
// publisher restart goes silently stale because
// `treestore.Subscriber.Connect` is one-shot with no built-in
// reconnect.
//
// This manager runs each feed as a periodic *cycle*:
//
//	subscribe → wait for first Root → walk into local snapshot
//	→ unsubscribe → sleep `interval` → repeat
//
// Cycles only run while at least one consumer holds the feed via
// AcquireFor. When the last release happens the cycle stops on the
// next interval boundary (with a small grace), so a fast acquire-
// release-acquire reuses the running cycle without re-dialing.
//
// Get / Walk read the *snapshot*, not a live subscriber — so the
// data they see is always at most `interval`-old, never tied to a
// dmsg session that may have died. A publisher restart self-heals
// on the next sync because each cycle dials fresh.
//
// The manager is decoupled from any host type via Deps: the caller
// injects a live-dmsg-client accessor and a feed→(peer, port, prefix)
// resolver, so both the visor and a standalone service can drive it.
package cxosub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Feed identifies one upstream CXO publisher feed the manager can
// sync from. Adding a new feed means: add a const here, a case in the
// injected FeedSpec resolver, and a tab → feeds dependency entry
// below.
type Feed int

const (
	// FeedTPDMetrics is TPD's metrics-aggregate publisher
	// (metrics/days/<n>).
	FeedTPDMetrics Feed = iota
	// FeedTPDUptime is TPD's uptime-aggregate publisher
	// (uptimes/days/<n>).
	FeedTPDUptime
	// FeedSDServices is SD's services publisher
	// (services/<type>/<pk>/{entry,tombstone}).
	FeedSDServices
	// FeedDMSGDClientsByServer is DMSG-D's clients-by-server publisher
	// (one batched leaf per server at clients-by-server/<server>; older
	// publishers used clients-by-server/<server>/<client>/entry).
	FeedDMSGDClientsByServer
	// FeedTPDAllTransports is TPD's all-transports snapshot publisher
	// (transports/all/{with-self,without-self}). Used by the CLI's
	// `pv -t`, `tp tree`, `tp viz` commands as a CXO alternative to
	// HTTP-polling /all-transports.
	FeedTPDAllTransports
)

// FeedRoute returns the fixed dmsg CXO port and TreeStore path prefix a feed's
// publisher uses. The publisher PK is service-specific and supplied separately
// by the caller's FeedSpec — centralizing this (port, prefix) mapping keeps every
// FeedSpec (visor config-derived, reward-server deployment-derived) consistent.
// ok is false for an unknown feed.
func FeedRoute(f Feed) (port uint16, prefix string, ok bool) {
	switch f {
	case FeedTPDMetrics:
		return skyenv.DmsgTPDMetricsCXOPort, "metrics/days/", true
	case FeedTPDUptime:
		return skyenv.DmsgTPDUptimeCXOPort, "uptimes/days/", true
	case FeedSDServices:
		return skyenv.DmsgSDServicesCXOPort, "services/", true
	case FeedDMSGDClientsByServer:
		return skyenv.DmsgDMSGDClientsByServerCXOPort, "clients-by-server/", true
	case FeedTPDAllTransports:
		return skyenv.DmsgTPDAllTransportsCXOPort, "transports/all/", true
	}
	return 0, "", false
}

// Tab identifies a consumer of one or more CXO feeds. Tabs in the
// hypervisor UI map onto tabs here directly; non-UI consumers
// (autoconnect, CLI commands) get their own tab kinds.
type Tab int

const (
	// TabNetworkVisualizer is the network visualizer tab — needs SD
	// services + DMSG-D clients-by-server + TPD metrics.
	TabNetworkVisualizer Tab = iota
	// TabMetrics is the metrics tab — TPD metrics + TPD uptime.
	TabMetrics
	// TabUptime is the network uptime tab — TPD uptime only.
	TabUptime
	// TabAutoconnect is the visor's public-visor autoconnect cycle —
	// SD services only (filters down to the visor type at walk time).
	TabAutoconnect
	// TabCLIServices is the operator-facing service-listing CLI
	// commands (`skywire cli proxy list`, `vpn list`, `pv`). Same
	// SD services feed; refcount summed with TabAutoconnect when
	// both are active so they share the running cycle.
	TabCLIServices
	// TabCLITransports is the operator-facing transport-listing CLI
	// commands (`pv -t`, `tp tree`, `tp viz`). Maps to the TPD
	// all-transports feed.
	TabCLITransports
	// TabRoutingPolicy is the operator-programmable routing-policy
	// provider's SD-services consumer. Needed so the policy script's
	// geo.country(pk) can resolve multihop intermediates that
	// advertise themselves as proxy/vpn/visor services.
	TabRoutingPolicy
	// TabDmsgEntryLookup is the visor's dmsg-entry-lookup-over-CXO
	// consumer. Held for the lifetime of the dmsg client so the
	// clients-by-server snapshot stays warm, letting peer entry
	// lookups resolve from CXO instead of a fresh HTTP-over-dmsg
	// Noise handshake. HTTP stays the fallback on a snapshot miss.
	TabDmsgEntryLookup
)

// tabFeedDeps maps each tab to the set of feeds it depends on.
// Acquire / Release walks this list; a feed shared between tabs has
// its refcount summed across them.
var tabFeedDeps = map[Tab][]Feed{
	TabNetworkVisualizer: {FeedSDServices, FeedDMSGDClientsByServer, FeedTPDMetrics},
	TabMetrics:           {FeedTPDMetrics, FeedTPDUptime},
	TabUptime:            {FeedTPDUptime},
	TabAutoconnect:       {FeedSDServices},
	TabCLIServices:       {FeedSDServices},
	TabCLITransports:     {FeedTPDAllTransports},
	TabRoutingPolicy:     {FeedSDServices},
	TabDmsgEntryLookup:   {FeedDMSGDClientsByServer},
}

// Deps are the host-injected dependencies the manager needs. They
// decouple it from any concrete host type (*visor.Visor, a standalone
// reward server, …).
type Deps struct {
	// Dmsg returns the live dmsg client (may be nil during early
	// startup or when DMSG is disabled). Called fresh on each sync so
	// the manager always uses the current client.
	Dmsg func() *dmsg.Client
	// FeedSpec resolves a feed to its (publisher PK, CXO dmsg port,
	// TreeStore path prefix). Returns an error when the publisher
	// isn't configured — meaning the feed can never sync until the
	// host's config is fixed.
	FeedSpec func(Feed) (peerPK cipher.PubKey, port uint16, prefix string, err error)
	// Log is the logger; nil falls back to a default package logger.
	Log *logging.Logger
}

// Manager owns the per-feed cycle goroutines + the in-memory
// snapshots they populate. Construct with NewManager.
type Manager struct {
	deps     Deps
	log      *logging.Logger
	interval time.Duration // cycle period (cxo_subscribe_interval)
	grace    time.Duration // delay after last release before stopping the cycle

	mu    sync.Mutex
	feeds map[Feed]*managedFeed
}

// managedFeed holds a single feed's runtime state.
type managedFeed struct {
	// syncMu serializes syncOnce for this feed. syncOnce binds a fixed
	// per-feed dmsg port (feedSpec's port) for the lifetime of one
	// subscribe→snapshot→close cycle; two concurrent syncOnce calls
	// would collide with "dmsg listen on port N: port already occupied"
	// and both fail. The cycleLoop goroutine and an explicit RefreshNow
	// are independent callers, so without this lock a RefreshNow issued
	// while the cycle's syncOnce is mid-flight breaks the feed. Held for
	// the whole cycle so the port is free before the next caller binds it.
	syncMu sync.Mutex

	// Snapshot data — populated by syncOnce, read by Get / Walk.
	snapMu     sync.RWMutex
	snapshot   map[string][]byte
	lastSyncAt time.Time
	lastErr    error // sticky last error from syncOnce; cleared on success
	// lastSyncCachePaths / lastSyncCacheKeys: discriminator for empty
	// snapshots (publisher sent empty Root vs manager-side filter
	// dropped everything). Captured in syncOnce right before
	// sub.Close. Surfaced via FeedStatus for `cxo status`.
	lastSyncCachePaths int
	lastSyncCacheKeys  []string

	// Lifecycle (mutated only under manager.mu).
	refcount  int
	cancel    context.CancelFunc // stops the cycle goroutine
	done      chan struct{}      // closed when the cycle goroutine exits
	stopTimer *time.Timer        // pending grace-expiry stop
}

// DefaultSubscribeInterval is the cycle period when the operator
// hasn't set hypervisor.cxo_subscribe_interval. 5min matches the
// "no more than once every 5 min" intent.
const DefaultSubscribeInterval = 5 * time.Minute

// TabCloseGrace is how long after the refcount falls to zero we
// keep running the cycle. 10s smooths over UI tab navigation flicker
// without leaking a long idle subscription.
const TabCloseGrace = 10 * time.Second

// FirstSyncTimeout bounds how long syncOnce waits for the first Root
// after Connect. After this the subscriber is closed and the cycle
// records an error; the next cycle (or an explicit AcquireFor that
// triggers a sync) retries from scratch. Long enough to ride out a
// dmsg-session reconnect, short enough that a UI Acquire on a dead
// publisher returns its cache-miss within ~10s.
const FirstSyncTimeout = 10 * time.Second

// largeFeedFirstSyncTimeout is the first-Root wait for the big feeds.
// all-transports (~8MB network-wide snapshot) and sd-services (the full
// service-discovery listing, ~1000 servers) are large, deep trees that
// routinely need well over FirstSyncTimeout to deliver their first Root
// over dmsg — a 10s bound left them never syncing over CXO (sd-services
// timed out at 10s → empty proxy/vpn pickers). These feeds get the room
// they need; the small, fast feeds keep the tighter default so a UI
// Acquire on a dead publisher still returns its cache-miss within ~10s.
const largeFeedFirstSyncTimeout = 45 * time.Second

// FeedFirstSyncTimeout returns the first-Root wait for a feed. Per-feed so the
// large snapshots sync reliably without slowing a UI Acquire on the small,
// fast feeds.
func FeedFirstSyncTimeout(f Feed) time.Duration {
	switch f {
	case FeedTPDAllTransports, FeedSDServices:
		return largeFeedFirstSyncTimeout
	}
	return FirstSyncTimeout
}

// NewManager constructs an idle, ready manager. interval comes from
// hypervisor.cxo_subscribe_interval; <= 0 falls back to
// DefaultSubscribeInterval. deps.Log nil falls back to a default
// package logger.
func NewManager(deps Deps, interval time.Duration) *Manager {
	if interval <= 0 {
		interval = DefaultSubscribeInterval
	}
	log := deps.Log
	if log == nil {
		log = logging.MustGetLogger("cxosub")
	}
	return &Manager{
		deps:     deps,
		log:      log,
		interval: interval,
		grace:    TabCloseGrace,
		feeds:    make(map[Feed]*managedFeed),
	}
}

// dmsgClient returns the live dmsg client via the injected accessor,
// or nil if none was injected.
func (m *Manager) dmsgClient() *dmsg.Client {
	if m.deps.Dmsg == nil {
		return nil
	}
	return m.deps.Dmsg()
}

// feedSpec resolves a feed via the injected resolver.
func (m *Manager) feedSpec(fk Feed) (cipher.PubKey, uint16, string, error) {
	if m.deps.FeedSpec == nil {
		return cipher.PubKey{}, 0, "", errors.New("no feed resolver configured")
	}
	return m.deps.FeedSpec(fk)
}

// AcquireFor signals that a consumer is about to read from the
// feeds a tab depends on. Idempotent across multiple holders;
// refcount sums. On the first acquire of a feed, the cycle goroutine
// starts and a sync is kicked off immediately so the snapshot has a
// chance to populate before the caller's first Get / Walk. (Whether
// it actually does depends on dmsg session readiness; callers handle
// the cache-miss case by falling through to HTTP.)
func (m *Manager) AcquireFor(tab Tab) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, fk := range tabFeedDeps[tab] {
		m.acquireFeedLocked(fk)
	}
}

// ReleaseFor signals that a consumer is done. Drops the refcount on
// each feed the tab depends on. Feeds whose refcount falls to zero
// schedule their cycle goroutine to stop after the grace period —
// during which a fresh AcquireFor cancels the stop and reuses the
// running cycle.
func (m *Manager) ReleaseFor(tab Tab) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, fk := range tabFeedDeps[tab] {
		m.releaseFeedLocked(fk)
	}
}

// Get returns the cached body for (feed, path) plus the timestamp of
// the most recent successful sync, or ok=false if the feed has no
// snapshot yet (no acquire ever happened, the first cycle hasn't
// completed, or every cycle failed). Callers handle ok=false by
// falling through to HTTP.
func (m *Manager) Get(feed Feed, path string) ([]byte, time.Time, bool) {
	if m == nil {
		return nil, time.Time{}, false
	}
	m.mu.Lock()
	f, ok := m.feeds[feed]
	m.mu.Unlock()
	if !ok || f == nil {
		return nil, time.Time{}, false
	}
	f.snapMu.RLock()
	defer f.snapMu.RUnlock()
	if f.snapshot == nil {
		return nil, time.Time{}, false
	}
	body, ok := f.snapshot[path]
	if !ok || len(body) == 0 {
		return nil, time.Time{}, false
	}
	out := make([]byte, len(body))
	copy(out, body)
	return out, f.lastSyncAt, true
}

// Walk invokes fn for every (path, body) in the feed's cached
// snapshot whose path begins with prefix. Returns ok=false (with fn
// never called) if the feed has no snapshot. Bodies passed to fn
// are owned by the snapshot — callers must copy if they need to
// retain bytes after fn returns.
func (m *Manager) Walk(feed Feed, prefix string, fn func(path string, body []byte) bool) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	f, ok := m.feeds[feed]
	m.mu.Unlock()
	if !ok || f == nil {
		return false
	}
	f.snapMu.RLock()
	defer f.snapMu.RUnlock()
	if len(f.snapshot) == 0 {
		return false
	}
	for path, body := range f.snapshot {
		if prefix != "" && !hasPathPrefix(path, prefix) {
			continue
		}
		if !fn(path, body) {
			return true
		}
	}
	return true
}

// Close stops every active cycle and drops every snapshot. Called
// from the host's shutdown sequence.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	feeds := make([]*managedFeed, 0, len(m.feeds))
	for _, f := range m.feeds {
		if f.stopTimer != nil {
			f.stopTimer.Stop()
			f.stopTimer = nil
		}
		feeds = append(feeds, f)
	}
	m.feeds = make(map[Feed]*managedFeed)
	m.mu.Unlock()
	for _, f := range feeds {
		if f.cancel != nil {
			f.cancel()
		}
		if f.done != nil {
			<-f.done
		}
	}
}

// FeedStatus is one row of Manager.Status. Tells the operator
// everything needed to decide whether a CXO miss is "the snapshot is
// genuinely empty", "the cycle has never run", or "the cycle keeps
// failing for reason X". Exposed via the visor RPC so `skywire cli
// visor cxo status` can render it.
type FeedStatus struct {
	// Feed is the FeedXxx constant's stable string name (matches the
	// label FeedString returns).
	Feed string
	// PeerPK is the publisher's PK (or empty if not configured).
	PeerPK string
	// DmsgPort is the publisher's CXO port.
	DmsgPort uint16
	// Prefix is the TreeStore path prefix this feed walks under.
	Prefix string
	// SnapshotPaths is the number of (path, body) entries currently
	// cached. 0 + LastSyncAt zero means "no cycle has ever completed".
	SnapshotPaths int
	// SnapshotBytes is the sum of body lengths in the snapshot. Quick
	// "how much memory is this feed using" check.
	SnapshotBytes int
	// LastSyncAt is the wall-clock time of the most recent successful
	// sync. Zero when no cycle has ever finished cleanly.
	LastSyncAt time.Time
	// LastErr is the sticky last error from syncOnce, cleared on
	// success. Empty when the most recent cycle worked.
	LastErr string
	// Refcount is the number of holders that have currently AcquireFor'd
	// this feed without releasing it. >0 means a cycle is running.
	Refcount int
	// CycleRunning is true while the per-feed goroutine is alive (i.e.
	// inside its tick loop). Goes false during the grace-period close.
	CycleRunning bool
	// SpecErr is non-empty if the feed resolver couldn't resolve the
	// publisher (e.g. SD CXO peer not configured) — meaning the feed
	// can never sync until the host config is fixed.
	SpecErr string
	// LastSyncCachePaths is len(sub.cache) snapshotted right before the
	// most recent syncOnce closed its subscriber. Discriminator for
	// "SnapshotPaths=0 with no error": when this is also 0 the
	// publisher's Root was structurally empty; when this is >0 but
	// SnapshotPaths=0 the manager's snapshot copy / prefix filter is
	// the culprit.
	LastSyncCachePaths int
	// LastSyncCacheSampleKeys is up to 5 keys from sub.cache at the
	// moment LastSyncCachePaths was captured. Lets the operator
	// eyeball whether the publisher is writing the paths the
	// subscriber's prefix expects.
	LastSyncCacheSampleKeys []string
}

// Status returns a snapshot of every feed the manager knows about
// (i.e. every feed someone has ever acquired). Feeds that were never
// touched don't appear; callers should treat absence as "no cycle
// ever started". Safe to call on a nil receiver — returns nil.
func (m *Manager) Status() []FeedStatus {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	out := make([]FeedStatus, 0, len(m.feeds))
	for fk, f := range m.feeds {
		st := FeedStatus{Feed: FeedString(fk)}
		if pk, port, prefix, err := m.feedSpec(fk); err != nil {
			st.SpecErr = err.Error()
		} else {
			st.PeerPK = pk.Hex()
			st.DmsgPort = port
			st.Prefix = prefix
		}
		st.Refcount = f.refcount
		st.CycleRunning = f.cancel != nil
		f.snapMu.RLock()
		st.SnapshotPaths = len(f.snapshot)
		for _, body := range f.snapshot {
			st.SnapshotBytes += len(body)
		}
		st.LastSyncAt = f.lastSyncAt
		if f.lastErr != nil {
			st.LastErr = f.lastErr.Error()
		}
		st.LastSyncCachePaths = f.lastSyncCachePaths
		if len(f.lastSyncCacheKeys) > 0 {
			st.LastSyncCacheSampleKeys = append([]string(nil), f.lastSyncCacheKeys...)
		}
		f.snapMu.RUnlock()
		out = append(out, st)
	}
	m.mu.Unlock()
	return out
}

// RefreshNow forces a fresh subscribe → first-Root → walk cycle for
// `feed` and blocks until either the cycle reports success/failure or
// the caller's ctx expires. Unlike AcquireFor — which starts a cycle
// goroutine and returns immediately, leaving the caller racing the
// first sync — this is synchronous: the returned FeedStatus reflects
// the snapshot AFTER the sync attempt. Used by `skywire cli visor cxo
// refresh` so an operator can deterministically test "can this visor
// actually reach the publisher right now".
//
// The cycle goroutine started by AcquireFor (if any) is left alone;
// this opens its own short-lived subscriber.
func (m *Manager) RefreshNow(ctx context.Context, feed Feed) (FeedStatus, error) {
	if m == nil {
		return FeedStatus{}, errors.New("cxo manager not initialized (no DMSG client?)")
	}
	m.mu.Lock()
	f, ok := m.feeds[feed]
	if !ok {
		f = &managedFeed{}
		m.feeds[feed] = f
	}
	// f.cancel != nil means a cycle goroutine is running (set in
	// acquireFeedLocked, cleared by the grace-stop). Under the live
	// model that cycle owns a persistent subscriber on this feed's
	// dmsg port and holds syncMu for its whole life, so calling
	// syncOnce here would block forever on syncMu (and collide on the
	// port). The live snapshot is already the freshest data available,
	// so wait (bounded) for it to be populated instead of forcing a
	// second subscribe.
	cycleRunning := f.cancel != nil
	m.mu.Unlock()

	if cycleRunning {
		m.waitForSnapshot(ctx, feed, f)
	} else {
		m.syncOnce(ctx, feed, f)
	}
	st := m.statusFor(feed, f)
	if f.lastErr != nil {
		return st, f.lastErr
	}
	return st, nil
}

// waitForSnapshot blocks until the live cycle has populated a non-empty
// snapshot for the feed, ctx is done, or the feed's first-sync timeout
// elapses. Used by RefreshNow when a persistent cycle already owns the
// feed's subscriber (see RefreshNow).
func (m *Manager) waitForSnapshot(ctx context.Context, feed Feed, f *managedFeed) {
	f.snapMu.RLock()
	has := len(f.snapshot) > 0
	f.snapMu.RUnlock()
	if has {
		return
	}
	deadline := time.NewTimer(FeedFirstSyncTimeout(feed))
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
			f.snapMu.RLock()
			has := len(f.snapshot) > 0
			f.snapMu.RUnlock()
			if has {
				return
			}
		}
	}
}

// statusFor builds a FeedStatus for one feed under the manager's lock
// discipline. Pulled out so Status() and RefreshNow() share one
// implementation.
func (m *Manager) statusFor(fk Feed, f *managedFeed) FeedStatus {
	st := FeedStatus{Feed: FeedString(fk)}
	if pk, port, prefix, err := m.feedSpec(fk); err != nil {
		st.SpecErr = err.Error()
	} else {
		st.PeerPK = pk.Hex()
		st.DmsgPort = port
		st.Prefix = prefix
	}
	m.mu.Lock()
	st.Refcount = f.refcount
	st.CycleRunning = f.cancel != nil
	m.mu.Unlock()
	f.snapMu.RLock()
	st.SnapshotPaths = len(f.snapshot)
	for _, body := range f.snapshot {
		st.SnapshotBytes += len(body)
	}
	st.LastSyncAt = f.lastSyncAt
	if f.lastErr != nil {
		st.LastErr = f.lastErr.Error()
	}
	st.LastSyncCachePaths = f.lastSyncCachePaths
	if len(f.lastSyncCacheKeys) > 0 {
		st.LastSyncCacheSampleKeys = append([]string(nil), f.lastSyncCacheKeys...)
	}
	f.snapMu.RUnlock()
	return st
}

// FeedString returns the stable string label for a feed constant.
// Used by Status / RefreshNow so RPC clients don't need to import the
// Feed integer enum.
func FeedString(feed Feed) string {
	switch feed {
	case FeedTPDMetrics:
		return "tpd-metrics"
	case FeedTPDUptime:
		return "tpd-uptime"
	case FeedSDServices:
		return "sd-services"
	case FeedDMSGDClientsByServer:
		return "dmsgd-clients-by-server"
	case FeedTPDAllTransports:
		return "tpd-all-transports"
	}
	return fmt.Sprintf("feed#%d", feed)
}

// FeedFromString is the inverse of FeedString. Returns ok=false
// for any unrecognized name.
func FeedFromString(name string) (Feed, bool) {
	switch name {
	case "tpd-metrics":
		return FeedTPDMetrics, true
	case "tpd-uptime":
		return FeedTPDUptime, true
	case "sd-services":
		return FeedSDServices, true
	case "dmsgd-clients-by-server":
		return FeedDMSGDClientsByServer, true
	case "tpd-all-transports":
		return FeedTPDAllTransports, true
	}
	return 0, false
}

// LastError returns the sticky last error seen by syncOnce on a
// feed, or nil if the most recent cycle succeeded (or no cycle has
// ever run). Exposed for /health-style introspection.
func (m *Manager) LastError(feed Feed) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	f, ok := m.feeds[feed]
	m.mu.Unlock()
	if !ok || f == nil {
		return nil
	}
	f.snapMu.RLock()
	defer f.snapMu.RUnlock()
	return f.lastErr
}

// LastSync returns the wall-clock time of the feed's most recent
// successful sync, or the zero time if no cycle has ever completed.
// Consumers use it as a cheap snapshot-version to memoize a derived
// index and rebuild it only when the underlying snapshot advances.
// Safe on a nil receiver.
func (m *Manager) LastSync(feed Feed) time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.Lock()
	f, ok := m.feeds[feed]
	m.mu.Unlock()
	if !ok || f == nil {
		return time.Time{}
	}
	f.snapMu.RLock()
	defer f.snapMu.RUnlock()
	return f.lastSyncAt
}

// acquireFeedLocked must be called under m.mu.
func (m *Manager) acquireFeedLocked(fk Feed) {
	f, ok := m.feeds[fk]
	if !ok {
		f = &managedFeed{}
		m.feeds[fk] = f
	}
	f.refcount++
	if f.stopTimer != nil {
		f.stopTimer.Stop()
		f.stopTimer = nil
	}
	if f.cancel == nil {
		// First reference: start the cycle goroutine. cancel is
		// stashed on the feed and called from releaseFeedLocked
		// (via the grace timer) or Close — gosec's "context cancel
		// not called" check can't see across the timer hop.
		ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec
		f.cancel = cancel
		f.done = make(chan struct{})
		go m.cycleLoop(ctx, fk, f)
	}
}

// releaseFeedLocked must be called under m.mu.
func (m *Manager) releaseFeedLocked(fk Feed) {
	f, ok := m.feeds[fk]
	if !ok {
		return
	}
	if f.refcount > 0 {
		f.refcount--
	}
	if f.refcount > 0 {
		return
	}
	// refcount hit zero — schedule cycle stop after grace. A fresh
	// acquire before the timer fires cancels it.
	if f.stopTimer != nil {
		return
	}
	feed := fk
	f.stopTimer = time.AfterFunc(m.grace, func() {
		m.mu.Lock()
		ff, ok := m.feeds[feed]
		if !ok || ff.refcount > 0 {
			if ff != nil {
				ff.stopTimer = nil
			}
			m.mu.Unlock()
			return
		}
		cancel := ff.cancel
		done := ff.done
		ff.cancel = nil
		ff.done = nil
		ff.stopTimer = nil
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	})
}

// cycleLoop is the per-feed goroutine. It keeps a single subscriber
// connected to the publisher for the whole time the feed is held and
// re-walks the snapshot on every Root the publisher pushes (liveServe).
// This is both snappier and lighter than the old subscribe→walk→close→
// sleep(interval) poll: one dmsg dial instead of a re-dial every
// interval, and no publisher-side load beyond the Roots it already
// pushes. liveServe only returns on a setup/first-Root failure; the
// loop then retries with capped backoff so a temporarily-down publisher
// is picked up without hammering it.
func (m *Manager) cycleLoop(ctx context.Context, fk Feed, f *managedFeed) {
	defer close(f.done)
	backoff := initialLiveRetry
	for {
		if ctx.Err() != nil {
			return
		}
		if err := m.liveServe(ctx, fk, f); err != nil && ctx.Err() == nil {
			f.recordErr(err)
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > m.interval {
			backoff = m.interval
		}
	}
}

// initialLiveRetry is the first backoff after a failed subscribe/first-
// Root before liveServe is retried; it doubles up to m.interval.
const initialLiveRetry = 5 * time.Second

// liveServe holds one subscriber connected for as long as ctx is live.
// It waits for the first Root (snapshotting it), then re-walks on every
// subsequent Root the publisher pushes. The subscriber's own reconnect
// watchdog recovers dropped dmsg sessions after the first Root, so a
// drop does not return here — liveServe returns only when ctx is done
// (clean, nil) or the initial subscribe/connect/first-Root fails (the
// error, so cycleLoop can back off and retry). syncMu is held for the
// whole session: it owns the feed's fixed dmsg port until it returns,
// which is what keeps RefreshNow from binding a second subscriber on the
// same port (RefreshNow detects the running cycle and waits instead).
func (m *Manager) liveServe(ctx context.Context, fk Feed, f *managedFeed) error {
	f.syncMu.Lock()
	defer f.syncMu.Unlock()

	dmsgC := m.dmsgClient()
	if dmsgC == nil {
		return errors.New("dmsg client not ready")
	}
	peerPK, port, prefix, err := m.feedSpec(fk)
	if err != nil {
		return err
	}
	sub, err := treestore.NewSubscriber(dmsgC, peerPK, treestore.SubConfig{
		InMemoryDB: true,
		DmsgPort:   port,
	})
	if err != nil {
		return fmt.Errorf("create subscriber: %w", err)
	}
	sub.SetPrefixes([]string{prefix})
	defer func() { _ = sub.Close() }() //nolint:errcheck

	// Buffered(1) so a burst of Roots coalesces into a single pending
	// re-walk (natural debounce → bounds subscriber CPU on chatty feeds).
	updateCh := make(chan struct{}, 1)
	sub.OnUpdate(func(_ []treestore.UpdateEvent) {
		select {
		case updateCh <- struct{}{}:
		default:
		}
	})

	if err := sub.Connect(ctx, peerPK); err != nil {
		return fmt.Errorf("dial publisher: %w", err)
	}

	syncTimeout := FeedFirstSyncTimeout(fk)
	first := time.NewTimer(syncTimeout)
	defer first.Stop()
	select {
	case <-updateCh:
		first.Stop()
		m.walkIntoSnapshot(sub, prefix, f)
	case <-first.C:
		return fmt.Errorf("timeout waiting for Root after %s", syncTimeout)
	case <-ctx.Done():
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-updateCh:
			m.walkIntoSnapshot(sub, prefix, f)
		}
	}
}

// walkIntoSnapshot re-walks the subscriber's cache under prefix and
// replaces the feed snapshot. Cache stats are captured while the
// subscriber is still open (live mode never closes it between walks).
func (m *Manager) walkIntoSnapshot(sub *treestore.Subscriber, prefix string, f *managedFeed) {
	snapshot := make(map[string][]byte)
	sub.Walk(prefix, func(path string, body []byte) bool {
		// Walk passes a copy of body, so storing it is safe.
		snapshot[path] = body
		return true
	})
	cacheSize, cacheKeys := sub.CacheSizeAndSampleKeys(5)
	f.snapMu.Lock()
	f.snapshot = snapshot
	f.lastSyncAt = time.Now()
	f.lastSyncCachePaths = cacheSize
	f.lastSyncCacheKeys = cacheKeys
	f.lastErr = nil
	f.snapMu.Unlock()
}

// syncOnce performs a single subscribe → wait-for-first-Root →
// snapshot → unsubscribe cycle. On any error the previous snapshot
// is left in place; callers continue serving stale data until the
// next cycle succeeds.
func (m *Manager) syncOnce(ctx context.Context, fk Feed, f *managedFeed) {
	// Serialize per feed: syncOnce binds a fixed dmsg port for the
	// duration of the subscribe→snapshot→close cycle. The cycleLoop and
	// RefreshNow are independent callers; overlapping them collides on
	// the port. See managedFeed.syncMu.
	f.syncMu.Lock()
	defer f.syncMu.Unlock()

	dmsgC := m.dmsgClient()
	if dmsgC == nil {
		f.recordErr(errors.New("dmsg client not ready"))
		return
	}
	peerPK, port, prefix, err := m.feedSpec(fk)
	if err != nil {
		f.recordErr(err)
		return
	}

	sub, err := treestore.NewSubscriber(dmsgC, peerPK, treestore.SubConfig{
		InMemoryDB: true,
		DmsgPort:   port,
	})
	if err != nil {
		f.recordErr(fmt.Errorf("create subscriber: %w", err))
		return
	}
	sub.SetPrefixes([]string{prefix})

	rootCh := make(chan struct{}, 1)
	sub.OnUpdate(func(_ []treestore.UpdateEvent) {
		select {
		case rootCh <- struct{}{}:
		default:
		}
	})

	if err := sub.Connect(ctx, peerPK); err != nil {
		_ = sub.Close() //nolint:errcheck
		f.recordErr(fmt.Errorf("dial publisher: %w", err))
		return
	}

	syncTimeout := FeedFirstSyncTimeout(fk)
	select {
	case <-rootCh:
		// First Root arrived — proceed to snapshot.
	case <-time.After(syncTimeout):
		_ = sub.Close() //nolint:errcheck
		f.recordErr(fmt.Errorf("timeout waiting for Root after %s", syncTimeout))
		return
	case <-ctx.Done():
		_ = sub.Close() //nolint:errcheck
		return
	}

	snapshot := make(map[string][]byte)
	sub.Walk(prefix, func(path string, body []byte) bool {
		// Walk passes a copy of body, so storing it is safe.
		snapshot[path] = body
		return true
	})
	// Capture the raw cache stats BEFORE Close so the FeedStatus
	// discriminator (LastSyncCachePaths vs SnapshotPaths) is honest:
	// cache>0 + snapshot=0 means the manager's prefix filter or
	// snapshot copy dropped everything; both 0 means the publisher's
	// Root was structurally empty.
	cacheSize, cacheKeys := sub.CacheSizeAndSampleKeys(5)
	_ = sub.Close() //nolint:errcheck

	f.snapMu.Lock()
	f.snapshot = snapshot
	f.lastSyncAt = time.Now()
	f.lastSyncCachePaths = cacheSize
	f.lastSyncCacheKeys = cacheKeys
	f.lastErr = nil
	f.snapMu.Unlock()
}

// recordErr stores the error on the feed for LastError to surface.
// Does not touch the snapshot — the previous one stays in place.
func (f *managedFeed) recordErr(err error) {
	f.snapMu.Lock()
	f.lastErr = err
	f.snapMu.Unlock()
}

// hasPathPrefix matches the semantics of the upstream
// treestore.HasPrefix without the package import: empty prefix
// matches everything, and the prefix must end at a path-segment
// boundary (or the path must equal the prefix).
func hasPathPrefix(path, prefix string) bool {
	if prefix == "" {
		return true
	}
	if path == prefix {
		return true
	}
	if len(path) <= len(prefix) {
		return false
	}
	if path[:len(prefix)] != prefix {
		return false
	}
	// Allow either the prefix already includes a trailing '/', or the
	// next char in path is '/'.
	last := prefix[len(prefix)-1]
	if last == '/' {
		return true
	}
	return path[len(prefix)] == '/'
}
