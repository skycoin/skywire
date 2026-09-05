// Package visor pkg/visor/api_tpd_metrics_subscriber.go c3-vis-core
//
// Reader-side helper for TPD's network-wide transport metrics feed.
// The CXO subscription lives in the unified CXOSubscriptionManager
// (see cxo_subscription_manager.go); this file is a thin facade that
// AcquireFor's the relevant tab and serves the cached blob.
//
// TPD publishes one leaf per calendar day at
// "metrics/day/<YYYY-MM-DD>", so a day window is assembled HERE from
// the N newest leaves — see the pivot/merge pair in
// pkg/deployment/tpd/store/cxo_metrics_layout.go. Callers still get
// the single JSON array of store.TransportMetric they always got.
//
// The reader also still understands the previous layout, one leaf
// per window at "metrics/days/<n>". TPD and visors update
// independently from the same develop-latest binary on a ~5 minute
// timer, so a new visor talking to a not-yet-updated TPD is a real
// state, and it is one visor-side branch rather than a second set of
// leaves TPD would have to keep publishing.
//
// Pre-task-#134 this file owned its own standalone Subscriber that
// bound DmsgTPDMetricsCXOPort directly. That subscriber raced with
// the unified manager for the same DMSG port, causing one of them to
// fail with "dmsg listen on port 51: port already occupied". The
// duplication is gone now — there's a single subscriber per CXO
// port, owned by the manager.
package visor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// ErrTPDMetricsNotReady is returned by FetchTransportMetricsCXO when
// the local manager has no snapshot for the requested day window yet
// (manager not initialized, first cycle hasn't completed, or TPD
// hasn't published anything). Callers should fall back to the HTTP
// path on this error.
var ErrTPDMetricsNotReady = errors.New("tpd metrics: cxo cache miss")

// FetchTransportMetricsCXO returns the metrics blob for the given day
// window as a single JSON array of store.TransportMetric —
// (bytes, lastRootAt, nil) on a hit, (nil, zero,
// ErrTPDMetricsNotReady) when the feed has nothing to serve it from.
//
// `days` may be any positive count; it is satisfied from however many
// day leaves TPD is currently publishing (30 at present), so asking
// for more days than exist returns the days that do.
func (v *Visor) FetchTransportMetricsCXO(days int) ([]byte, time.Time, error) {
	if days < 1 {
		days = 1
	}
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}
	mgr.AcquireFor(TabMetrics)
	defer mgr.ReleaseFor(TabMetrics)

	body, ts, err := readTransportMetricsCXO(mgr, days)
	if err == nil {
		return body, ts, nil
	}

	// Cold cache: the first fill may still be in flight. Returning the miss
	// here would also release the reference, and the grace-period teardown
	// then closes the subscriber mid-fill — so a feed whose first sync takes
	// longer than one call never becomes readable, however often it is
	// retried. This feed is tens of megabytes across many leaves and takes
	// tens of seconds to fill; measured on production it delivers 7.1 MB
	// (20,493 records) once the reference is simply held open.
	//
	// The AcquireFor above stays held across the wait, which is what keeps
	// the cycle alive long enough to finish.
	if !mgr.WaitForFirstSync(context.Background(), FeedTPDMetrics, cxosub.FeedFirstSyncTimeout(FeedTPDMetrics)) {
		return nil, time.Time{}, err
	}
	return readTransportMetricsCXO(mgr, days)
}

// metricsSnapshot is the slice of CXOSubscriptionManager the assembly
// below reads. Named as an interface so the assembly is exercised by
// unit tests against a plain map instead of a live DMSG subscription.
type metricsSnapshot interface {
	Get(feed CXOFeed, path string) ([]byte, time.Time, bool)
	SyncedAt(feed CXOFeed, path string) (time.Time, bool)
	Walk(feed CXOFeed, prefix string, fn func(path string, body []byte) bool) bool
}

func readTransportMetricsCXO(mgr metricsSnapshot, days int) ([]byte, time.Time, error) {
	if body, ts, err := assembleTransportMetricDays(mgr, days); err == nil {
		return body, ts, nil
	}
	// Fall back to the pre-day-leaf layout, which a TPD that has not
	// updated yet is still publishing.
	return fetchLegacyMetricsWindow(mgr, days)
}

