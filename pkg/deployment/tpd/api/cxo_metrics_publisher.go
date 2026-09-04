// Package api pkg/deployment/tpd/api/cxo_metrics_publisher.go c4-net-discovery
//
// CXO publisher for the network-wide transport-metrics aggregate.
// On a fixed cadence (default 60s) the publisher recomputes the
// metrics for a small set of day windows (1, 7, 30) and writes the
// gzipped JSON-encoded []store.TransportMetric to a TreeStore path
//
//	metrics/days/<n>
//
// Subscribers (the hvui's Transports tab via the visor's CXO
// subscriber) watch the feed instead of HTTP-polling /metrics.
//
// Two properties of CXO shape how the body is written:
//
//   - CXO stores and propagates object bytes verbatim — it does not
//     compress. Bodies are gzipped here, as every sibling TPD
//     publisher already does (see cxo_uptime_publisher.go and
//     cxo_all_transports_publisher.go). Readers use cxoutils.Gunzip,
//     which passes a raw body through unchanged.
//
//   - An object over skyobject MaxObjectSize (16 MB) is refused at
//     Put time, and refusing one window kills the whole feed: no
//     Root is ever published and every subscriber sits in "timeout
//     waiting for Root" forever. Measured on production, even the
//     1-day window is 16.02 MB of JSON — every window was over.
//     Gzip (~4x on this data) carries the short windows; long
//     windows are additionally split across
//
//     metrics/days/<n>/part/<NNNN>
//
//     leaves so no data is dropped. Readers stitch the parts back
//     into one array; the prefix Walk that pkg/tpviz already used
//     picks them up unchanged.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// metricsPublishWindow defines a CXO-published day-window and the
// cadence at which its body is recomputed. Heavy windows (7d, 30d)
// aggregate over long periods and barely change minute-to-minute, so
// they recompute less often than the 1d window — the previous
// uniform 60s tick across all three windows drove TPD into a GC
// storm (~70% of CPU in gcBgMarkWorker under prod load, since
// buildTransportMetrics walks all transports × days × per-edge
// fields each call). Staggering the heavy windows keeps subscribers
// fed without paying the full cost every minute.
type metricsPublishWindow struct {
	days     int
	interval time.Duration
}

// metricsPublishWindows is the set of day windows the publisher
// refreshes, each on its own ticker. The hvui picks one of these via
// the day selector; everything else falls through to the HTTP path.
// Cadence picks: 60s for the 1d window matches a typical hvui-open
// freshness expectation; 5m for the 7d window and 30m for the 30d
// window are short enough that an opening hvui sees recent data
// (well inside human-noticeable freshness on a long aggregate) and
// long enough that the buildTransportMetrics cost amortizes.
var metricsPublishWindows = []metricsPublishWindow{
	{days: 1, interval: 60 * time.Second},
	{days: 7, interval: 5 * time.Minute},
	{days: 30, interval: 30 * time.Minute},
}

// MetricsCXOPublisher periodically computes the /metrics aggregate
// for a fixed set of day windows and publishes each result as a
// JSON leaf at "metrics/days/<n>". The struct is owned by the API;
// Close shuts the publisher and stops the ticker.
type MetricsCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	lastError error
	// lastParts records how many part leaves each day window was last
	// published as (0 = a single leaf), so the next cycle knows which
	// leaves to retire.
	lastParts map[int]int
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
		api:       api,
		pub:       pub,
		log:       log,
		cancel:    cancel,
		done:      make(chan struct{}),
		lastParts: make(map[int]int),
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

func (m *MetricsCXOPublisher) loop(ctx context.Context) {
	defer close(m.done)

	var wg sync.WaitGroup
	for _, w := range metricsPublishWindows {
		wg.Add(1)
		go func(w metricsPublishWindow) {
			defer wg.Done()
			// Publish once immediately so a subscriber that connects
			// shortly after TPD starts gets a snapshot without waiting
			// a full tick.
			m.publishWindow(ctx, w.days)

			t := time.NewTicker(w.interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					m.publishWindow(ctx, w.days)
				}
			}
		}(w)
	}
	wg.Wait()
}

