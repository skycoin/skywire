// Package api pkg/deployment/tpd/api/cxo_stats_publisher.go c4-net-discovery
//
// CXO publisher for TPD's NETWORK-AGGREGATE stats — the sub-kilobyte
// reductions that the bulk feeds are otherwise downloaded to compute.
//
//	stats/network   the GET /all-transports/stats shape (138 B live)
//	stats/versions  the GET /version fleet histogram (300 B live)
//	stats/daily     the GET /metric daily aggregate (~2.7 KB live)
//
// Why a separate feed rather than another path on an existing one: size
// is cadence. The metrics and all-transports feeds are staggered to
// minutes because recomputing them is expensive; the first two bodies
// are a few hundred bytes and cost a map walk over caches the HTTP
// handlers already read, so they republish every statsPublishInterval.
// A chart that wants "transports on the network, every 15 seconds"
// costs ~400 B per sample here instead of the 2.4 MB all-transports
// snapshot or the 16.8 MB metrics window it would otherwise reduce
// locally.
//
// stats/daily is the exception on cadence and the reason
// statsDailyInterval exists. Its BODY is small — the whole 30-day
// series with a per-type breakdown in under three kilobytes — but
// producing it is a 30-day store query, not a map walk, so it runs on
// its own slow timer inside the same loop rather than on the 12 s tick.
// It is here rather than on the metrics feed because a consumer that
// wants the reduction must not be made to subscribe to the ~120 MB of
// per-transport day leaves the reduction was computed from; that is the
// entire point of this feed.
//
// The per-key rollup (GET /all-transports/per-key-stats, ~38 KB gzipped)
// is deliberately NOT here. It is two orders of magnitude larger than
// these bodies and belongs on its own slower tier; putting it on this
// feed would drag the whole feed onto the large-feed first-sync budget
// and defeat the point of a feed a dashboard can hold continuously.
//
// PARTIAL-AGGREGATE HAZARD (skycoin/skywire#4513). TPD's transport
// aggregate is READABLE WHILE IT REFILLS after a restart: the count
// climbs monotonically from near zero to its settled value, every type
// diluted in proportion, with nothing in the body to say it is partial.
// TPD restarts on every deploy, so a naive publisher at this cadence
// would stamp a permanent sawtooth into every time series built on it.
// This publisher therefore judges each sample against a trailing peak
// (see completenessTracker) and:
//
//   - stamps every body with observed_at, complete, confidence and the
//     trailing peak it was judged against, so a consumer can filter;
//   - HOLDS the last complete sample on the feed rather than overwriting
//     it with one that looks partial, for up to statsIncompleteHoldover.
//
// The holdover bound rather than an indefinite hold is deliberate: a
// genuine network-wide drop below the trailing peak (a dmsg server
// outage, say) is indistinguishable from a refill at sample time, and a
// feed that silently freezes on real news is worse than one that
// publishes the news marked complete=false. After the bound the sample
// goes out with its honest verdict attached.
package api

