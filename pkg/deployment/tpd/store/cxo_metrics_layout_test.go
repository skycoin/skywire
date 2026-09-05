// Package store pkg/deployment/tpd/store/cxo_metrics_layout_test.go c4-net-discovery
package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// windowFixture builds what GetAllTransportMetrics returns for a
// `days` window: one record per transport, each carrying a daily row
// for a subset of the days so the pivot has to deal with sparse
// coverage rather than a dense rectangle.
func windowFixture(transports int, dates []string) []TransportMetric {
	out := make([]TransportMetric, 0, transports)
	for i := 0; i < transports; i++ {
		var daily []DailyEdgeBandwidth
		for d, date := range dates {
			// Transport i is silent on every (i+1)th day, so coverage is
			// ragged and no two transports share a day set.
			if (d+i)%(i%4+2) == 0 {
				continue
			}
			daily = append(daily, DailyEdgeBandwidth{
				Date: date,
				A:    &EdgeBandwidth{Sent: uint64(i*7919 + d), Recv: uint64(i*104729 + d)},
				B:    &EdgeBandwidth{Sent: uint64(i*15485863 + d), Recv: uint64(i*32452843 + d)},
			})
		}
		m := TransportMetric{
			ID:    fmt.Sprintf("%08x-4660-0f05-aff8-8b006cc4c9%02x", i, i%256),
			Type:  []string{"stcpr", "sudph", "dmsg", "webrtc"}[i%4],
			Live:  i%3 != 0,
			Edges: []string{fmt.Sprintf("02%064x", i), fmt.Sprintf("03%064x", i+1)},
			Daily: daily,
		}
		// Two thirds of the transports have measured latency; the rest
		// only exist through their bandwidth history.
		if i%3 != 2 {
			m.Latency = &TransportLatency{Min: int64(1000 + i), Max: int64(90000 + i), Avg: int64(14000 + i)}
		}
		if m.Latency == nil && len(m.Daily) == 0 {
			// Mirrors the store's own rule: a transport with no metrics
			// data at all is not reported.
			continue
		}
		out = append(out, m)
	}
	return out
}

// pivotAndMerge is the whole wire path in miniature: split the window
// into day leaves, then reassemble it the way the visor does, through
// the JSON each leaf is actually published as.
func pivotAndMerge(t *testing.T, window []TransportMetric, dates []string) []TransportMetric {
	t.Helper()
	byDate := PivotDailyMetrics(window, dates)
	days := make([][]TransportMetric, 0, len(dates))
	for _, date := range dates {
		leaf, ok := byDate[date]
		if !ok {
			t.Fatalf("no leaf produced for %s", date)
		}
		encoded, err := json.Marshal(leaf)
		if err != nil {
			t.Fatalf("leaf %s did not encode: %v", date, err)
		}
		if string(encoded) == "null" {
			t.Fatalf("leaf %s encoded as null, not an empty array", date)
		}
		var decoded []TransportMetric
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("leaf %s did not decode: %v", date, err)
		}
		days = append(days, decoded)
	}
	return MergeDailyMetrics(days)
}

