// Package api pkg/deployment/tpd/api/cxo_stats_daily_test.go c4-net-discovery
//
// Tests for the stats/daily leaf. The claim under test is that a
// consumer can unmarshal the published body into
// store.NetworkMetricResponse and get exactly what GET /metric returns
// — the whole point of adding the path is that the charts stop making
// that request, and they can only stop if the two agree.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// dailyOnlyStore satisfies store.Store by embedding the interface and
// overriding the one method under test. Any other call panics on the
// nil embedded value, which is the assertion that nothing else is
// reached.
type dailyOnlyStore struct {
	store.Store
	resp  *store.NetworkMetricResponse
	err   error
	calls int
}

func (s *dailyOnlyStore) GetNetworkMetrics(_ context.Context, q store.MetricsQuery) (*store.NetworkMetricResponse, error) {
	s.calls++
	if q.Days != statsDailyDays {
		return nil, errors.New("publisher asked for a window other than statsDailyDays")
	}
	return s.resp, s.err
}

func testDailyResponse() *store.NetworkMetricResponse {
	return &store.NetworkMetricResponse{
		Daily: []store.DailyAggregate{
			{
				Date:      "2026-09-04",
				Bandwidth: 9_000_000,
				Latency:   41.5,
				ByType: map[string]*store.TypeMetricAggregate{
					"stcpr": {Bandwidth: 6_000_000, Latency: 30},
					"dmsg":  {Bandwidth: 3_000_000, Latency: 80},
				},
			},
			{
				Date:      "2026-09-03",
				Bandwidth: 4_000_000,
				Latency:   52,
				ByType: map[string]*store.TypeMetricAggregate{
					"stcpr": {Bandwidth: 4_000_000, Latency: 52},
				},
			},
		},
		Cumulative: &store.CumulativeAggregate{
			Bandwidth: 13_000_000,
			ByType: map[string]*store.TypeMetricAggregate{
				"stcpr": {Bandwidth: 10_000_000},
				"dmsg":  {Bandwidth: 3_000_000},
			},
		},
	}
}

// TestDailyStatsIsTheMetricEndpointBody pins the published body to the
// GET /metric response shape: unmarshalling the leaf into
// store.NetworkMetricResponse must reproduce the store's answer
// field-for-field.
func TestDailyStatsIsTheMetricEndpointBody(t *testing.T) {
	want := testDailyResponse()
	api := &API{store: &dailyOnlyStore{resp: want}}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	sp, puts := newTestStatsPublisher(t, api, now.Add(-time.Hour))

	sp.publishDaily(context.Background(), now)

	p, ok := lastPutTo(*puts, StatsPathDaily)
	if !ok {
		t.Fatalf("nothing was published to %s", StatsPathDaily)
	}
	// decodePut asserts the body is gzipped and is JSON — every sibling
	// path on this feed is, and subscribers gunzip unconditionally.
	decodePut(t, p)

	var got store.NetworkMetricResponse
	if err := json.Unmarshal(cxoutils.Gunzip(p.body), &got); err != nil {
		t.Fatalf("body does not unmarshal as store.NetworkMetricResponse: %v", err)
	}
	if len(got.Daily) != len(want.Daily) {
		t.Fatalf("daily days = %d, want %d", len(got.Daily), len(want.Daily))
	}
	for i := range want.Daily {
		w, g := want.Daily[i], got.Daily[i]
		if g.Date != w.Date || g.Bandwidth != w.Bandwidth || g.Latency != w.Latency {
			t.Fatalf("day %d = %+v, want %+v", i, g, w)
		}
		for tp, wv := range w.ByType {
			gv, ok := g.ByType[tp]
			if !ok || gv == nil {
				t.Fatalf("day %d: type %q missing from the published body", i, tp)
			}
			if gv.Bandwidth != wv.Bandwidth || gv.Latency != wv.Latency {
				t.Fatalf("day %d type %q = %+v, want %+v", i, tp, gv, wv)
			}
		}
	}
	if got.Cumulative == nil || got.Cumulative.Bandwidth != want.Cumulative.Bandwidth {
		t.Fatalf("cumulative = %+v, want bandwidth %d", got.Cumulative, want.Cumulative.Bandwidth)
	}

	// The completeness stamp rides along; a consumer must be able to see
	// that an early sample is not trustworthy as an absolute.
	var stamped DailyStats
	if err := json.Unmarshal(cxoutils.Gunzip(p.body), &stamped); err != nil {
		t.Fatalf("body does not unmarshal as DailyStats: %v", err)
	}
	if stamped.Confidence != ConfidenceWarmup || stamped.Complete {
		t.Fatalf("stamp = (%q, complete=%v), want warmup/incomplete before the transport set is judged",
			stamped.Confidence, stamped.Complete)
	}
	if stamped.Days != statsDailyDays {
		t.Fatalf("days = %d, want %d", stamped.Days, statsDailyDays)
	}
}