import (
	"context"
	"encoding/json"
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

// Published paths. Exported so visor-side subscribers don't have to
// duplicate the strings.
const (
	// StatsPathNetwork carries the network-wide transport aggregate
	// (the GET /all-transports/stats shape plus a completeness stamp).
	StatsPathNetwork = "stats/network"
	// StatsPathVersions carries the fleet version histogram (the
	// GET /version shape plus a completeness stamp).
	StatsPathVersions = "stats/versions"
	// StatsPathDaily carries the network-wide daily aggregate (the
	// GET /metric shape plus a completeness stamp) — per-day
	// bandwidth, latency and by-type breakdown over statsDailyDays.
	StatsPathDaily = "stats/daily"
)

// statsPublishInterval is the recompute cadence. Both bodies are read
// off caches the HTTP handlers already serve from and reduce to a few
// hundred bytes, so unlike the metrics/uptime publishers (60s, because
// the recompute itself is the cost) this can run at time-series
// resolution. 12s keeps a chart live without making the tick a
// meaningful fraction of TPD's cache-refresh period.
const statsPublishInterval = 12 * time.Second

const (
	// statsDailyDays is the window stats/daily carries. It matches the
	// window the metrics feed publishes day leaves for, so the two
	// describe the same span of history.
	statsDailyDays = 30

	// statsDailyInterval is how often the daily aggregate is
	// recomputed. Deliberately far slower than statsPublishInterval:
	// unlike the other two paths this one is a 30-day store query
	// (GetNetworkMetrics pipelines one HGetAll per transport per day),
	// so the cost is the recompute, not the ~2.7 KB body. The figures
	// are calendar-day totals — they move slowly enough that five
	// minutes is indistinguishable from twelve seconds to any consumer,
	// and every consumer of this path already caches on top of it.
	statsDailyInterval = 5 * time.Minute
)

// Completeness-judgment tunables. See completenessTracker.
const (
	// statsTrailingWindow is how far back the peak a sample is judged
	// against is remembered. Wide enough to span several refill
	// sawteeth (#4513 observed a ~36 minute band of them) so a reset
	// does not reset the yardstick with it.
	statsTrailingWindow = 15 * time.Minute

	// statsCompleteRatio is the fraction of the trailing peak a sample
	// must reach to be called complete. #4513 measured the refill
	// passing through roughly half the settled value; 0.9 puts the
	// verdict well clear of ordinary churn (the settled value moved
	// ~1% between consecutive live samples) without demanding the peak
	// be re-attained exactly.
	statsCompleteRatio = 0.9

	// statsWarmup is how long after the publisher starts every sample
	// is reported complete=false regardless of value. The publisher
	// starts with TPD, so its first minutes ARE the refill; without
	// this the first sample would define the peak it is judged against
	// and a partial set would certify itself.
	statsWarmup = 2 * time.Minute

	// statsIncompleteHoldover bounds how long a last-known-complete
	// sample is held on the feed rather than being overwritten by one
	// that looks partial.
	statsIncompleteHoldover = 5 * time.Minute
)

// Confidence values stamped on a published body.
const (
	// ConfidenceSettled means the sample reached statsCompleteRatio of
	// the trailing peak — safe to chart as an absolute count.
	ConfidenceSettled = "settled"
	// ConfidenceRefilling means the sample fell materially below the
	// trailing peak. Under #4513 that is the signature of reading the
	// aggregate mid-rebuild; treat the value as a lower bound.
	ConfidenceRefilling = "refilling"
	// ConfidenceWarmup means the publisher has not observed long
	// enough to have a trustworthy peak to judge against.
	ConfidenceWarmup = "warmup"
)

// NetworkStats is the body published at StatsPathNetwork. The first
// three fields are store.TransportSummary verbatim — a consumer that
// unmarshals this into store.TransportSummary gets exactly what
// GET /all-transports/stats returns — and the rest is the completeness
// stamp #4513 makes necessary.
type NetworkStats struct {
	Total        int            `json:"total_transports"`
	ByType       map[string]int `json:"by_type"`
	UniqueVisors int            `json:"unique_visors"`

	ObservedAt time.Time `json:"observed_at"`
	Complete   bool      `json:"complete"`
	Confidence string    `json:"confidence"`
	// TrailingPeak is the highest total_transports seen in the last
	// TrailingWindowSeconds. Published so a consumer can apply its own
	// threshold instead of trusting ours.
	TrailingPeak          int `json:"trailing_peak_transports"`
	TrailingWindowSeconds int `json:"trailing_window_seconds"`
}

// VersionStats is the body published at StatsPathVersions. Versions is
// the GET /version body verbatim; the rest is the completeness stamp.
// The histogram is derived from the uptime cache, not the transport
// cache, so it carries its own independent verdict.
type VersionStats struct {
	Versions map[string]int `json:"versions"`
	Visors   int            `json:"visors"`

	ObservedAt time.Time `json:"observed_at"`
	Complete   bool      `json:"complete"`
	Confidence string    `json:"confidence"`
	// TrailingPeak is the highest visor count seen in the last
	// TrailingWindowSeconds.
	TrailingPeak          int `json:"trailing_peak_visors"`
	TrailingWindowSeconds int `json:"trailing_window_seconds"`
}

// DailyStats is the body published at StatsPathDaily. The first two
// fields are store.NetworkMetricResponse verbatim — a consumer that
// unmarshals this into store.NetworkMetricResponse gets exactly what
// GET /metric returns, newest day first — and the rest is the
// completeness stamp.
//
// The stamp is the TRANSPORT-SET verdict, not one computed from the
// bandwidth figures. These are sums over the same registry
// stats/network counts, so "the registry is still refilling" is
// precisely the thing that makes a day's total a lower bound; judging
// the sums themselves against a trailing peak would instead flag every
// ordinary quiet day as partial.
type DailyStats struct {
	Daily      []store.DailyAggregate     `json:"daily"`
	Cumulative *store.CumulativeAggregate `json:"cumulative"`

	// Days is the window requested from the store. TPD returns only the
	// history it holds, so len(Daily) may be shorter.
	Days int `json:"days"`

	ObservedAt time.Time `json:"observed_at"`
	Complete   bool      `json:"complete"`
	Confidence string    `json:"confidence"`
	// TrailingPeak is the transport-count peak the sample was judged
	// against — the same field stats/network publishes, repeated here
	// so this body stands on its own.
	TrailingPeak          int `json:"trailing_peak_transports"`
	TrailingWindowSeconds int `json:"trailing_window_seconds"`
}

// StatsCXOPublisher publishes the network-aggregate bodies, gating each
// against a completeness tracker. Closed by Close.
type StatsCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	// Trackers and holdover state are touched only from the publish
	// loop, which is single-goroutine; no lock needed for them.
	transports completenessTracker
	visors     completenessTracker
	heldSince  map[string]time.Time

	// lastTransportVerdict is the most recent transport-set judgment,
	// reused to stamp stats/daily (which is a reduction of that same
	// set) without observing a second, unrelated series.
	lastTransportVerdict completenessVerdict
	// lastDailyAt is when the slow stats/daily recompute was last
	// ATTEMPTED — set on attempt rather than on success so a store
	// error backs off to statsDailyInterval instead of retrying the
	// 30-day query on every 12 s tick.
	lastDailyAt time.Time

	// putFn writes one already-gzipped leaf. Always s.pub.Put in
	// production; a seam so the publish path (gzip, holdover, the set
	// of paths written) is exercised by unit tests without standing up
	// a DMSG client.
	putFn func(path string, body []byte) error

	mu        sync.Mutex
	lastError error
}