// The load-bearing property: a window assembled from day leaves holds
// every record and every daily row the single-window body held.
func TestPivotMergeRoundTripLosesNothing(t *testing.T) {
	dates := MetricsWindowDates(time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC), 30)
	window := windowFixture(400, dates)

	got := pivotAndMerge(t, window, dates)

	if len(got) != len(window) {
		t.Fatalf("round-trip returned %d records, want %d", len(got), len(window))
	}
	byID := make(map[string]*TransportMetric, len(got))
	for i := range got {
		byID[got[i].ID] = &got[i]
	}
	var wantRows, gotRows int
	for i := range window {
		want := &window[i]
		wantRows += len(want.Daily)
		have, ok := byID[want.ID]
		if !ok {
			t.Fatalf("transport %s is missing from the assembled window", want.ID)
		}
		gotRows += len(have.Daily)
		if have.Type != want.Type {
			t.Errorf("%s: type = %q, want %q", want.ID, have.Type, want.Type)
		}
		if !reflect.DeepEqual(have.Edges, want.Edges) {
			t.Errorf("%s: edges = %v, want %v", want.ID, have.Edges, want.Edges)
		}
		// Live and Latency belong to the current day's leaf, so they
		// survive for the transports that leaf holds — see
		// TestCurrentStateFollowsTheCurrentDayLeaf for the rule.
		if inCurrentDay(want, dates[0]) {
			if have.Live != want.Live {
				t.Errorf("%s: live = %v, want %v", want.ID, have.Live, want.Live)
			}
			if !reflect.DeepEqual(have.Latency, want.Latency) {
				t.Errorf("%s: latency = %+v, want %+v", want.ID, have.Latency, want.Latency)
			}
		} else if have.Live || have.Latency != nil {
			t.Errorf("%s: absent from the current day but reports live=%v latency=%+v", want.ID, have.Live, have.Latency)
		}
		// Daily rows are compared as a set: the assembly orders them by
		// day, newest first, which the fixture's per-transport gaps do
		// not necessarily match index-for-index.
		wantByDate := make(map[string]DailyEdgeBandwidth, len(want.Daily))
		for _, row := range want.Daily {
			wantByDate[row.Date] = row
		}
		for _, row := range have.Daily {
			w, ok := wantByDate[row.Date]
			if !ok {
				t.Fatalf("%s: assembled window invented a row for %s", want.ID, row.Date)
			}
			if !reflect.DeepEqual(row, w) {
				t.Errorf("%s %s: row = %+v, want %+v", want.ID, row.Date, row, w)
			}
			delete(wantByDate, row.Date)
		}
		if len(wantByDate) != 0 {
			t.Errorf("%s: %d daily rows were dropped", want.ID, len(wantByDate))
		}
	}
	if gotRows != wantRows {
		t.Errorf("assembled window holds %d daily rows, want %d", gotRows, wantRows)
	}
}

// inCurrentDay reports whether the current day's leaf will hold a
// record for m: it does so for anything with latency, or with a row
// on that day.
func inCurrentDay(m *TransportMetric, current string) bool {
	if m.Latency != nil {
		return true
	}
	for _, row := range m.Daily {
		if row.Date == current {
			return true
		}
	}
	return false
}

// The documented rule, pinned: current state rides on the current
// day's leaf and nowhere else. A transport with no presence on that
// day reads back not-live with no latency — the single behavioral
// difference from the old per-window bodies, and the price of past
// days being immutable.
func TestCurrentStateFollowsTheCurrentDayLeaf(t *testing.T) {
	dates := MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 4)
	window := []TransportMetric{
		{ // live, measured, active today: everything survives
			ID: "today", Type: "dmsg", Live: true, Edges: []string{"02a", "03b"},
			Latency: &TransportLatency{Min: 1, Max: 3, Avg: 2},
			Daily:   []DailyEdgeBandwidth{{Date: dates[0], A: &EdgeBandwidth{Sent: 7}}},
		},
		{ // live and measured but silent today: the latency puts it in
			// the current leaf anyway, so Live survives
			ID: "quiet", Type: "stcpr", Live: true, Edges: []string{"02c", "03d"},
			Latency: &TransportLatency{Min: 4, Max: 6, Avg: 5},
			Daily:   []DailyEdgeBandwidth{{Date: dates[2], A: &EdgeBandwidth{Sent: 9}}},
		},
		{ // no latency and nothing today: history only, reads not-live
			ID: "historic", Type: "sudph", Live: true, Edges: []string{"02e", "03f"},
			Daily: []DailyEdgeBandwidth{{Date: dates[2], A: &EdgeBandwidth{Sent: 11}}},
		},
	}

	got := pivotAndMerge(t, window, dates)
	if len(got) != 3 {
		t.Fatalf("assembled %d records, want 3", len(got))
	}
	byID := map[string]TransportMetric{}
	for _, m := range got {
		byID[m.ID] = m
	}
	for _, id := range []string{"today", "quiet"} {
		if m := byID[id]; !m.Live || m.Latency == nil {
			t.Errorf("%s: live=%v latency=%+v, want both present", id, m.Live, m.Latency)
		}
	}
	if m := byID["historic"]; m.Live || m.Latency != nil {
		t.Errorf("historic: live=%v latency=%+v, want not-live and unmeasured", m.Live, m.Latency)
	}
	// The history itself is never lost, whatever the current state says.
	for _, id := range []string{"quiet", "historic"} {
		if m := byID[id]; len(m.Daily) != 1 || m.Daily[0].Date != dates[2] {
			t.Errorf("%s: daily = %+v, want the row on %s", id, m.Daily, dates[2])
		}
	}
}

