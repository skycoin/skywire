// Package api pkg/deployment/tpd/api/cxo_metrics_publisher.go c4-net-discovery
//
// CXO publisher for the network-wide transport-metrics aggregate.
// The publisher writes ONE GZIPPED JSON LEAF PER CALENDAR DAY
//
//	metrics/day/<YYYY-MM-DD>
//
// and a day window (1, 7, 30) is assembled reader-side from the N
// newest leaves. See pkg/deployment/tpd/store/cxo_metrics_layout.go
// for the pivot/merge pair and for why Live and Latency live only in
// the newest day's leaf.
//
// This replaced three OVERLAPPING window leaves (metrics/days/1, /7,
// /30) recomputed on separate tickers. Two things were wrong with
// that:
//
//   - Content-addressing bought nothing. One leaf per window meant a
//     single changed byte made the whole multi-megabyte object new,
//     so the 30-day window went back over the wire in full every 30
//     minutes even though 29 of its 30 days could not have changed.
//     Per-day leaves make a settled day hash identically forever, so
//     only the current day actually moves.
//
//   - The recompute was redundant. Every 30 minutes the old scheme
//     asked the store for 30×1 + 6×7 + 1×30 = 102 transport-days;
//     this one asks for 30×1 + 1×30 = 60, because the short windows
//     are no longer separate queries at all.
//
// Two properties of CXO still shape how a body is written:
//
//   - CXO stores and propagates object bytes verbatim — it does not
//     compress. Bodies are gzipped here, as every sibling TPD
//     publisher already does. Readers use cxoutils.Gunzip, which
//     passes a raw body through unchanged.
//
//   - An object over skyobject MaxObjectSize (16 MB) is refused at
//     Put time, and one refused Put kills the whole feed: no Root is
//     ever published and every subscriber sits in "timeout waiting
//     for Root" forever. A single production day is ~16 MB of JSON
//     that gzips to ~4 MB, so a day fits comfortably — but the
//     part-splitting safety net is kept, at
//
//     metrics/day/<YYYY-MM-DD>/part/<NNNN>
//
//     because "it fits today" is not a guarantee about a larger
//     network tomorrow.
package api

import (
	"bytes"
	"compress/gzip"
	"context"
	jsoniter "github.com/json-iterator/go"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// json is jsoniter's fastest config. The metrics feed marshals one record per
// transport per day, and that marshal — not the compression — is where its
// allocations go; the stdlib encoder's reflection is the slower half of a
// publish measured at a third of this service's CPU (2026-09-11).
var fastJSON = jsoniter.ConfigFastest

const (
	// metricsWindowDays is how much history the feed carries. It is
	// the longest window the hvui's day selector offers, and it sits
	// inside the 35-day TTL on the bw:daily:* keys the store reads.
	metricsWindowDays = 30

	// metricsTick is the cadence of the current day's leaf. Each tick
	// re-encodes and re-gzips the whole day (~2.8 CPU-s on prod01), but a
	// transport's telemetry reaches the store at most once per 5 minutes
	// (cxoaggregator telemetryApplyEvery), so a faster tick mostly
	// republishes the same numbers.
	metricsTick = 5 * time.Minute

	// legacyMetricsPrefix is the sub-tree the pre-day-leaf publisher
	// wrote (metrics/days/<n>). Pruned once at startup so a feed
	// backed by a persistent DB cannot serve a stale window forever.
	legacyMetricsPrefix = "metrics/days"
)

// MetricsCXOPublisher periodically recomputes the /metrics aggregate
// and publishes it as one leaf per calendar day. The struct is owned
// by the API; Close shuts the publisher and stops the ticker.
type MetricsCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	// parts records, per published date, how many part leaves that day
	// was last written as (0 = a single leaf). The next cycle needs it
	// to retire leaves that are no longer produced. Only the publish
	// loop touches it.
	parts map[string]int

	// unsaved holds the last body published for each day still open; it is
	// saved to the store once the day settles. Only the publish loop touches
	// it.
	unsaved map[string][][]byte

	mu        sync.Mutex
	lastError error
}