// StartStatsCXOPublisher constructs a publisher backed by the given
// DMSG client and TPD secret key, then kicks off the publish ticker.
// The allowlist is left open (any subscriber may read) — same access
// policy as the HTTP endpoints it mirrors.
//
// Best-effort: the HTTP routes stay the source of truth, so the caller
// should log and continue if this returns an error.
func StartStatsCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*StatsCXOPublisher, error) {
	log := logging.MustGetLogger("tpd-cxo-stats-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true, // recomputed from the in-RAM caches each tick
		DmsgPort:   skyenv.DmsgTPDStatsCXOPort,
	})
	if err != nil {
		return nil, err
	}
	// nil allowlist = open feed (any subscriber accepted).
	pub.SetAllowlist(nil)

	pubCtx, cancel := context.WithCancel(ctx)
	sp := &StatsCXOPublisher{
		api:        api,
		pub:        pub,
		log:        log,
		cancel:     cancel,
		done:       make(chan struct{}),
		transports: newCompletenessTracker(time.Now()),
		visors:     newCompletenessTracker(time.Now()),
		heldSince:  make(map[string]time.Time),
		// Until publishNetwork has observed anything, the honest verdict
		// for a body stamped from the transport set is "warmup", not the
		// zero value's empty confidence string.
		lastTransportVerdict: completenessVerdict{confidence: ConfidenceWarmup},
	}
	sp.putFn = pub.Put
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgTPDStatsCXOPort).
			Info("CXO stats publisher running")
	}
	go sp.loop(pubCtx)
	return sp, nil
}

// FeedPK returns the publisher's feed PK (TPD's own PK). Subscribers
// connect to this PK at port skyenv.DmsgTPDStatsCXOPort.
func (s *StatsCXOPublisher) FeedPK() cipher.PubKey { return s.pub.Feed() }

// Close stops the ticker and tears down the publisher.
func (s *StatsCXOPublisher) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	<-s.done
	return s.pub.Close()
}

func (s *StatsCXOPublisher) loop(ctx context.Context) {
	defer close(s.done)

	// Publish once immediately so a freshly-connected subscriber gets a
	// snapshot without waiting a full tick. It will be stamped
	// confidence=warmup, which is the honest answer this early.
	s.publishOnce(ctx)

	t := time.NewTicker(statsPublishInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.publishOnce(ctx)
		}
	}
}