// Daily rows come back newest-first, which is the order
// buildTransportMetrics produces and therefore the order every caller
// of the single-window body already saw.
func TestMergeOrdersDailyRowsNewestFirst(t *testing.T) {
	dates := MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 7)
	got := pivotAndMerge(t, windowFixture(40, dates), dates)
	for _, m := range got {
		for i := 1; i < len(m.Daily); i++ {
			if m.Daily[i-1].Date <= m.Daily[i].Date {
				t.Fatalf("%s: daily rows out of order (%s then %s)", m.ID, m.Daily[i-1].Date, m.Daily[i].Date)
			}
		}
	}
}

// Determinism is the whole economic argument: a leaf whose bytes move
// because Redis handed the transports back in a different order is a
// new CXO object, and content-addressing buys nothing. Both halves of
// the round trip must be stable under input permutation.
func TestPivotAndMergeAreDeterministic(t *testing.T) {
	dates := MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 12)
	window := windowFixture(120, dates)

	shuffled := make([]TransportMetric, len(window))
	copy(shuffled, window)
	// A fixed, reproducible permutation — not a random one, so a
	// failure here is reproducible too.
	for i := range shuffled {
		j := (i*7 + 3) % len(shuffled)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	a, b := PivotDailyMetrics(window, dates), PivotDailyMetrics(shuffled, dates)
	for _, date := range dates {
		ja, err := json.Marshal(a[date])
		if err != nil {
			t.Fatal(err)
		}
		jb, err := json.Marshal(b[date])
		if err != nil {
			t.Fatal(err)
		}
		if string(ja) != string(jb) {
			t.Fatalf("leaf %s encodes differently after permuting the input — the leaf is not content-stable", date)
		}
	}

	if !reflect.DeepEqual(pivotAndMerge(t, window, dates), pivotAndMerge(t, shuffled, dates)) {
		t.Error("assembled windows differ after permuting the input")
	}
}

// Live and Latency are current state, not day-scoped. They belong to
// the current day's leaf only — carrying them in every leaf would make
// every past day mutable and defeat the layout.
func TestOnlyTheCurrentDayLeafCarriesCurrentState(t *testing.T) {
	dates := MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 5)
	byDate := PivotDailyMetrics(windowFixture(60, dates), dates)

	if len(byDate[dates[0]]) == 0 {
		t.Fatal("the current day's leaf is empty")
	}
	var sawLatency, sawLive bool
	for _, m := range byDate[dates[0]] {
		sawLatency = sawLatency || m.Latency != nil
		sawLive = sawLive || m.Live
	}
	if !sawLatency || !sawLive {
		t.Error("the current day's leaf carries neither latency nor liveness")
	}
	for _, date := range dates[1:] {
		for _, m := range byDate[date] {
			if m.Latency != nil {
				t.Errorf("%s: past-day leaf carries latency for %s", date, m.ID)
			}
			if m.Live {
				t.Errorf("%s: past-day leaf carries liveness for %s", date, m.ID)
			}
			if len(m.Daily) != 1 || m.Daily[0].Date != date {
				t.Errorf("%s: past-day record for %s holds %d rows, want exactly that day's", date, m.ID, len(m.Daily))
			}
			if m.ID == "" || m.Type == "" || len(m.Edges) != 2 {
				t.Errorf("%s: past-day record lost its identity: %+v", date, m)
			}
		}
	}
}

