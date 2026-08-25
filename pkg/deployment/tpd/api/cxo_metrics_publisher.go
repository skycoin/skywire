// Package api pkg/deployment/tpd/api/cxo_metrics_publisher.go c4-net-discovery
//
// CXO publisher for the network-wide transport-metrics aggregate.
// On a fixed cadence (default 60s) the publisher recomputes the
// metrics for a small set of day windows (1, 7, 30) and writes the
// JSON-encoded []store.TransportMetric to a TreeStore path
//
//	metrics/days/<n>
//
// Subscribers (the hvui's Transports tab via the visor's CXO
// subscriber) watch the feed instead of HTTP-polling /metrics. CXO's
// content-addressing means the publisher only re-encodes subtrees
// that actually changed, so unchanged transport rows produce a
// stable Root and the wire footprint is the delta, not the full
// dataset.
package api

import (
	"context"
	"encoding/json"
	"fmt"
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
		m.log.WithError(err).WithField("days", days).Debug("metrics fetch failed; will retry next tick")
		m.recordError(err)
		return
	}
	body, err := json.Marshal(metrics)
	if err != nil {
		m.log.WithError(err).WithField("days", days).Warn("metrics marshal failed")
		m.recordError(err)
		return
	}
	path := metricsPath(days)
	if err := m.pub.Put(path, body); err != nil {
		m.log.WithError(err).WithField("path", path).Warn("publisher Put failed")
		m.recordError(err)
		return
	}
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