// StartMetricsCXOPublisher constructs a publisher backed by the
// given DMSG client and TPD secret key, then kicks off the recompute
// ticker. The publisher's allowlist is left open (any subscriber may
// read) — the metrics endpoint is already a public read on the HTTP
// side, so the CXO mirror inherits that access policy.
//
// Returns nil + error when the publisher can't be created (no DMSG
// client, listener bind failure, etc.); the caller should log and
// continue without it. Treat the publisher as best-effort: the
// existing HTTP /metrics route stays the source of truth.
func StartMetricsCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*MetricsCXOPublisher, error) {
	log := logging.MustGetLogger("tpd-cxo-metrics-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true, // metrics are always recomputed from redis on the next tick
		DmsgPort:   skyenv.DmsgTPDMetricsCXOPort,
	})
	if err != nil {
		return nil, err
	}
	// nil allowlist = open feed (any subscriber accepted).
	pub.SetAllowlist(nil)

	pubCtx, cancel := context.WithCancel(ctx)
	mp := &MetricsCXOPublisher{
		api:    api,
		pub:    pub,
		log:    log,
		cancel: cancel,
		done:   make(chan struct{}),
		parts:  make(map[string]int),

		unsaved: make(map[string][][]byte),
	}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgTPDMetricsCXOPort).
			Info("CXO metrics publisher running")
	}
	go mp.loop(pubCtx)
	return mp, nil
}

// FeedPK returns the publisher's feed PK — i.e. TPD's own PK, since
// the publisher was built with TPD's secret key. Subscribers connect
// to this PK at port skyenv.DmsgTPDMetricsCXOPort.
func (m *MetricsCXOPublisher) FeedPK() cipher.PubKey { return m.pub.Feed() }

// Close stops the ticker and tears down the publisher.
func (m *MetricsCXOPublisher) Close() error {
	if m.cancel != nil {
		m.cancel()
	}
	<-m.done
	return m.pub.Close()
}

// loop drives both cadences from ONE goroutine. The three windows
// used to each have their own ticker; a single loop means the
// per-date bookkeeping below needs no lock and the full and current
// publishes can never interleave.
func (m *MetricsCXOPublisher) loop(ctx context.Context) {
	defer close(m.done)

	if err := m.pub.PrunePrefix(legacyMetricsPrefix); err != nil {
		m.log.WithError(err).Debug("could not prune the legacy metrics/days sub-tree")
	}

	// Publish the whole window once immediately so a subscriber that
	// connects shortly after TPD starts gets history without waiting.
	m.publish(ctx, true)

	t := time.NewTicker(metricsTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.publish(ctx, false)
		}
	}
}

// maxPublishBody bounds a single published body, measured on the GZIPPED bytes
// since those are what CXO stores. The margin below skyobject's 16 MB
// MaxObjectSize covers the encoding overhead CXO adds around the payload.
const maxPublishBody = 12 * 1024 * 1024