// publishOnce writes one round. publishNetwork runs first because it
// sets the transport-set verdict publishDaily stamps its body with.
func (s *StatsCXOPublisher) publishOnce(ctx context.Context) {
	now := time.Now().UTC()
	s.publishNetwork(ctx, now)
	s.publishVersions(now)
	if s.lastDailyAt.IsZero() || now.Sub(s.lastDailyAt) >= statsDailyInterval {
		s.lastDailyAt = now
		s.publishDaily(ctx, now)
	}
}

// publishNetwork writes StatsPathNetwork. It reads the same warm cache
// GET /all-transports/stats counts off, falling back to the store's
// single-pass summary when the cache is cold — so the published numbers
// and the HTTP numbers come from one source, not two.
func (s *StatsCXOPublisher) publishNetwork(ctx context.Context, now time.Time) {
	summary := s.networkSummary(ctx)
	if summary == nil {
		return
	}
	verdict := s.transports.observe(now, summary.Total)
	s.lastTransportVerdict = verdict
	body := NetworkStats{
		Total:                 summary.Total,
		ByType:                summary.ByType,
		UniqueVisors:          summary.UniqueVisors,
		ObservedAt:            now,
		Complete:              verdict.complete,
		Confidence:            verdict.confidence,
		TrailingPeak:          verdict.peak,
		TrailingWindowSeconds: int(statsTrailingWindow / time.Second),
	}
	s.put(StatsPathNetwork, body, verdict.complete, now)
}

// networkSummary mirrors getAllTransportsStats' source selection: warm
// cache first, store summary on a cold cache. Self-transports are
// included, matching the endpoint's default.
func (s *StatsCXOPublisher) networkSummary(ctx context.Context) *store.TransportSummary {
	if entries := s.api.getTransportsFromCache(true); entries != nil {
		summary := &store.TransportSummary{ByType: make(map[string]int)}
		uniqueVisors := make(map[cipher.PubKey]struct{})
		for _, entry := range entries {
			summary.Total++
			summary.ByType[string(entry.Type)]++
			for _, edge := range entry.Edges {
				uniqueVisors[edge] = struct{}{}
			}
		}
		summary.UniqueVisors = len(uniqueVisors)
		return summary
	}
	summary, err := s.api.store.GetTransportSummary(ctx, true)
	if err != nil {
		s.log.WithError(err).Debug("network stats summary failed; will retry next tick")
		s.recordError(err)
		return nil
	}
	return summary
}

// publishVersions writes StatsPathVersions from the same uptime cache
// GET /version reads, with no online filter (the endpoint's default).
func (s *StatsCXOPublisher) publishVersions(now time.Time) {
	uptimes := s.api.getUptimesFromCache()
	versions := make(map[string]int, 32)
	for _, vs := range uptimes {
		version := vs.Version
		if version == "" {
			version = "unknown"
		}
		versions[version]++
	}
	verdict := s.visors.observe(now, len(uptimes))
	body := VersionStats{
		Versions:              versions,
		Visors:                len(uptimes),
		ObservedAt:            now,
		Complete:              verdict.complete,
		Confidence:            verdict.confidence,
		TrailingPeak:          verdict.peak,
		TrailingWindowSeconds: int(statsTrailingWindow / time.Second),
	}
	s.put(StatsPathVersions, body, verdict.complete, now)
}

// publishDaily writes StatsPathDaily from the same store call
// GET /metric serves, so the published series and the HTTP series are
// one number computed once, not two that can disagree.
//
// This is the read the charts on the reward server's /stats pages need
// and the one that had no feed: over HTTP it is a 2.7 KB body that
// still fails outright whenever the requesting dmsg client loses its
// sessions (skycoin/skywire#4538), because an HTTP fetch is
// all-or-nothing at request time while a subscriber reads a snapshot it
// already holds.
func (s *StatsCXOPublisher) publishDaily(ctx context.Context, now time.Time) {
	resp, err := s.api.store.GetNetworkMetrics(ctx, store.MetricsQuery{
		Days:      statsDailyDays,
		Live:      "all",
		Bandwidth: true,
		Latency:   true,
	})
	if err != nil {
		s.log.WithError(err).Debug("daily aggregate query failed; will retry next cycle")
		s.recordError(err)
		return
	}
	// An empty series is not news, it is a store that has not answered.
	// Publishing it would overwrite a good body with nothing.
	if resp == nil || len(resp.Daily) == 0 {
		return
	}
	verdict := s.lastTransportVerdict
	body := DailyStats{
		Daily:                 resp.Daily,
		Cumulative:            resp.Cumulative,
		Days:                  statsDailyDays,
		ObservedAt:            now,
		Complete:              verdict.complete,
		Confidence:            verdict.confidence,
		TrailingPeak:          verdict.peak,
		TrailingWindowSeconds: int(statsTrailingWindow / time.Second),
	}
	s.put(StatsPathDaily, body, verdict.complete, now)
}

