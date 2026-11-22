// Package rewardsserver pkg/deployment/rewards/server/statscxo.go c3-app-rewards
//
// CXO as the first source for the statistics pages, with HTTP-over-dmsg
// kept behind it.
//
// WHY. The statistics pages fetched everything from TPD over
// HTTP-over-dmsg, and those fetches fail intermittently with EOF —
// observed live on /stats and /stats/charts for bodies as small as
// 2.7 KB, and for a /metric/visor/{pks} URL ~1,400 characters long. It
// is not size. The reward server's long-lived dmsg client periodically
// loses its sessions ("no mux session available for ping",
// "session closed"; skycoin/skywire#4538) and every request issued
// inside that window dies outright, because an HTTP fetch is
// all-or-nothing AT REQUEST TIME.
//
// A CXO subscriber has no such exposure. It holds a local snapshot that
// was filled in the background, so a read is served from memory and is
// whole-object-or-nothing: a leaf is either present and complete or
// absent, never half a body under a 200.
//
// WHAT THIS FILE PROVIDES. One cxosub.Manager built over the dmsg
// client the reward server ALREADY holds — never a fresh identity;
// minting throwaway keys is the bug removed in #4501/#4502 — plus typed
// readers for the leaves the pages need:
//
//	stats/network        replaces GET /all-transports/stats
//	stats/versions       replaces GET /version
//	stats/daily          replaces GET /metric?days=30
//	metrics/day/<date>   replaces GET /metrics?days=N&bandwidth=true
//	                     AND the per-visor GET /metric/visor/{pks}
//
// The last line is the largest win and the reason this file holds the
// per-transport feed at all. The per-transport day leaves carry `edges`
// and per-day, per-edge bandwidth, so per-visor bandwidth is a local
// sum by edge public key — the 1,400-character URL is not shortened,
// it is deleted. The same records are what the min()-verified network
// total is computed from, so that figure keeps its three-branch trust
// model (see fillTPDBandwidth) applied to identical inputs rather than
// being quietly swapped for TPD's own cumulative aggregate, which is a
// different number.
//
// THE COST, stated plainly: the per-transport feed is ~30 day leaves of
// a few megabytes each. That is a large first fill. It is paid once per
// process — a settled day's leaf is content-addressed and hashes the
// same forever, so only the current day moves afterwards — and it
// replaces three separate HTTP bodies, one of which (the 7-day
// per-transport table) is tens of megabytes on every uncached request.
//
// FALLBACK AND HONESTY. Every reader here returns a miss rather than an
// error the caller must interpret, and every caller falls back to its
// existing HTTP path on a miss. But a stale CXO snapshot and a fresh
// HTTP fetch are not the same number, so each reader also returns a
// statsSource the page prints. Naming the source is the same rule as
// naming an absence: the page must never present a figure without
// saying where it came from.
package rewardsserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdapi "github.com/skycoin/skywire/pkg/deployment/tpd/api"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/logging"
)

// statsCXO holds the process-wide subscription manager. Set once from
// serveStandalone; nil in the embedded/test contexts where the reward
// handler is mounted without a dmsg client of its own, in which case
// every reader below misses and the pages use HTTP exactly as before.
var statsCXO struct {
	sync.RWMutex
	mgr *cxosub.Manager
	// newMgr builds another manager over the same client and feeds, for a
	// feed read once and then dropped whole by Close.
	newMgr func() *cxosub.Manager
}

