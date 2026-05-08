// Package visor pkg/visor/cxo_subscription_manager.go
//
// Cycle-based on-demand CXO sync manager.
//
// The hypervisor's network visualizer / metrics tabs and the visor's
// own autoconnect / CLI listing commands all want a recent snapshot
// of network-wide state from one of the deployment-side CXO
// publishers (TPD's metrics + uptime aggregates, SD's services tree,
// DMSG-D's clients-by-server tree). Keeping a long-lived subscription
// alive is wasteful — most of those updates are noise to a UI that
// might be open for a few seconds at a time, and a subscription that
// was open across a publisher restart goes silently stale because
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
package visor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// CXOFeed identifies one upstream CXO publisher feed the manager can
// sync from. Adding a new feed means: add a const here, a case in
// feedSpec, and a tab → feeds dependency entry below.
type CXOFeed int

const (
	// FeedTPDMetrics is TPD's metrics-aggregate publisher
	// (metrics/days/<n>).
	FeedTPDMetrics CXOFeed = iota
	// FeedTPDUptime is TPD's uptime-aggregate publisher
	// (uptimes/days/<n>).
	FeedTPDUptime
	// FeedSDServices is SD's services publisher
	// (services/<type>/<pk>/{entry,tombstone}).
	FeedSDServices
	// FeedDMSGDClientsByServer is DMSG-D's clients-by-server publisher
	// (clients-by-server/<server>/<client>/{entry,tombstone}).
	FeedDMSGDClientsByServer
)

// CXOTab identifies a consumer of one or more CXO feeds. Tabs in the
// hypervisor UI map onto tabs here directly; non-UI consumers
// (autoconnect, CLI commands) get their own tab kinds.
type CXOTab int

const (
	// TabNetworkVisualizer is the network visualizer tab — needs SD
	// services + DMSG-D clients-by-server + TPD metrics.
	TabNetworkVisualizer CXOTab = iota
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
)

// tabFeedDeps maps each tab to the set of feeds it depends on.
// Acquire / Release walks this list; a feed shared between tabs has
// its refcount summed across them.
var tabFeedDeps = map[CXOTab][]CXOFeed{
	TabNetworkVisualizer: {FeedSDServices, FeedDMSGDClientsByServer, FeedTPDMetrics},
	TabMetrics:           {FeedTPDMetrics, FeedTPDUptime},
	TabUptime:            {FeedTPDUptime},
	TabAutoconnect:       {FeedSDServices},
	TabCLIServices:       {FeedSDServices},
}

// CXOSubscriptionManager owns the per-feed cycle goroutines + the
// in-memory snapshots they populate. Constructed lazily by
// Visor.CXOSubMgr() on first use.
type CXOSubscriptionManager struct {
	v        *Visor
	log      *logging.Logger
	interval time.Duration // cycle period (cxo_subscribe_interval)
	grace    time.Duration // delay after last release before stopping the cycle

	mu    sync.Mutex
	feeds map[CXOFeed]*managedFeed
}

// managedFeed holds a single feed's runtime state.
type managedFeed struct {
	// Snapshot data — populated by syncOnce, read by Get / Walk.
	snapMu     sync.RWMutex
	snapshot   map[string][]byte
	lastSyncAt time.Time
	lastErr    error // sticky last error from syncOnce; cleared on success

	// Lifecycle (mutated only under manager.mu).
	refcount  int
	cancel    context.CancelFunc // stops the cycle goroutine
	done      chan struct{}      // closed when the cycle goroutine exits
	stopTimer *time.Timer        // pending grace-expiry stop
}

// defaultCXOSubscribeInterval is the cycle period when the operator
// hasn't set hypervisor.cxo_subscribe_interval. 5min matches the
// "no more than once every 5 min" intent.
const defaultCXOSubscribeInterval = 5 * time.Minute

