// Package visor pkg/visor/cxo_subscription_manager.go
//
// On-demand CXO subscription manager for hypervisor UI tabs.
//
// The hypervisor's network visualizer and metrics tabs source their
// data from CXO publisher feeds (TPD's transport metrics + uptime,
// SD's services tree, DMSG-D's clients-by-server tree). Keeping
// long-lived subscriptions to all four feeds when nobody is looking
// at the UI is wasteful — DMSG sessions stay open, the publishers
// keep pushing Root updates, the visor keeps receiving and ignoring
// them. Operators reasonably expect "no UI open == no traffic."
//
// This manager wraps a per-feed CXO subscriber with refcount +
// grace-period semantics:
//
//   - AcquireFor(tab) — UI tab is opening; bumps refcount on every
//     feed that tab depends on. Subscribes to feeds that aren't yet
//     active.
//   - ReleaseFor(tab) — UI tab is closing; drops refcount. When a
//     feed's refcount falls to zero, the subscription is scheduled
//     for close after a short grace period (default 10s) — long
//     enough that a navigation flicker doesn't tear-down + re-dial.
//   - Get(feed, path) — handler reads the latest cached value from
//     the active subscriber, or "not ready" if no acquire is live.
//
// `hypervisor.cxo_subscribe_interval` (default 5m) is the re-sync
// minimum: subscriptions stay open while a tab is acquired, and the
// publisher's own tick rate (~60s for metrics/uptime) drives the
// data freshness inside that window. The interval matters when tabs
// open/close repeatedly: we won't tear down a feed sooner than that
// after its last release, so a fast re-acquire reuses the live
// subscription.
package visor

import (
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

// CXOFeed identifies one upstream CXO publisher feed the manager
// can subscribe to. Adding a new feed means: add a const here, a
// case in feedSpec, and a tab → feeds dependency entry below.
type CXOFeed int

const (
	// FeedTPDMetrics is TPD's metrics-aggregate publisher, paths
	// metrics/days/<n>. Drives the network-wide transport view.
	FeedTPDMetrics CXOFeed = iota
	// FeedTPDUptime is TPD's uptime-aggregate publisher, paths
	// uptimes/days/<n>. Drives the network-wide visor-uptime view.
	FeedTPDUptime
	// FeedSDServices is SD's services publisher, paths
	// services/<type>/<pk>/{entry,tombstone}. Drives the network
	// visualizer's "which visors run which services" overlay.
	FeedSDServices
	// FeedDMSGDClientsByServer is DMSG-D's clients-by-server
	// publisher, paths clients-by-server/<server>/<client>/...
	// Drives the network visualizer's "who's on each dmsg server"
	// overlay.
	FeedDMSGDClientsByServer
)

// CXOTab identifies a hypervisor UI tab whose data sourcing is
// managed by this manager.
type CXOTab int

const (
	// TabNetworkVisualizer is the network visualizer tab — needs
	// SD services + DMSG-D clients-by-server + TPD metrics for
	// the transport graph.
	TabNetworkVisualizer CXOTab = iota
	// TabMetrics is the network metrics tab — needs TPD metrics +
	// TPD uptime aggregates.
	TabMetrics
	// TabUptime is the network uptime tab — needs TPD uptime only.
	TabUptime
)

// tabFeedDeps maps each tab to the set of feeds it depends on.
// AcquireFor / ReleaseFor walks this list; a feed shared between
// tabs has its refcount summed across them.
var tabFeedDeps = map[CXOTab][]CXOFeed{
	TabNetworkVisualizer: {FeedSDServices, FeedDMSGDClientsByServer, FeedTPDMetrics},
	TabMetrics:           {FeedTPDMetrics, FeedTPDUptime},
	TabUptime:            {FeedTPDUptime},
}

// CXOSubscriptionManager owns the lifecycle of per-feed CXO
// subscribers and the refcount/grace logic that decides when to
// open / close them. Constructed by initCXOSubscriptionManager()
// and stored on the Visor.
type CXOSubscriptionManager struct {
	v        *Visor
	log      *logging.Logger
	interval time.Duration // hypervisor.cxo_subscribe_interval (resync floor)
	grace    time.Duration // tab-close grace before actual close

	mu    sync.Mutex
	feeds map[CXOFeed]*managedFeed
	tabs  map[CXOTab]int // outstanding AcquireFor refcount per tab
}

// managedFeed holds the per-feed runtime state.
type managedFeed struct {
	sub        *treestore.Subscriber
	refcount   int         // total tabs requiring this feed
	closeTimer *time.Timer // scheduled close (nil = none pending)
	lastRootAt time.Time   // most recent OnUpdate timestamp
	openErr    error       // last subscriber-open error (sticky for diagnostics)
}

// defaultCXOSubscribeInterval is the resync floor when the operator
// hasn't set hypervisor.cxo_subscribe_interval. 5min matches the
// user-facing intent: refresh per-feed data at most that often.
const defaultCXOSubscribeInterval = 5 * time.Minute

// cxoTabCloseGrace is the delay between a feed's refcount dropping
// to zero and the subscriber actually closing. 10s smooths over UI
// navigation flicker (close-then-immediate-reopen) so we don't pay
// a fresh DMSG handshake for a 1s tab switch.
const cxoTabCloseGrace = 10 * time.Second

// NewCXOSubscriptionManager constructs a manager but doesn't open
// any subscriptions until AcquireFor is called. interval comes
// from hypervisor.cxo_subscribe_interval; <= 0 falls back to the
// default.
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
		tabs:     make(map[CXOTab]int),
	}
}

