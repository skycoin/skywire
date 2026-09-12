// Package visor pkg/visor/api_tpd_stats_subscriber.go c3-vis-core
//
// Reader-side helper for TPD's network-aggregate stats feed. The
// publisher lives in pkg/deployment/tpd/api/cxo_stats_publisher.go and
// writes three gzipped leaves:
//
//	stats/network   the GET /all-transports/stats shape
//	stats/versions  the GET /version fleet histogram
//	stats/daily     the GET /metric daily aggregate (~2.7 KB)
//
// Both carry a completeness stamp (observed_at / complete / confidence
// / trailing peak) that the reader passes through untouched — deciding
// whether a partial sample is usable is the consumer's call, not the
// visor's, and stripping the stamp here would recreate exactly the
// blind spot skycoin/skywire#4513 describes.
//
// The subscription itself lives in the unified CXOSubscriptionManager;
// this is a thin facade that AcquireFor's TabNetworkStats and serves
// the cached blob.
package visor

import (
	"context"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdapi "github.com/skycoin/skywire/pkg/deployment/tpd/api"
)

// ErrTPDStatsNotReady is returned when the CXO subscriber has nothing
// for the requested path yet (no manager, no TPD PK, hasn't synced).
// Callers treat it as a cache miss and fall through to HTTP.
var ErrTPDStatsNotReady = errors.New("tpd stats: cxo cache miss")

// StatsKindNetwork, StatsKindVersions and StatsKindDaily are the short
// path names the RPC/CLI layer uses, mirroring tpd-all-transports'
// with-self / without-self.
const (
	StatsKindNetwork  = "network"
	StatsKindVersions = "versions"
	// StatsKindDaily is the GET /metric daily aggregate — per-day
	// bandwidth, latency and by-type breakdown over the published
	// window, newest day first.
	StatsKindDaily = "daily"
)

// statsPathForKind maps a short kind to the publisher's TreeStore path.
// ok is false for anything else, which the caller reports as an invalid
// path rather than syncing a feed for a leaf that cannot exist.
func statsPathForKind(kind string) (string, bool) {
	switch kind {
	case StatsKindNetwork:
		return tpdapi.StatsPathNetwork, true
	case StatsKindVersions:
		return tpdapi.StatsPathVersions, true
	case StatsKindDaily:
		return tpdapi.StatsPathDaily, true
	}
	return "", false
}

// FetchTPDStatsCXO returns the JSON body for one aggregate — (bytes,
// lastRootAt, nil) on a hit, (nil, zero, ErrTPDStatsNotReady) when the
// feed has nothing to serve it from.
//
// The publisher republishes every ~12s, so lastRootAt doubles as a
// liveness signal: a root much older than that means the subscription,
// not the network, went quiet.
func (v *Visor) FetchTPDStatsCXO(kind string) ([]byte, time.Time, error) {
	path, ok := statsPathForKind(kind)
	if !ok {
		return nil, time.Time{}, ErrTPDStatsNotReady
	}
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, time.Time{}, ErrTPDStatsNotReady
	}
	mgr.AcquireFor(TabNetworkStats)
	defer mgr.ReleaseFor(TabNetworkStats)

	// A leaf that is present AND fresh is served straight from the snapshot.
	// Freshness is checked, not assumed: the cycle stops between calls, so a
	// leaf can sit here aging indefinitely, and serving it unconditionally is
	// what made this path disagree with the live endpoint it mirrors.
	maxAge := statsMaxAge(kind)
	if body, ts, ok := readStatsLeaf(mgr, path); ok && time.Since(ts) <= maxAge {
		return body, ts, nil
	}
	// Cold or stale snapshot: AcquireFor only started the cycle. Block briefly
	// for the sync (over dmsg) so CXO serves this call instead of the caller
	// falling back to dmsg-http. These are sub-kilobyte leaves, so the short
	// FirstSyncTimeout applies — no large-feed budget for this feed.
	ctx, cancel := context.WithTimeout(context.Background(), feedFirstSyncTimeout(FeedTPDStats))
	_, _ = mgr.RefreshNow(ctx, FeedTPDStats) //nolint:errcheck
	cancel()
	if body, ts, ok := readStatsLeaf(mgr, path); ok && time.Since(ts) <= maxAge {
		return body, ts, nil
	}
	// Still stale after a resync: report a miss so the caller's chain falls
	// through to the live service rather than being handed a number that is
	// quietly wrong. A stale aggregate is worse than a slower correct one —
	// nothing downstream can tell it apart from a current reading.
	return nil, time.Time{}, ErrTPDStatsNotReady
}

// statsSnapshot is the slice of the CXO subscription manager
// readStatsLeaf needs. An interface so the read path is unit-testable
// against a plain map instead of a live DMSG subscription.
type statsSnapshot interface {
	Get(feed CXOFeed, path string) ([]byte, time.Time, bool)
}

// readStatsLeaf serves one leaf, decompressing it. Gunzip passes a raw
// body through unchanged, so this reads both the gzipped bodies the
// publisher writes and any uncompressed ones an older publisher left.
func readStatsLeaf(mgr statsSnapshot, path string) ([]byte, time.Time, bool) {
	body, ts, ok := mgr.Get(FeedTPDStats, path)
	if !ok || len(body) == 0 {
		return nil, time.Time{}, false
	}
	decoded := cxoutils.Gunzip(body)
	if len(decoded) == 0 {
		return nil, time.Time{}, false
	}
	// Walk/Get lend the snapshot's bytes and Gunzip of a raw body returns
	// that same slice, so copy before handing it to an RPC marshaller.
	return append([]byte(nil), decoded...), ts, true
}

// statsMaxAge bounds how old a cached leaf may be before FetchTPDStatsCXO
// treats it as stale and forces a resync.
//
// AcquireFor/ReleaseFor bracket a single call, so the refcount drops back to
// zero the moment it returns and the cycle stops. The snapshot then sits in the
// manager's map and ages. Because the old read path only called RefreshNow when
// the leaf was ABSENT, a present-but-stale leaf was served forever: observed
// live as `cli tp net-stats` reporting a total 34% above what TPD itself
// returned, frozen at the same value for twenty minutes, while
// `cli svc tpd stats` (which does not take the CXO step) tracked the network.
// Two commands, one endpoint, two answers.
//
// The bounds follow the publisher's cadence
// (pkg/deployment/tpd/api/cxo_stats_publisher.go): network and versions are
// rewritten every ~12s, daily every ~5m. A few multiples of that is late enough
// to be unambiguously a stopped cycle rather than a slow one.
func statsMaxAge(kind string) time.Duration {
	if kind == StatsKindDaily {
		return 15 * time.Minute
	}
	return 90 * time.Second
}