// maxPublishBody bounds a single published body, measured on the GZIPPED bytes
// since those are what CXO stores. The margin below skyobject's 16 MB
// MaxObjectSize covers the encoding overhead CXO adds around the payload.
const maxPublishBody = 12 * 1024 * 1024

func (m *MetricsCXOPublisher) publishWindow(ctx context.Context, days int) {
	query := store.MetricsQuery{
		Days:      days,
		Live:      "all",
		Edges:     true,
		Bandwidth: true,
		Latency:   true,
	}
	metrics, err := m.api.store.GetAllTransportMetrics(ctx, query)
	if err != nil {
		// WARN, not DEBUG. A window that fails every tick makes the whole feed
		// unusable, and at DEBUG that is invisible on a production deployment —
		// which is exactly how this went unnoticed.
		m.log.WithError(err).WithField("days", days).Warn("metrics fetch failed; will retry next tick")
		m.recordError(err)
		return
	}

	bodies, err := gzipParts(metrics, maxPublishBody)
	if err != nil {
		m.log.WithError(err).WithField("days", days).Warn("metrics marshal failed")
		m.recordError(err)
		return
	}

	base := metricsPath(days)
	m.mu.Lock()
	prevParts := m.lastParts[days]
	m.mu.Unlock()

	// Deletes are emitted before puts: PutBatch applies ops in order, and a
	// path cannot be a leaf and a sub-tree at once. Switching a window between
	// the two forms therefore has to retire the old form first or the put
	// fails with ErrPathConflict.
	var deletes, puts []treestore.PutOp
	staleFrom := len(bodies)
	if len(bodies) == 1 {
		puts = append(puts, treestore.PutOp{Path: base, Value: bodies[0]})
		staleFrom = 0 // every part path from a previous split is now stale
	} else {
		deletes = append(deletes, treestore.PutOp{Path: base})
		for i, b := range bodies {
			puts = append(puts, treestore.PutOp{Path: metricsPartPath(days, i), Value: b})
		}
	}
	// A shrinking network splits into fewer parts than last time. Without this
	// the leftover high-index leaves stay in the tree and a prefix Walk keeps
	// serving records that no longer exist.
	for i := staleFrom; i < prevParts; i++ {
		deletes = append(deletes, treestore.PutOp{Path: metricsPartPath(days, i)})
	}

	if err := m.pub.PutBatch(append(deletes, puts...)); err != nil {
		m.log.WithError(err).WithField("path", base).Warn("publisher PutBatch failed")
		m.recordError(err)
		return
	}

	nowParts := 0
	if len(bodies) > 1 {
		nowParts = len(bodies)
		m.log.WithField("days", days).WithField("parts", nowParts).
			Debug("metrics window exceeded the CXO object limit; published as parts")
	}
	m.mu.Lock()
	m.lastParts[days] = nowParts
	m.mu.Unlock()
}

// gzipParts encodes metrics as one or more gzipped JSON arrays, each at most
// max bytes. One part is the normal case; a window only splits when even
// compressed it will not fit in a single CXO object.
func gzipParts(metrics []store.TransportMetric, max int) ([][]byte, error) {
	whole, err := json.Marshal(metrics)
	if err != nil {
		return nil, err
	}
	gz := cxoutils.Gzip(whole)
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
	body, err := json.Marshal(part)
	if err != nil {
		return err
	}
	if gz := cxoutils.Gzip(body); len(gz) <= max || len(part) == 1 {
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
// loop, or nil if the last tick succeeded for every window. Exposed
// for /health-style introspection if a future caller wants it.
func (m *MetricsCXOPublisher) LastError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastError
}

// MetricsPath returns the TreeStore path the publisher writes to for
// a given day window. Exported so visor-side subscribers don't have
// to duplicate the format string.
func MetricsPath(days int) string { return metricsPath(days) }

func metricsPath(days int) string {
	return fmt.Sprintf("metrics/days/%d", days)
}

// MetricsPartPath returns the path of one part of a split window. Zero-padded
// so a reader that sorts the paths lexically gets them in publication order.
func MetricsPartPath(days, part int) string { return metricsPartPath(days, part) }

func metricsPartPath(days, part int) string {
	return fmt.Sprintf("%s/part/%04d", metricsPath(days), part)
}