// A transport with latency but no bandwidth on any day in the window
// still has to appear — pkg/tpviz's latency graph reads exactly one
// day's leaf and would otherwise lose it.
func TestLatencyOnlyTransportLandsInTheCurrentDayLeaf(t *testing.T) {
	dates := MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 7)
	window := []TransportMetric{{
		ID:      "quiet",
		Type:    "dmsg",
		Live:    true,
		Edges:   []string{"02aa", "03bb"},
		Latency: &TransportLatency{Min: 1, Max: 3, Avg: 2},
		Daily:   []DailyEdgeBandwidth{},
	}}
	byDate := PivotDailyMetrics(window, dates)
	if len(byDate[dates[0]]) != 1 {
		t.Fatalf("current-day leaf holds %d records, want 1", len(byDate[dates[0]]))
	}
	if got := byDate[dates[0]][0]; got.Latency == nil || !got.Live {
		t.Errorf("latency-only record lost its state: %+v", got)
	}
	for _, date := range dates[1:] {
		if len(byDate[date]) != 0 {
			t.Errorf("%s: past-day leaf should be empty, holds %d", date, len(byDate[date]))
		}
	}
}

// Rows dated outside the window belong to no leaf. Publishing them
// under a day that is not in the window would resurrect data the
// window is supposed to have retired.
func TestPivotDropsRowsOutsideTheWindow(t *testing.T) {
	dates := MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 3)
	window := []TransportMetric{{
		ID:   "t",
		Type: "stcpr",
		Daily: []DailyEdgeBandwidth{
			{Date: dates[1], A: &EdgeBandwidth{Sent: 1}},
			{Date: "2020-01-01", A: &EdgeBandwidth{Sent: 99}},
		},
	}}
	byDate := PivotDailyMetrics(window, dates)
	if _, ok := byDate["2020-01-01"]; ok {
		t.Error("the pivot created a leaf outside the window")
	}
	if len(byDate) != len(dates) {
		t.Errorf("pivot produced %d leaves, want %d — one per window day", len(byDate), len(dates))
	}
	total := 0
	for _, recs := range byDate {
		total += len(recs)
	}
	if total != 1 {
		t.Errorf("pivot placed %d records, want 1", total)
	}
}

func TestMetricsWindowDates(t *testing.T) {
	now := time.Date(2026, 3, 2, 5, 30, 0, 0, time.UTC)
	got := MetricsWindowDates(now, 4)
	want := []string{"2026-03-02", "2026-03-01", "2026-02-28", "2026-02-27"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MetricsWindowDates = %v, want %v", got, want)
	}
	if got := MetricsWindowDates(now, 0); len(got) != 1 {
		t.Errorf("a zero window returned %v, want a single day", got)
	}
	// A non-UTC clock must still name UTC days, or the leaf a reader
	// asks for and the leaf the publisher wrote can differ by one.
	east := time.FixedZone("UTC+13", 13*3600)
	if got := MetricsWindowDates(now.In(east), 1); got[0] != "2026-03-02" {
		t.Errorf("window dates are not UTC: got %v", got)
	}
}

func TestMetricsDayPaths(t *testing.T) {
	if got, want := MetricsDayPath("2026-09-04"), "metrics/day/2026-09-04"; got != want {
		t.Errorf("MetricsDayPath = %q, want %q", got, want)
	}
	// Zero-padded so a lexical sort of the paths is publication order.
	if a, b := MetricsDayPartPath("2026-09-04", 9), MetricsDayPartPath("2026-09-04", 10); a >= b {
		t.Errorf("%q should sort before %q", a, b)
	}
	for _, tc := range []struct {
		path string
		date string
		ok   bool
	}{
		{"metrics/day/2026-09-04", "2026-09-04", true},
		{"metrics/day/2026-09-04/part/0003", "2026-09-04", true},
		{"metrics/days/7", "", false},
		{"metrics/days/7/part/0000", "", false},
		{"metrics/day/", "", false},
		{"uptimes/days/7", "", false},
	} {
		date, ok := MetricsDayDate(tc.path)
		if ok != tc.ok || date != tc.date {
			t.Errorf("MetricsDayDate(%q) = %q,%v; want %q,%v", tc.path, date, ok, tc.date, tc.ok)
		}
	}
}