// publish writes the day leaves. A settled day never changes, so it is
// computed once — by the full cycle at startup, or on its last day open —
// and never read from the store again: each tick reads only the open days
// (store.OpenMetricsDays: today, and yesterday for a few minutes after
// UTC midnight) and retires days that left the window. The whole window
// used to be re-read every 30 minutes — one HGETALL per transport per day,
// over ~180k transports — to re-encode 29 leaves to the bytes they held.
func (m *MetricsCXOPublisher) publish(ctx context.Context, full bool) {
	now := time.Now().UTC()
	window := store.MetricsWindowDates(now, metricsWindowDays)
	open := store.OpenMetricsDays(now)
	days := open
	saved := map[string][][]byte{}
	if full {
		// Settled days an earlier run saved need no recompute; only when one
		// is missing is the whole window read.
		saved = m.loadSettled(ctx, window[open:])
		if len(saved) < len(window)-open {
			days = metricsWindowDays
			saved = map[string][][]byte{}
		}
	}
	metrics, err := m.fetch(ctx, days)
	if err != nil {
		// WARN, not DEBUG. A cycle that fails every tick makes the whole feed
		// unusable, and at DEBUG that is invisible on a production deployment —
		// which is exactly how this went unnoticed.
		m.log.WithError(err).WithField("days", days).Warn("metrics fetch failed; will retry next tick")
		m.recordError(err)
		return
	}

	fresh := window[:days]
	byDate := store.PivotDailyMetrics(metrics, fresh)
	if days > open {
		perDay := make(map[string]int, len(fresh))
		for _, d := range fresh {
			perDay[d] = len(byDate[d])
		}
		m.log.WithField("transports", len(metrics)).WithField("per_day", perDay).Info("Rebuilt the metrics window")
	}

	bodies := make(map[string][][]byte, len(fresh))
	for _, date := range fresh {
		parts, gerr := gzipParts(byDate[date], maxPublishBody)
		if gerr != nil {
			m.log.WithError(gerr).WithField("date", date).Warn("metrics marshal failed")
			m.recordError(gerr)
			return
		}
		bodies[date] = parts
	}
	for date, parts := range saved {
		bodies[date] = parts
	}

	// The whole window, so settled days are kept (planDayOps leaves a date
	// with no body untouched) and days that left it are retired.
	ops, next := planDayOps(bodies, window, m.parts, true)
	if err := m.pub.PutBatch(ops); err != nil {
		m.log.WithError(err).Warn("publisher PutBatch failed")
		m.recordError(err)
		return
	}
	m.parts = next
	m.settle(ctx, bodies, window[:open])
}

// planDayOps turns one cycle's gzipped bodies into the PutBatch that
// realizes them, and returns the per-date part counts the next cycle
// should compare against.
//
// Deletes are emitted before puts because PutBatch applies ops IN
// ORDER and a path cannot be a leaf and a sub-tree at once: a day
// that switches between the single-leaf and the split form has to
// retire the old form first or the put fails with ErrPathConflict.
//
// prune=true additionally retires day leaves that have fallen out of
// the window. Without it a rolling window grows a leaf a day forever,
// and a prefix Walk keeps serving days the store no longer has data
// for.
func planDayOps(bodies map[string][][]byte, dates []string, prev map[string]int, prune bool) (ops []treestore.PutOp, next map[string]int) {
	next = make(map[string]int, len(prev)+len(dates))
	for d, n := range prev {
		next[d] = n
	}

	inWindow := make(map[string]bool, len(dates))
	var deletes, puts []treestore.PutOp
	for _, date := range dates {
		inWindow[date] = true
		bs := bodies[date]
		if len(bs) == 0 {
			continue
		}
		base := store.MetricsDayPath(date)
		staleFrom := len(bs)
		if len(bs) == 1 {
			puts = append(puts, treestore.PutOp{Path: base, Value: bs[0]})
			staleFrom = 0 // every part path from a previous split is now stale
		} else {
			deletes = append(deletes, treestore.PutOp{Path: base})
			for i, b := range bs {
				puts = append(puts, treestore.PutOp{Path: store.MetricsDayPartPath(date, i), Value: b})
			}
		}
		// A day that splits into fewer parts than last time leaves the
		// leftover high-index leaves behind; a reader stitching by
		// prefix would then serve records that no longer exist.
		for i := staleFrom; i < prev[date]; i++ {
			deletes = append(deletes, treestore.PutOp{Path: store.MetricsDayPartPath(date, i)})
		}
		if len(bs) > 1 {
			next[date] = len(bs)
		} else {
			next[date] = 0
		}
	}

	if prune {
		for date, n := range next {
			if inWindow[date] {
				continue
			}
			if n == 0 {
				deletes = append(deletes, treestore.PutOp{Path: store.MetricsDayPath(date)})
			}
			for i := 0; i < n; i++ {
				deletes = append(deletes, treestore.PutOp{Path: store.MetricsDayPartPath(date, i)})
			}
			delete(next, date)
		}
		// Retiring days must be deterministic too: a map iteration
		// order in the batch would make the same cycle emit different
		// op sequences on different runs, which is untestable.
		sortOpsByPath(deletes)
	}

	return append(deletes, puts...), next
}

