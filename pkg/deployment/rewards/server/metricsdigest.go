// Package rewardsserver pkg/deployment/rewards/server/metricsdigest.go c3-app-rewards
//
// The statistics pages read three things from TPD's per-transport metrics
// feed: the newest day's verified bandwidth, each visor's daily bytes over
// tpdDailyDays, and how many transports each visor has in that window. The
// feed is hundreds of megabytes once filled, so it is no longer held for the
// life of the process. Every interval it is filled on a manager of its own,
// reduced to those figures one day at a time, and dropped.
package rewardsserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
)

// DefaultMetricsDigestInterval is how often the metrics digest is rebuilt.
// Each rebuild is a full fill of the feed.
const DefaultMetricsDigestInterval = 3 * time.Hour

// metricsDigestFillWait bounds one fill. A full fill took 2m45s in November
// 2026, far past the 45s a page waits.
const metricsDigestFillWait = 10 * time.Minute

// metricsDigestRetry is how soon a failed build is tried again.
const metricsDigestRetry = 10 * time.Minute

// metricsDigest holds what the pages need from one fill of the feed.
type metricsDigest struct {
	// dayTotal is the verified bandwidth of the newest day leaf, as
	// fillTPDBandwidth sums it. dayErr is set when that day did not parse.
	dayTotal uint64
	dayErr   string
	// daily is each visor's bytes per date and edges is visor -> transports,
	// over the newest tpdDailyDays leaves. winErr is set when none parsed.
	daily  *visorDays
	edges  map[string]int
	winErr string
	src    statsSource
}

var metricsDigestState struct {
	sync.RWMutex
	d *metricsDigest
}

// currentMetricsDigest returns the last digest built, or nil.
func currentMetricsDigest() *metricsDigest {
	metricsDigestState.RLock()
	defer metricsDigestState.RUnlock()
	return metricsDigestState.d
}

// startMetricsDigest builds the digest now and then every interval until ctx
// ends.
func startMetricsDigest(ctx context.Context, interval time.Duration, log *logging.Logger) {
	if interval <= 0 {
		interval = DefaultMetricsDigestInterval
	}
	go func() {
		for {
			next := interval
			start := time.Now()
			if err := buildMetricsDigest(); err != nil {
				log.WithError(err).Warn("Could not build the metrics digest")
				next = min(interval, metricsDigestRetry)
			} else {
				log.WithField("took", time.Since(start).Round(time.Second)).Info("Built the metrics digest")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(next):
			}
		}
	}()
}

// metricsDigestGCPercent is the GOGC of a build. While the feed is held the
// live heap is about its size, and the default of 100 lets garbage double it.
const metricsDigestGCPercent = 20

func buildMetricsDigest() error {
	defer debug.SetGCPercent(debug.SetGCPercent(metricsDigestGCPercent))
	// The closed feed leaves hundreds of megabytes free at once; hand them
	// back now rather than at the scavenger's pace.
	defer debug.FreeOSMemory()
	return withMetricsFeed(metricsDigestFillWait, func(mgr *cxosub.Manager) error {
		byDate, dates, newest, err := metricsDayBodies(mgr)
		if err != nil {
			return err
		}
		d := digestMetricsDays(byDate, dates, tpdDailyDays)
		d.src = statsSource{Via: "CXO", Path: tpdstore.MetricsDayPrefix + "*", At: newest}
		metricsDigestState.Lock()
		metricsDigestState.d = d
		metricsDigestState.Unlock()
		return nil
	})
}

// metricsDayBodies groups the feed's day leaves by date, newest date first,
// with the time the newest leaf landed.
func metricsDayBodies(mgr *cxosub.Manager) (map[string][][]byte, []string, time.Time, error) {
	byDate := make(map[string][][]byte)
	var newest time.Time
	ok := mgr.Walk(cxosub.FeedTPDMetrics, tpdstore.MetricsDayPrefix, func(path string, body []byte) bool {
		date, day := tpdstore.MetricsDayDate(path)
		if !day || len(body) == 0 {
			return true
		}
		// The snapshot's bodies are never modified in place, so holding them is safe.
		byDate[date] = append(byDate[date], body)
		if ts, found := mgr.SyncedAt(cxosub.FeedTPDMetrics, path); found && ts.After(newest) {
			newest = ts
		}
		return true
	})
	if !ok || len(byDate) == 0 {
		return nil, nil, time.Time{}, fmt.Errorf("no metrics day leaves on the feed")
	}
	dates := make([]string, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	// The date format sorts lexically into chronological order.
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return byDate, dates, newest, nil
}

// decodeMetricsDay decodes one day's leaves, or reports false when any part
// fails to parse or the day holds no records.
func decodeMetricsDay(bodies [][]byte) ([]tpdstore.TransportMetric, bool) {
	var recs []tpdstore.TransportMetric
	for _, body := range bodies {
		var chunk []tpdstore.TransportMetric
		if err := json.Unmarshal(cxoutils.Gunzip(body), &chunk); err != nil {
			return nil, false
		}
		recs = append(recs, chunk...)
	}
	return recs, len(recs) > 0
}

// eachMetric streams one day's records to fn, decompressing and decoding one
// record at a time. It reports false where decodeMetricsDay would, and fn may
// have seen part of the day by then.
func eachMetric(bodies [][]byte, fn func(*tpdstore.TransportMetric)) bool {
	n := 0
	for _, body := range bodies {
		var r io.Reader = bytes.NewReader(body)
		if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
			zr, err := gzip.NewReader(r)
			if err != nil {
				return false
			}
			r = zr
		}
		dec := json.NewDecoder(r)
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if tok != nil { // a null part holds no records
			if tok != json.Delim('[') {
				return false
			}
			for dec.More() {
				var m tpdstore.TransportMetric
				if err := dec.Decode(&m); err != nil {
					return false
				}
				fn(&m)
				n++
			}
			if _, err := dec.Token(); err != nil {
				return false
			}
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return false
		}
	}
	return n > 0
}