// setStatsCXOSubMgrFromDmsg wires the statistics pages' CXO subscriber
// over dmsgC — the reward server's OWN client. A no-op when dmsgC is
// nil.
//
// Modeled on pkg/tpviz.SetCXOSubMgrFromDmsg, which does the same job
// for the embedded tp-viz server in this same process. The two hold
// separate managers because they subscribe to different feed sets and
// acquire/release on different schedules; they share the one dmsg
// identity, which is what matters.
func setStatsCXOSubMgrFromDmsg(dmsgC *dmsg.Client) {
	if dmsgC == nil {
		return
	}
	tpd := statsCXOPeer(deployment.Prod.TransportDiscovery)
	if (tpd == cipher.PubKey{}) {
		tpd = statsCXOPeer(deployment.Prod.TransportDiscoveryDmsg)
	}

	feedSpec := func(f cxosub.Feed) (cipher.PubKey, uint16, string, error) {
		port, prefix, ok := cxosub.FeedRoute(f)
		if !ok {
			return cipher.PubKey{}, 0, "", fmt.Errorf("unknown feed %d", f)
		}
		switch f {
		case cxosub.FeedTPDStats, cxosub.FeedTPDMetrics, cxosub.FeedTPDUptime, cxosub.FeedTPDVisorBW:
			if (tpd == cipher.PubKey{}) {
				return cipher.PubKey{}, 0, "", fmt.Errorf("no TPD publisher PK (dmsg service URL unset)")
			}
			return tpd, port, prefix, nil
		}
		// The statistics pages read no other feed; refusing here keeps a
		// stray AcquireFor from opening a subscription nothing consumes.
		return cipher.PubKey{}, 0, "", fmt.Errorf("feed %d not used by the statistics pages", f)
	}

	newMgr := func() *cxosub.Manager {
		return cxosub.NewManager(cxosub.Deps{
			Dmsg:     func() *dmsg.Client { return dmsgC },
			FeedSpec: feedSpec,
			Log:      logging.MustGetLogger("reward-stats-cxosub"),
		}, 0) // 0 → default cycle interval
	}
	mgr := newMgr()

	// Pin the small feeds for the process lifetime. Without a pin the ~10s
	// grace teardown closes each subscription as soon as the page that
	// read it finishes, and the NEXT page load pays a cold first sync.
	// The per-transport metrics feed is NOT pinned: it is hundreds of
	// megabytes, and the pages read a digest of it (metricsdigest.go).
	mgr.Pin(cxosub.FeedTPDStats)
	// The uptime feed too: the visors-online plot and the per-visor
	// timeline panel both read the tracker's v3 dump, which was being
	// pulled by running `skywire cli ut tpd graph --json` as a subprocess
	// every ten minutes. Pinning it makes that a memory read of a feed
	// this process already keeps warm.
	mgr.Pin(cxosub.FeedTPDUptime)

	statsCXO.Lock()
	statsCXO.mgr = mgr
	statsCXO.newMgr = newMgr
	statsCXO.Unlock()
}

// closeStatsCXO closes the manager, when the server that set it stops.
func closeStatsCXO() {
	statsCXO.Lock()
	mgr := statsCXO.mgr
	statsCXO.mgr = nil
	statsCXO.newMgr = nil
	statsCXO.Unlock()
	if mgr != nil {
		mgr.Close()
	}
}

// statsCXOMgr returns the manager, or nil when CXO is not wired.
func statsCXOMgr() *cxosub.Manager {
	statsCXO.RLock()
	defer statsCXO.RUnlock()
	return statsCXO.mgr
}

// statsCXOPeer extracts the publisher PK from a dmsg://<pk>:<port>
// service URL, or the zero PubKey when raw is empty or not a dmsg URL.
func statsCXOPeer(raw string) cipher.PubKey {
	if raw == "" {
		return cipher.PubKey{}
	}
	var u dmsgcurl.URL
	if err := u.Fill(raw); err != nil || u.Scheme != "dmsg" {
		return cipher.PubKey{}
	}
	return u.Addr.PK
}

// statsSource names where a rendered figure came from. A page that
// prints a number without this is asserting a freshness it does not
// know: a CXO snapshot can be minutes old while an HTTP fetch is by
// definition current, and the two must not read identically.
type statsSource struct {
	// Via is "CXO" or "HTTP over dmsg".
	Via string
	// Path is the CXO leaf or the HTTP endpoint the figure came from.
	Path string
	// At is when the CXO snapshot's root landed. Zero for HTTP, whose
	// answer is as of the request.
	At time.Time
	// Note carries the feed's own completeness caveat, or the CXO miss
	// that sent the page to HTTP. Never empty when it applies.
	Note string
}

// String renders the source as one line for a panel footer.
func (s statsSource) String() string {
	if s.Via == "" {
		return ""
	}
	out := "source: " + s.Via
	if s.Path != "" {
		out += " " + s.Path
	}
	if !s.At.IsZero() {
		out += fmt.Sprintf(" — snapshot %s old", statsAgeString(time.Since(s.At)))
	}
	if s.Note != "" {
		out += " — " + s.Note
	}
	return out
}

// statsAgeString formats a snapshot age at the resolution a reader
// cares about. Sub-minute ages are the normal case for the stats feed
// and rounding them to "0m" would hide the distinction the label exists
// to draw.
func statsAgeString(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.1fh", d.Hours())
	}
}

// cachedSourceLabel marks a figure the page is replaying from its own disk
// cache. The stored label describes how the body was ORIGINALLY obtained and
// would otherwise read as current — the cache is the third source, and it has
// to name itself like the other two. A cache written before sources were
// recorded carries none, and gets the age alone rather than a dangling dash.
func cachedSourceLabel(age, stored string) string {
	if stored == "" {
		return "page cache, " + age + " old"
	}
	return "page cache, " + age + " old — " + stored
}