func sortOpsByPath(ops []treestore.PutOp) {
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
}

// gzipParts encodes one day's records as one or more gzipped JSON arrays, each
// at most max bytes. One part is the normal case; a day only splits when even
// compressed it will not fit in a single CXO object.
func gzipParts(metrics []store.TransportMetric, max int) ([][]byte, error) {
	gz, err := gzipRecords(metrics)
	if err != nil {
		return nil, err
	}
	if len(gz) <= max {
		return [][]byte{gz}, nil
	}

	// Size the split from the COMPRESSED total against a target derived from
	// max, since gzipped bytes are what CXO stores and what the cap applies
	// to. Dividing the raw size by a fixed byte budget instead over-splits by
	// the compression ratio: on production data that turned a 7-day window
	// into 15 parts where four would fit, and every surplus leaf is another
	// round trip for a subscriber filling the tree. Aiming below max leaves
	// room for a part that compresses worse than the window average.
	target := max * 3 / 4
	if target < 1 {
		target = 1 // a pathologically small cap must not divide by zero
	}
	var out [][]byte
	n := len(gz)/target + 1
	for _, part := range splitEvenly(metrics, n) {
		if err := appendGzipped(&out, part, max); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// appendGzipped gzips part, halving it until every piece fits in max. A single
// record that will not fit on its own is appended anyway rather than silently
// dropped — the Put then fails loudly, which is the honest outcome.
func appendGzipped(out *[][]byte, part []store.TransportMetric, max int) error {
	if len(part) == 0 {
		return nil
	}
	gz, err := gzipRecords(part)
	if err != nil {
		return err
	}
	if len(gz) <= max || len(part) == 1 {
		*out = append(*out, gz)
		return nil
	}
	mid := len(part) / 2
	if err := appendGzipped(out, part[:mid], max); err != nil {
		return err
	}
	return appendGzipped(out, part[mid:], max)
}

func splitEvenly(metrics []store.TransportMetric, n int) [][]store.TransportMetric {
	if len(metrics) == 0 {
		return nil
	}
	if n < 1 {
		n = 1
	}
	if n > len(metrics) {
		n = len(metrics)
	}
	size := (len(metrics) + n - 1) / n
	out := make([][]store.TransportMetric, 0, n)
	for i := 0; i < len(metrics); i += size {
		end := i + size
		if end > len(metrics) {
			end = len(metrics)
		}
		out = append(out, metrics[i:end])
	}
	return out
}

func (m *MetricsCXOPublisher) recordError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = err
}

// LastError returns the most recent error encountered by the publish
// loop, or nil if the last tick succeeded. Exposed for /health-style
// introspection if a future caller wants it.
func (m *MetricsCXOPublisher) LastError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastError
}