// CXOSubMgr returns the visor's on-demand CXO subscription manager,
// constructing it lazily on first use. Returns nil when the visor
// has no DMSG client (e.g. early in startup, or DMSG disabled).
// Caller treats nil as "subscription unavailable" — every Acquire/
// Release/Get on a nil receiver is a no-op.
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

// AcquireFor signals that a UI tab is opening and pulls the feeds
// it depends on into "active" state. Idempotent across multiple
// open tabs of the same kind — the refcount sums across them. Feeds
// that fail to subscribe (no DMSG, no peer PK, dial timeout) are
// logged and skipped; later Get calls return ok=false until the
// next acquire retry.
func (m *CXOSubscriptionManager) AcquireFor(tab CXOTab) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tabs[tab]++
	for _, fk := range tabFeedDeps[tab] {
		m.acquireFeedLocked(fk)
	}
}

// ReleaseFor signals that a UI tab is closing. Drops the refcount
// on each of the tab's feeds; feeds that hit zero are scheduled for
// close after the grace period.
func (m *CXOSubscriptionManager) ReleaseFor(tab CXOTab) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tabs[tab] <= 0 {
		return
	}
	m.tabs[tab]--
	for _, fk := range tabFeedDeps[tab] {
		m.releaseFeedLocked(fk)
	}
}

// Get returns the current cached body for (feed, path) and the
// timestamp of the last Root update for that feed. ok=false when no
// AcquireFor is currently holding the feed open or the subscriber
// hasn't received a value for that path yet — handlers should treat
// that as a cache miss and fall through to whatever HTTP path
// served before the cutover.
func (m *CXOSubscriptionManager) Get(feed CXOFeed, path string) ([]byte, time.Time, bool) {
	if m == nil {
		return nil, time.Time{}, false
	}
	m.mu.Lock()
	f, ok := m.feeds[feed]
	if !ok || f == nil || f.sub == nil {
		m.mu.Unlock()
		return nil, time.Time{}, false
	}
	sub := f.sub
	ts := f.lastRootAt
	m.mu.Unlock()
	body, ok := sub.Get(path)
	if !ok || len(body) == 0 {
		return nil, time.Time{}, false
	}
	return body, ts, true
}