// TestDailyStatsCarriesTheTransportVerdict checks the stamp is the
// transport-set judgment rather than one invented from the bandwidth
// figures — a quiet day is not a partial read.
func TestDailyStatsCarriesTheTransportVerdict(t *testing.T) {
	api := &API{store: &dailyOnlyStore{resp: testDailyResponse()}}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	sp, puts := newTestStatsPublisher(t, api, now.Add(-time.Hour))
	sp.lastTransportVerdict = completenessVerdict{complete: true, confidence: ConfidenceSettled, peak: 4321}

	sp.publishDaily(context.Background(), now)

	p, ok := lastPutTo(*puts, StatsPathDaily)
	if !ok {
		t.Fatal("nothing was published")
	}
	var stamped DailyStats
	if err := json.Unmarshal(cxoutils.Gunzip(p.body), &stamped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !stamped.Complete || stamped.Confidence != ConfidenceSettled || stamped.TrailingPeak != 4321 {
		t.Fatalf("stamp = %+v, want the transport set's settled verdict with peak 4321", stamped)
	}
}

// TestDailyStatsSkipsNothingness checks an empty or failed store answer
// never overwrites a good body with an empty one. A store that has not
// answered is not news about the network.
func TestDailyStatsSkipsNothingness(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	for name, st := range map[string]*dailyOnlyStore{
		"store error":  {err: errors.New("redis down")},
		"empty series": {resp: &store.NetworkMetricResponse{}},
		"nil response": {},
	} {
		api := &API{store: st}
		sp, puts := newTestStatsPublisher(t, api, now.Add(-time.Hour))
		sp.publishDaily(context.Background(), now)
		if _, ok := lastPutTo(*puts, StatsPathDaily); ok {
			t.Fatalf("%s: published a body anyway", name)
		}
	}
}

// TestPublishOnceRunsDailyOnItsOwnCadence checks the 30-day store query
// does not run on the 12-second tick. The body is small; the recompute
// is not, and that is the whole reason statsDailyInterval exists.
func TestPublishOnceRunsDailyOnItsOwnCadence(t *testing.T) {
	st := &dailyOnlyStore{resp: testDailyResponse()}
	api := &API{transportsCache: testEntries(20), uptimesCache: testUptimes(), store: st}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	sp, _ := newTestStatsPublisher(t, api, now.Add(-time.Hour))

	// Twelve seconds apart for half the daily interval: one recompute.
	for elapsed := time.Duration(0); elapsed < statsDailyInterval/2; elapsed += statsPublishInterval {
		sp.publishOnce(context.Background())
	}
	if st.calls != 1 {
		t.Fatalf("store queried %d times inside one interval, want 1", st.calls)
	}

	// publishOnce stamps wall-clock time, so drive the next window by
	// rewinding the recorded attempt rather than sleeping.
	sp.lastDailyAt = time.Now().UTC().Add(-statsDailyInterval - time.Second)
	sp.publishOnce(context.Background())
	if st.calls != 2 {
		t.Fatalf("store queried %d times after the interval elapsed, want 2", st.calls)
	}
}