// httpStatsSource labels a figure that came from HTTP because CXO had
// nothing, carrying the reason so the page can say why.
func httpStatsSource(endpoint, whyCXOMissed string) statsSource {
	s := statsSource{Via: "HTTP over dmsg", Path: endpoint}
	if whyCXOMissed != "" {
		s.Note = "CXO miss: " + whyCXOMissed
	}
	return s
}

// completenessNote turns a published completeness stamp into the caveat
// a panel prints. An incomplete sample is still shown — a feed that
// froze on real news would be worse — but never without saying so.
func completenessNote(complete bool, confidence string) string {
	if complete {
		return ""
	}
	if confidence == "" {
		confidence = "unknown"
	}
	return "INCOMPLETE sample (" + confidence + "): treat counts as a lower bound"
}

// statsCXOLeaf reads one leaf off the tpd-stats feed, waiting out the
// first sync on a cold cache.
//
// The wait is not optional. AcquireFor only STARTS the fill; returning
// a miss immediately would also release the reference, and the grace
// teardown then closes the subscriber mid-fill — so a feed whose first
// sync outlasts one call would never become readable however often the
// page is refreshed. Hence WaitForFirstSync while the reference is
// still held (see #4525).
func statsCXOLeaf(path string) ([]byte, time.Time, error) {
	return statsCXOFeedLeaf(cxosub.FeedTPDStats, cxosub.TabNetworkStats, path)
}

// statsCXOFeedLeaf is statsCXOLeaf over an arbitrary feed. The uptime
// feed is read the same way and by the same rules; only the feed and the
// acquiring tab differ.
func statsCXOFeedLeaf(feed cxosub.Feed, tab cxosub.Tab, path string) ([]byte, time.Time, error) {
	mgr := statsCXOMgr()
	if mgr == nil {
		return nil, time.Time{}, fmt.Errorf("CXO not wired")
	}
	mgr.AcquireFor(tab)
	defer mgr.ReleaseFor(tab)

	if body, ts, ok := statsCXOGet(mgr, feed, path); ok {
		return body, ts, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), cxosub.FeedFirstSyncTimeout(feed))
	defer cancel()
	if !mgr.WaitForFirstSync(ctx, feed, cxosub.FeedFirstSyncTimeout(feed)) {
		return nil, time.Time{}, fmt.Errorf("feed not synced")
	}
	if body, ts, ok := statsCXOGet(mgr, feed, path); ok {
		return body, ts, nil
	}
	return nil, time.Time{}, fmt.Errorf("no %s leaf on the feed", path)
}

// statsCXOGet reads and decompresses one leaf. Gunzip passes a raw body
// through unchanged, so this reads both the gzipped bodies the current
// publisher writes and any uncompressed ones an older one left.
func statsCXOGet(mgr *cxosub.Manager, feed cxosub.Feed, path string) ([]byte, time.Time, bool) {
	body, ts, ok := mgr.Get(feed, path)
	if !ok || len(body) == 0 {
		return nil, time.Time{}, false
	}
	decoded := cxoutils.Gunzip(body)
	if len(decoded) == 0 {
		return nil, time.Time{}, false
	}
	// Get lends the snapshot's bytes and Gunzip of a raw body returns
	// that same slice, so copy before handing it on.
	return append([]byte(nil), decoded...), ts, true
}

// cxoNetworkStats reads stats/network — the GET /all-transports/stats
// numbers. The body is validated by PARSING it, never by size: reads
// through dmsg have repeatedly arrived truncated under a 200, and a
// short body that unmarshals is a body that is whole.
func cxoNetworkStats() (*tpdapi.NetworkStats, statsSource, error) {
	body, ts, err := statsCXOLeaf(tpdapi.StatsPathNetwork)
	if err != nil {
		return nil, statsSource{}, err
	}
	var out tpdapi.NetworkStats
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, statsSource{}, fmt.Errorf("parse failed: %w", err)
	}
	return &out, statsSource{
		Via: "CXO", Path: tpdapi.StatsPathNetwork, At: ts,
		Note: completenessNote(out.Complete, out.Confidence),
	}, nil
}