// put gzips and writes one body, applying the incomplete-sample
// holdover: while a complete sample published within
// statsIncompleteHoldover still stands on the feed, an incomplete one
// is dropped rather than overwriting it. Past that bound the incomplete
// sample is published with its complete=false stamp, so a feed never
// freezes indefinitely on what might be real news.
func (s *StatsCXOPublisher) put(path string, body interface{}, complete bool, now time.Time) {
	if !complete {
		if held, ok := s.heldSince[path]; ok && now.Sub(held) < statsIncompleteHoldover {
			s.log.WithField("path", path).Debug("holding last complete stats sample; this one looks partial")
			return
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		s.log.WithError(err).WithField("path", path).Warn("stats marshal failed")
		s.recordError(err)
		return
	}
	// gzip before Put: CXO stores and propagates object bytes verbatim,
	// so a raw JSON body travels uncompressed. Subscribers auto-detect
	// and gunzip (cxoutils.Gunzip). Matches every sibling publisher.
	if err := s.putFn(path, cxoutils.Gzip(raw)); err != nil {
		s.log.WithError(err).WithField("path", path).Warn("stats publisher Put failed")
		s.recordError(err)
		return
	}
	if complete {
		s.heldSince[path] = now
	} else {
		// An incomplete sample now stands on the feed; there is no
		// complete one left to hold, so the next tick may publish
		// freely.
		delete(s.heldSince, path)
	}
}

func (s *StatsCXOPublisher) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err
}

// LastError returns the most recent error encountered by the publish
// loop, or nil if the last tick succeeded for both paths.
func (s *StatsCXOPublisher) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastError
}

// completenessVerdict is one sample's judgment.
type completenessVerdict struct {
	complete   bool
	confidence string
	peak       int
}

// completenessSample is one remembered observation.
type completenessSample struct {
	at    time.Time
	value int
}

// completenessTracker decides whether an aggregate sample is settled or
// still refilling, using the shape of the #4513 artifact: the count
// climbs monotonically from near zero to a settled plateau, resets, and
// climbs again. A sample well below the highest value seen recently is
// therefore far more likely to be a partial read than a real collapse,
// and a sample at or near that peak is the plateau.
//
// It deliberately does NOT try to detect the reset itself. A monotonic
// -rise detector would call the first sample after a reset "complete"
// (it has no predecessor to be lower than); a trailing peak has no such
// blind spot, and it degrades gracefully — as the window ages past a
// genuine network shrink, the peak follows the network down.
//
// Not safe for concurrent use; the publish loop is single-goroutine.
type completenessTracker struct {
	started time.Time
	samples []completenessSample
}

func newCompletenessTracker(started time.Time) completenessTracker {
	return completenessTracker{started: started}
}

// observe records a sample and returns its verdict.
func (c *completenessTracker) observe(now time.Time, value int) completenessVerdict {
	cutoff := now.Add(-statsTrailingWindow)
	kept := c.samples[:0]
	for _, s := range c.samples {
		if !s.at.Before(cutoff) {
			kept = append(kept, s)
		}
	}
	c.samples = append(kept, completenessSample{at: now, value: value})

	peak := 0
	for _, s := range c.samples {
		if s.value > peak {
			peak = s.value
		}
	}

	switch {
	case now.Sub(c.started) < statsWarmup:
		return completenessVerdict{complete: false, confidence: ConfidenceWarmup, peak: peak}
	case float64(value) >= float64(peak)*statsCompleteRatio:
		return completenessVerdict{complete: true, confidence: ConfidenceSettled, peak: peak}
	default:
		return completenessVerdict{complete: false, confidence: ConfidenceRefilling, peak: peak}
	}
}