// cxoTabCloseGrace is how long after the refcount falls to zero we
// keep running the cycle. 10s smooths over UI tab navigation flicker
// without leaking a long idle subscription.
const cxoTabCloseGrace = 10 * time.Second

// firstSyncTimeout bounds how long syncOnce waits for the first Root
// after Connect. After this the subscriber is closed and the cycle
// records an error; the next cycle (or an explicit AcquireFor that
// triggers a sync) retries from scratch. Long enough to ride out a
// dmsg-session reconnect, short enough that a UI Acquire on a dead
// publisher returns its cache-miss within ~10s.
const firstSyncTimeout = 10 * time.Second

// NewCXOSubscriptionManager constructs an idle manager. interval
// comes from hypervisor.cxo_subscribe_interval; <= 0 falls back to
// defaultCXOSubscribeInterval.
func NewCXOSubscriptionManager(v *Visor, interval time.Duration, log *logging.Logger) *CXOSubscriptionManager {
	if interval <= 0 {
		interval = defaultCXOSubscribeInterval
	}
	if log == nil {
		log = logging.MustGetLogger("cxo_subscription_manager")
	}
	return &CXOSubscriptionManager{
		v:        v,
		log:      log,
		interval: interval,
		grace:    cxoTabCloseGrace,
		feeds:    make(map[CXOFeed]*managedFeed),
	}
}

// CXOSubMgr returns the visor's manager, constructing it lazily on
// first use. Returns nil when the visor has no DMSG client (early
// startup, or DMSG disabled). Callers treat nil as "no manager;
// fall through to whatever non-CXO source you'd otherwise use" —
// every method on a nil receiver is a safe no-op.
func (v *Visor) CXOSubMgr() *CXOSubscriptionManager {
	v.initLock.Lock()
	defer v.initLock.Unlock()
	if v.cxoSubMgr != nil {
		return v.cxoSubMgr
	}
	if v.dmsgC == nil {
		return nil
	}
	interval := defaultCXOSubscribeInterval
	if v.conf != nil && v.conf.Hypervisor != nil {
		if d := time.Duration(v.conf.Hypervisor.CXOSubscribeInterval); d > 0 {
			interval = d
		}
	}
	v.cxoSubMgr = NewCXOSubscriptionManager(v, interval, v.MasterLogger().PackageLogger("cxo_subscription_manager"))
	return v.cxoSubMgr
}