// cxoVersionStats reads stats/versions — the GET /version histogram.
func cxoVersionStats() (*tpdapi.VersionStats, statsSource, error) {
	body, ts, err := statsCXOLeaf(tpdapi.StatsPathVersions)
	if err != nil {
		return nil, statsSource{}, err
	}
	var out tpdapi.VersionStats
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, statsSource{}, fmt.Errorf("parse failed: %w", err)
	}
	if len(out.Versions) == 0 {
		return nil, statsSource{}, fmt.Errorf("version histogram carried no builds")
	}
	return &out, statsSource{
		Via: "CXO", Path: tpdapi.StatsPathVersions, At: ts,
		Note: completenessNote(out.Complete, out.Confidence),
	}, nil
}

// cxoDailyStats reads stats/daily — the GET /metric daily aggregate.
// This is the leaf added alongside this change: the bandwidth- and
// latency-over-time charts had no feed at all and were the pages that
// failed most visibly.
func cxoDailyStats() (*tpdapi.DailyStats, statsSource, error) {
	body, ts, err := statsCXOLeaf(tpdapi.StatsPathDaily)
	if err != nil {
		return nil, statsSource{}, err
	}
	var out tpdapi.DailyStats
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, statsSource{}, fmt.Errorf("parse failed: %w", err)
	}
	if len(out.Daily) == 0 {
		return nil, statsSource{}, fmt.Errorf("daily aggregate carried no days")
	}
	return &out, statsSource{
		Via: "CXO", Path: tpdapi.StatsPathDaily, At: ts,
		Note: completenessNote(out.Complete, out.Confidence),
	}, nil
}

// cxoTransportMetrics assembles the newest `days` calendar-day leaves of
// TPD's per-transport metrics feed into the []TransportMetric shape the
// HTTP /metrics endpoint serves.
//
// Same assembly as the visor-side reader in
// pkg/visor/api_tpd_metrics_subscriber.go: a window is a JOIN, because
// one transport has a row in every day it moved bytes and the caller's
// record shape is one record per transport carrying N daily rows.
// MergeDailyMetrics walks newest-first so the current day's Live and
// Latency — the only leaf that carries them — win.
func cxoTransportMetrics(days int) ([]tpdstore.TransportMetric, statsSource, error) {
	if days < 1 {
		days = 1
	}
	var recs []tpdstore.TransportMetric
	var newest time.Time
	err := withMetricsFeed(cxosub.FeedFirstSyncTimeout(cxosub.FeedTPDMetrics), func(mgr *cxosub.Manager) (err error) {
		recs, newest, err = readCXOTransportMetrics(mgr, days)
		return err
	})
	if err != nil {
		return nil, statsSource{}, err
	}
	return recs, statsSource{Via: "CXO", Path: tpdstore.MetricsDayPrefix + "*", At: newest}, nil
}

// metricsFeedMu lets one fill of the metrics feed run at a time, so a page
// read during a digest build does not hold the feed a second time.
var metricsFeedMu sync.Mutex

// withMetricsFeed fills the metrics feed on a manager of its own, runs fn on
// it and closes the manager, which drops the whole snapshot. A closed feed's
// snapshot would otherwise stay referenced after its grace teardown.
func withMetricsFeed(wait time.Duration, fn func(*cxosub.Manager) error) error {
	statsCXO.RLock()
	newMgr := statsCXO.newMgr
	statsCXO.RUnlock()
	if newMgr == nil {
		return fmt.Errorf("CXO not wired")
	}
	if !metricsFeedMu.TryLock() {
		return fmt.Errorf("the metrics feed is being read")
	}
	defer metricsFeedMu.Unlock()
	mgr := newMgr()
	defer mgr.Close()
	mgr.Pin(cxosub.FeedTPDMetrics)
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	if !mgr.WaitForFirstSync(ctx, cxosub.FeedTPDMetrics, wait) {
		return fmt.Errorf("metrics feed not synced within %s", wait)
	}
	return fn(mgr)
}

// readCXOTransportMetrics does the assembly against a synced snapshot. A day
// too big for one CXO object arrives as several "<day>/part/<NNNN>" leaves,
// disjoint slices of one array, and a day with a malformed part is left out.
func readCXOTransportMetrics(mgr *cxosub.Manager, days int) ([]tpdstore.TransportMetric, time.Time, error) {
	byDate, dates, newest, err := metricsDayBodies(mgr)
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(dates) > days {
		dates = dates[:days]
	}
	perDay := make([][]tpdstore.TransportMetric, 0, len(dates))
	for _, date := range dates {
		if recs, ok := decodeMetricsDay(byDate[date]); ok {
			perDay = append(perDay, recs)
		}
	}
	if len(perDay) == 0 {
		return nil, time.Time{}, fmt.Errorf("no metrics day leaf parsed")
	}
	return tpdstore.MergeDailyMetrics(perDay), newest, nil
}