// gzipRecords is the gzipped JSON array of metrics, streamed one record at a
// time straight into the compressor. The output is byte-identical to
// gzip(json.Marshal(metrics)) — "[]" for an empty or nil slice — but the whole
// JSON body is never held: a day of metrics marshaled to ~130 MB before
// compression and was built twice (whole, then per part), which is what pushed
// the TPD's heap to multi-GB peaks every minute (2026-09-10 heap profile).
func gzipRecords(metrics []store.TransportMetric) ([]byte, error) {
	var buf bytes.Buffer
	// BestSpeed, not the default level 6. This body is rebuilt and republished
	// every tick and the publisher was measured at a third of transport-
	// discovery's CPU with two thirds of that inside deflate (2026-09-11).
	// The feed is machine-read from CXO, where a few percent of size buys
	// back most of that CPU, and the bodies are far below the split ceiling
	// either way so nothing gains a part.
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write([]byte{'['}); err != nil {
		return nil, err
	}
	for i := range metrics {
		if i > 0 {
			if _, err := zw.Write([]byte{','}); err != nil {
				return nil, err
			}
		}
		b, err := fastJSON.Marshal(&metrics[i])
		if err != nil {
			return nil, err
		}
		if _, err := zw.Write(b); err != nil {
			return nil, err
		}
	}
	if _, err := zw.Write([]byte{']'}); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Publisher returns the underlying feed publisher, for introspection.
func (x *MetricsCXOPublisher) Publisher() *treestore.Publisher { return x.pub }

// todayStore is the store's delta read of today's metrics
// (store.GetTodayTransportMetrics): only the transports whose figures moved
// are re-read. A store without it is read whole.
type todayStore interface {
	GetTodayTransportMetrics(ctx context.Context) ([]store.TransportMetric, error)
}

// leafStore keeps settled days' leaves across restarts
// (store.SaveMetricsLeaf / LoadMetricsLeaves).
type leafStore interface {
	SaveMetricsLeaf(ctx context.Context, date string, parts [][]byte) error
	LoadMetricsLeaves(ctx context.Context, dates []string) (map[string][][]byte, error)
}

// fetch reads the given number of most recent days.
func (m *MetricsCXOPublisher) fetch(ctx context.Context, days int) ([]store.TransportMetric, error) {
	if days == 1 {
		if ts, ok := m.api.store.(todayStore); ok {
			return ts.GetTodayTransportMetrics(ctx)
		}
	}
	return m.api.store.GetAllTransportMetrics(ctx, store.MetricsQuery{
		Days:      days,
		Live:      "all",
		Edges:     true,
		Bandwidth: true,
		Latency:   true,
	})
}

// loadSettled returns the saved leaves of the given settled dates.
func (m *MetricsCXOPublisher) loadSettled(ctx context.Context, dates []string) map[string][][]byte {
	ls, ok := m.api.store.(leafStore)
	if !ok || len(dates) == 0 {
		return map[string][][]byte{}
	}
	got, err := ls.LoadMetricsLeaves(ctx, dates)
	if err != nil {
		m.log.WithError(err).Debug("could not load saved metrics leaves; rebuilding the window")
		return map[string][][]byte{}
	}
	for date, parts := range got {
		if emptyLeaf(parts) {
			m.log.WithField("date", date).Warn("saved metrics day holds no transports; rebuilding it")
			delete(got, date)
		}
	}
	return got
}

// emptyLeaf reports whether a day's parts hold no transport, or do not decode.
// The network never has a day without traffic, so such a leaf was built from a
// read that missed the day's rows.
func emptyLeaf(parts [][]byte) bool {
	records, err := decodeMetricsParts(parts)
	return err != nil || len(records) == 0
}

// settle remembers the bodies of days still open and saves every day that
// has settled since it was last published. open is the dates still open now.
func (m *MetricsCXOPublisher) settle(ctx context.Context, bodies map[string][][]byte, open []string) {
	isOpen := make(map[string]bool, len(open))
	for _, d := range open {
		isOpen[d] = true
	}
	for date, parts := range bodies {
		m.unsaved[date] = parts
	}
	ls, ok := m.api.store.(leafStore)
	for date, parts := range m.unsaved {
		if isOpen[date] {
			continue
		}
		if emptyLeaf(parts) {
			// Not saved, so the next start rebuilds the day from its rows.
			m.log.WithField("date", date).Warn("settled metrics day holds no transports; not saving it")
			delete(m.unsaved, date)
			continue
		}
		if ok {
			if err := ls.SaveMetricsLeaf(ctx, date, parts); err != nil {
				m.log.WithError(err).WithField("date", date).Debug("could not save settled metrics leaf")
				continue // try again next tick
			}
		}
		delete(m.unsaved, date)
	}
}