// AcquireFor signals that a consumer is about to read from the
// feeds a tab depends on. Idempotent across multiple holders;
// refcount sums. On the first acquire of a feed, the cycle goroutine
// starts and a sync is kicked off immediately so the snapshot has a
// chance to populate before the caller's first Get / Walk. (Whether
// it actually does depends on dmsg session readiness; callers handle
// the cache-miss case by falling through to HTTP.)
func (m *CXOSubscriptionManager) AcquireFor(tab CXOTab) {
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
func (m *CXOSubscriptionManager) ReleaseFor(tab CXOTab) {
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
func (m *CXOSubscriptionManager) Get(feed CXOFeed, path string) ([]byte, time.Time, bool) {
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
func (m *CXOSubscriptionManager) Walk(feed CXOFeed, prefix string, fn func(path string, body []byte) bool) bool {
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
// from the visor's shutdown sequence.
func (m *CXOSubscriptionManager) Close() {
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
	m.feeds = make(map[CXOFeed]*managedFeed)
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

// LastError returns the sticky last error seen by syncOnce on a
// feed, or nil if the most recent cycle succeeded (or no cycle has
// ever run). Exposed for /health-style introspection.
func (m *CXOSubscriptionManager) LastError(feed CXOFeed) error {
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

// acquireFeedLocked must be called under m.mu.
func (m *CXOSubscriptionManager) acquireFeedLocked(fk CXOFeed) {
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
func (m *CXOSubscriptionManager) releaseFeedLocked(fk CXOFeed) {
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

// cycleLoop is the per-feed goroutine. Runs syncOnce immediately,
// then on every `interval` tick, until ctx is canceled.
func (m *CXOSubscriptionManager) cycleLoop(ctx context.Context, fk CXOFeed, f *managedFeed) {
	defer close(f.done)
	m.syncOnce(ctx, fk, f)
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.syncOnce(ctx, fk, f)
		}
	}
}

// syncOnce performs a single subscribe → wait-for-first-Root →
// snapshot → unsubscribe cycle. On any error the previous snapshot
// is left in place; callers continue serving stale data until the
// next cycle succeeds.
func (m *CXOSubscriptionManager) syncOnce(ctx context.Context, fk CXOFeed, f *managedFeed) {
	if m.v.dmsgC == nil {
		f.recordErr(errors.New("dmsg client not ready"))
		return
	}
	peerPK, port, prefix, err := m.feedSpec(fk)
	if err != nil {
		f.recordErr(err)
		return
	}

	sub, err := treestore.NewSubscriber(m.v.dmsgC, peerPK, treestore.SubConfig{
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

	if err := sub.Connect(peerPK); err != nil {
		_ = sub.Close() //nolint:errcheck
		f.recordErr(fmt.Errorf("dial publisher: %w", err))
		return
	}

	select {
	case <-rootCh:
		// First Root arrived — proceed to snapshot.
	case <-time.After(firstSyncTimeout):
		_ = sub.Close() //nolint:errcheck
		f.recordErr(fmt.Errorf("timeout waiting for Root after %s", firstSyncTimeout))
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
	_ = sub.Close() //nolint:errcheck

	f.snapMu.Lock()
	f.snapshot = snapshot
	f.lastSyncAt = time.Now()
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

// feedSpec returns the (peerPK, dmsgPort, pathPrefix) tuple for a
// feed. Returns an error if the visor doesn't have the peer's PK
// configured.
func (m *CXOSubscriptionManager) feedSpec(fk CXOFeed) (cipher.PubKey, uint16, string, error) {
	switch fk {
	case FeedTPDMetrics:
		pk, ok := tpdCXOPeer(m.v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no TPD CXO peer (transport.discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgTPDMetricsCXOPort, "metrics/days/", nil
	case FeedTPDUptime:
		pk, ok := tpdCXOPeer(m.v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no TPD CXO peer (transport.discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgTPDUptimeCXOPort, "uptimes/days/", nil
	case FeedSDServices:
		pk, ok := sdCXOPeer(m.v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no SD CXO peer (launcher.service_discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgSDServicesCXOPort, "services/", nil
	case FeedDMSGDClientsByServer:
		pk, ok := dmsgdCXOPeer(m.v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no DMSG-D CXO peer (dmsg.discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgDMSGDClientsByServerCXOPort, "clients-by-server/", nil
	}
	return cipher.PubKey{}, 0, "", fmt.Errorf("unknown feed: %d", fk)
}

// sdCXOPeer extracts the SD publisher PK from the visor's
// launcher.service_discovery_dmsg URL.
func sdCXOPeer(v *Visor) (cipher.PubKey, bool) {
	return parseDmsgPeer(v.conf.Launcher.ServiceDiscDmsg)
}

// dmsgdCXOPeer extracts the DMSG-D publisher PK from the visor's
// dmsg.discovery_dmsg URL.
func dmsgdCXOPeer(v *Visor) (cipher.PubKey, bool) {
	if v.conf.Dmsg == nil {
		return cipher.PubKey{}, false
	}
	return parseDmsgPeer(v.conf.Dmsg.DiscoveryDmsg)
}

func parseDmsgPeer(raw string) (cipher.PubKey, bool) {
	if raw == "" {
		return cipher.PubKey{}, false
	}
	var u dmsgcurl.URL
	if err := u.Fill(raw); err != nil {
		return cipher.PubKey{}, false
	}
	if u.Scheme != "dmsg" {
		return cipher.PubKey{}, false
	}
	return u.Addr.PK, true
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