// digestMetricsDays reduces the newest days of dates (newest first) to a
// digest, streaming each day. The figures equal those computed from
// tpdstore.MergeDailyMetrics over the same days: a transport's edges are the
// first non-empty ones in newest-first order, and every row of the transport
// is attributed to them.
func digestMetricsDays(byDate map[string][][]byte, dates []string, days int) *metricsDigest {
	d := &metricsDigest{dayErr: "no metrics day leaf parsed"}
	if len(dates) > days {
		dates = dates[:days]
	}

	// Pass 1: the edges of each transport, and the newest day's total. A
	// day is applied only once all of it parsed. Edge keys repeat across
	// transports and days, so they are interned.
	intern := make(map[string]string)
	internEdges := func(edges []string) []string {
		if len(edges) == 0 {
			return nil
		}
		out := make([]string, len(edges))
		for i, pk := range edges {
			v, ok := intern[pk]
			if !ok {
				v = pk
				intern[pk] = v
			}
			out[i] = v
		}
		return out
	}
	edgesByID := make(map[string][]string)
	var parsed []string
	for i, date := range dates {
		staged := make(map[string][]string)
		var s tpdNetworkSummary
		ok := eachMetric(byDate[date], func(m *tpdstore.TransportMetric) {
			if i == 0 {
				accumulateVerifiedBandwidth([]tpdstore.TransportMetric{*m}, &s)
			}
			cur, seen := staged[m.ID]
			if !seen {
				cur, seen = edgesByID[m.ID]
			}
			if seen && len(cur) > 0 {
				return
			}
			staged[m.ID] = internEdges(m.Edges)
		})
		if !ok {
			continue
		}
		if i == 0 {
			d.dayTotal, d.dayErr = s.TotalBandwidth, ""
		}
		maps.Copy(edgesByID, staged)
		parsed = append(parsed, date)
	}
	if len(parsed) == 0 {
		d.winErr = "no metrics day leaf parsed"
		return d
	}

	d.edges = make(map[string]int)
	for _, e := range edgesByID {
		for _, pk := range e {
			d.edges[pk]++
		}
	}

	// Pass 2: each visor's bytes per day, over the days that parsed.
	d.daily = newVisorDays()
	for _, date := range parsed {
		eachMetric(byDate[date], func(m *tpdstore.TransportMetric) {
			addVisorDaily(d.daily, edgesByID[m.ID], m.Daily)
		})
	}
	return d
}

// visorDays is each visor's bytes per date, one slot per date, which is far
// smaller than a map per visor.
type visorDays struct {
	dates []string
	at    map[string]int
	bytes map[string][]uint64
}

func newVisorDays() *visorDays {
	return &visorDays{at: make(map[string]int), bytes: make(map[string][]uint64)}
}

func (v *visorDays) add(pk, date string, n uint64) {
	if n == 0 {
		return
	}
	i, ok := v.at[date]
	if !ok {
		i = len(v.dates)
		v.dates = append(v.dates, date)
		v.at[date] = i
	}
	s := v.bytes[pk]
	if len(s) <= i {
		grown := make([]uint64, len(v.dates))
		copy(grown, s)
		s = grown
		v.bytes[pk] = s
	}
	s[i] += n
}

// of returns the dates on which pk moved bytes, with the bytes.
func (v *visorDays) of(pk string) map[string]uint64 {
	if v == nil {
		return nil
	}
	var out map[string]uint64
	for i, n := range v.bytes[pk] {
		if n > 0 {
			if out == nil {
				out = make(map[string]uint64)
			}
			out[v.dates[i]] = n
		}
	}
	return out
}

// addVisorDaily adds one transport's rows: edge A is edges[0] and edge B is
// edges[1].
func addVisorDaily(daily *visorDays, edges []string, rows []tpdstore.DailyEdgeBandwidth) {
	if len(edges) != 2 {
		return
	}
	for _, r := range rows {
		if r.A != nil {
			daily.add(edges[0], r.Date, r.A.Sent+r.A.Recv)
		}
		if r.B != nil {
			daily.add(edges[1], r.Date, r.B.Sent+r.B.Recv)
		}
	}
}

// visorDailyFromMetrics is visorDays for already merged records.
func visorDailyFromMetrics(metrics []tpdstore.TransportMetric) *visorDays {
	daily := newVisorDays()
	for i := range metrics {
		addVisorDaily(daily, metrics[i].Edges, metrics[i].Daily)
	}
	return daily
}