// Close tears down every active subscription. Called from the
// visor's shutdown sequence.
func (m *CXOSubscriptionManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for fk, f := range m.feeds {
		if f.closeTimer != nil {
			f.closeTimer.Stop()
			f.closeTimer = nil
		}
		if f.sub != nil {
			_ = f.sub.Close() //nolint:errcheck
			f.sub = nil
		}
		delete(m.feeds, fk)
	}
}

// acquireFeedLocked must be called under m.mu.
func (m *CXOSubscriptionManager) acquireFeedLocked(fk CXOFeed) {
	f, ok := m.feeds[fk]
	if !ok {
		f = &managedFeed{}
		m.feeds[fk] = f
	}
	f.refcount++
	// Cancel any pending close — caller still wants this feed.
	if f.closeTimer != nil {
		f.closeTimer.Stop()
		f.closeTimer = nil
	}
	if f.sub != nil {
		// Already subscribed; nothing else to do.
		return
	}
	sub, err := m.openSubscriberLocked(fk)
	if err != nil {
		f.openErr = err
		m.log.WithError(err).WithField("feed", fk).
			Debug("Failed to open CXO subscriber; serving stale / cache-miss until next acquire")
		return
	}
	f.sub = sub
	f.openErr = nil
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
	if f.refcount > 0 || f.sub == nil {
		return
	}
	// refcount hit zero. Schedule close after grace; if a re-acquire
	// arrives before the timer fires, acquireFeedLocked cancels it.
	closeAt := m.grace
	// Honor the resync-floor as a minimum: never tear down a feed
	// sooner than `interval` after the last sync. Keeps a fast
	// reopen-after-close path subscription-stable.
	if since := time.Since(f.lastRootAt); since < m.interval {
		if remaining := m.interval - since; remaining > closeAt {
			closeAt = remaining
		}
	}
	feed := fk
	f.closeTimer = time.AfterFunc(closeAt, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		ff, ok := m.feeds[feed]
		if !ok || ff.refcount > 0 || ff.sub == nil {
			return
		}
		_ = ff.sub.Close() //nolint:errcheck
		ff.sub = nil
		ff.closeTimer = nil
	})
}

// feedSpec returns the (peerPK, dmsgPort, pathPrefix) tuple for a
// feed. Returns an error if the visor doesn't have the peer's PK
// configured (e.g. transport.discovery_dmsg empty for TPD feeds).
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

// openSubscriberLocked dials the peer's TreeStore publisher and
// returns a connected Subscriber filtered to the feed's path
// prefix. Must be called under m.mu.
func (m *CXOSubscriptionManager) openSubscriberLocked(fk CXOFeed) (*treestore.Subscriber, error) {
	if m.v.dmsgC == nil {
		return nil, errors.New("dmsg client not ready")
	}
	peerPK, port, prefix, err := m.feedSpec(fk)
	if err != nil {
		return nil, err
	}
	sub, err := treestore.NewSubscriber(m.v.dmsgC, peerPK, treestore.SubConfig{
		InMemoryDB: true,
		DmsgPort:   port,
	})
	if err != nil {
		return nil, fmt.Errorf("create subscriber: %w", err)
	}
	sub.SetPrefixes([]string{prefix})
	feed := fk
	sub.OnUpdate(func(_ []treestore.UpdateEvent) {
		m.mu.Lock()
		if f, ok := m.feeds[feed]; ok {
			f.lastRootAt = time.Now()
		}
		m.mu.Unlock()
	})
	if err := sub.Connect(peerPK); err != nil {
		_ = sub.Close() //nolint:errcheck
		return nil, fmt.Errorf("dial publisher: %w", err)
	}
	return sub, nil
}

// sdCXOPeer / dmsgdCXOPeer mirror tpdCXOPeer (init_stats.go) for
// the new SD-services and DMSG-D-clients-by-server feeds. Returns
// ok=false if the visor's config doesn't carry a parseable
// service_discovery_dmsg / dmsg.discovery_dmsg URL.
func sdCXOPeer(v *Visor) (cipher.PubKey, bool) {
	raw := v.conf.Launcher.ServiceDiscDmsg
	return parseDmsgPeer(raw)
}

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