// assembleTransportMetricDays merges the N newest day leaves into one
// window.
//
// The window is decoded and re-encoded rather than spliced at the
// byte level, because a window is a JOIN: the same transport has a
// row in every day it moved bytes, and the caller's record shape is
// one record per transport carrying N daily rows. A one-day window
// skips that entirely and passes the leaf's bytes straight through,
// which is the case pkg/tpviz and the hvui's default view take.
func assembleTransportMetricDays(mgr metricsSnapshot, days int) ([]byte, time.Time, error) {
	dates := transportMetricDates(mgr)
	if len(dates) == 0 {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}
	if len(dates) > days {
		dates = dates[:days]
	}

	var newest time.Time
	perDay := make([][]tpdstore.TransportMetric, 0, len(dates))
	for _, date := range dates {
		body, ts, err := dayLeafBody(mgr, date)
		if err != nil {
			continue
		}
		if ts.After(newest) {
			newest = ts
		}
		if len(dates) == 1 {
			return body, ts, nil
		}
		var recs []tpdstore.TransportMetric
		if err := json.Unmarshal(body, &recs); err != nil {
			return nil, time.Time{}, fmt.Errorf("%w: day %s: %v", ErrTPDMetricsNotReady, date, err)
		}
		perDay = append(perDay, recs)
	}
	if len(perDay) == 0 {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}

	merged, err := json.Marshal(tpdstore.MergeDailyMetrics(perDay))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: %v", ErrTPDMetricsNotReady, err)
	}
	return merged, newest, nil
}

// transportMetricDates returns the dates TPD currently has leaves
// for, NEWEST FIRST. It Walks paths only — the callback never touches
// a body, so enumerating 30 days costs a map scan, not 30 gunzips.
func transportMetricDates(mgr metricsSnapshot) []string {
	seen := make(map[string]struct{})
	mgr.Walk(FeedTPDMetrics, tpdstore.MetricsDayPrefix, func(p string, _ []byte) bool {
		if date, ok := tpdstore.MetricsDayDate(p); ok {
			seen[date] = struct{}{}
		}
		return true
	})
	if len(seen) == 0 {
		return nil
	}
	dates := make([]string, 0, len(seen))
	for d := range seen {
		dates = append(dates, d)
	}
	// The date format sorts lexically into chronological order.
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates
}

// dayLeafBody returns one day's decoded (gunzipped) JSON array,
// stitching the part leaves back together if the day was split.
func dayLeafBody(mgr metricsSnapshot, date string) ([]byte, time.Time, error) {
	base := tpdstore.MetricsDayPath(date)
	if body, ts, ok := mgr.Get(FeedTPDMetrics, base); ok && len(body) > 0 {
		// Gunzip passes a raw body through unchanged, so this reads both
		// the gzipped bodies the publisher writes and any uncompressed
		// ones still cached from an older TPD.
		return cxoutils.Gunzip(body), ts, nil
	}
	return joinTransportMetricParts(mgr, base)
}

// fetchLegacyMetricsWindow reads the one-leaf-per-window layout a TPD
// that predates the day leaves still publishes.
func fetchLegacyMetricsWindow(mgr metricsSnapshot, days int) ([]byte, time.Time, error) {
	path := fmt.Sprintf("metrics/days/%d", days)
	if body, ts, ok := mgr.Get(FeedTPDMetrics, path); ok && len(body) > 0 {
		return cxoutils.Gunzip(body), ts, nil
	}
	return joinTransportMetricParts(mgr, path)
}

// joinTransportMetricParts concatenates the part leaves under base into one
// JSON array. Parts are spliced as bytes rather than decoded and re-encoded:
// the parts of one leaf are disjoint slices of the same array, so no join is
// needed and these bodies run to megabytes.
func joinTransportMetricParts(mgr metricsSnapshot, base string) ([]byte, time.Time, error) {
	type part struct {
		path string
		body []byte
	}
	var parts []part
	var newest time.Time

	mgr.Walk(FeedTPDMetrics, base+"/part/", func(p string, body []byte) bool {
		if len(body) == 0 {
			return true
		}
		// Walk lends the snapshot's bytes; Gunzip of a raw body returns that
		// same slice, so copy before the callback returns.
		decoded := cxoutils.Gunzip(body)
		parts = append(parts, part{path: p, body: append([]byte(nil), decoded...)})
		if ts, ok := mgr.SyncedAt(FeedTPDMetrics, p); ok && ts.After(newest) {
			newest = ts
		}
		return true
	})
	if len(parts) == 0 {
		return nil, time.Time{}, ErrTPDMetricsNotReady
	}
	// Walk iterates a map, so order is arbitrary; the zero-padded part index
	// makes a lexical sort the publication order.
	sort.Slice(parts, func(i, j int) bool { return parts[i].path < parts[j].path })

	bodies := make([][]byte, len(parts))
	for i, p := range parts {
		bodies[i] = p.body
	}
	joined, err := spliceJSONArrays(bodies)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: %v", ErrTPDMetricsNotReady, err)
	}
	return joined, newest, nil
}

// spliceJSONArrays concatenates JSON arrays into one array at the byte level.
// Splicing a body that is not an array would produce silently malformed JSON,
// so that is an error rather than something the caller discovers downstream.
func spliceJSONArrays(bodies [][]byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	first := true
	for i, body := range bodies {
		inner := bytes.TrimSpace(body)
		if len(inner) < 2 || inner[0] != '[' || inner[len(inner)-1] != ']' {
			return nil, fmt.Errorf("part %d is not a JSON array", i)
		}
		if inner = bytes.TrimSpace(inner[1 : len(inner)-1]); len(inner) == 0 {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		buf.Write(inner)
		first = false
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}
