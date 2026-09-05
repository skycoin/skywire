// Package visor pkg/visor/api_tpd_metrics_subscriber_test.go c3-vis-core
package visor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// The reader stitches the publisher's part leaves back into the single array
// its callers expect. Splicing at the byte level is what makes a 30-megabyte
// window affordable, so the boundaries have to be exactly right.
func TestSpliceJSONArrays(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
		want  string
	}{
		{"single part", []string{`[{"id":"a"}]`}, `[{"id":"a"}]`},
		{"two parts", []string{`[{"id":"a"}]`, `[{"id":"b"},{"id":"c"}]`}, `[{"id":"a"},{"id":"b"},{"id":"c"}]`},
		{"empty part in the middle", []string{`[{"id":"a"}]`, `[]`, `[{"id":"b"}]`}, `[{"id":"a"},{"id":"b"}]`},
		{"all parts empty", []string{`[]`, `[]`}, `[]`},
		{"surrounding whitespace", []string{"  [ {\"id\":\"a\"} ] \n", `[{"id":"b"}]`}, `[{"id":"a"},{"id":"b"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make([][]byte, len(tc.parts))
			for i, p := range tc.parts {
				bodies[i] = []byte(p)
			}
			got, err := spliceJSONArrays(bodies)
			if err != nil {
				t.Fatalf("spliceJSONArrays: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
			var decoded []map[string]any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Errorf("result is not valid JSON: %v", err)
			}
		})
	}
}

// A body that is not an array must be reported, not spliced: pasting it between
// the brackets would yield malformed JSON that only fails much further
// downstream, in the hvui.
func TestSpliceJSONArraysRejectsNonArray(t *testing.T) {
	for _, bad := range []string{`{"id":"a"}`, ``, `[`, `null`, `"a"`} {
		if _, err := spliceJSONArrays([][]byte{[]byte(`[{"id":"a"}]`), []byte(bad)}); err == nil {
			t.Errorf("spliceJSONArrays accepted a non-array part %q", bad)
		}
	}
}

// fakeSnapshot is a metricsSnapshot backed by a plain path→body map,
// standing in for a live CXO subscription.
type fakeSnapshot struct {
	leaves map[string][]byte
	at     time.Time
}

func (f *fakeSnapshot) Get(_ CXOFeed, path string) ([]byte, time.Time, bool) {
	b, ok := f.leaves[path]
	return b, f.at, ok
}

func (f *fakeSnapshot) SyncedAt(_ CXOFeed, path string) (time.Time, bool) {
	_, ok := f.leaves[path]
	return f.at, ok
}

func (f *fakeSnapshot) Walk(_ CXOFeed, prefix string, fn func(string, []byte) bool) bool {
	if len(f.leaves) == 0 {
		return false
	}
	for path, body := range f.leaves {
		if prefix != "" && !treestore.HasPrefix(path, prefix) {
			continue
		}
		if !fn(path, body) {
			return true
		}
	}
	return true
}

// dayLeaves publishes `window` through the publisher's pivot, exactly
// as TPD does, and hands back the leaves a subscriber would hold.
func dayLeaves(t *testing.T, window []tpdstore.TransportMetric, dates []string) *fakeSnapshot {
	t.Helper()
	f := &fakeSnapshot{leaves: map[string][]byte{}, at: time.Unix(1767225600, 0).UTC()}
	for date, recs := range tpdstore.PivotDailyMetrics(window, dates) {
		body, err := json.Marshal(recs)
		if err != nil {
			t.Fatal(err)
		}
		f.leaves[tpdstore.MetricsDayPath(date)] = cxoutils.Gzip(body)
	}
	return f
}

func metricsWindowFixture(dates []string) []tpdstore.TransportMetric {
	out := make([]tpdstore.TransportMetric, 0, 12)
	for i := 0; i < 12; i++ {
		var daily []tpdstore.DailyEdgeBandwidth
		for d, date := range dates {
			if (d+i)%3 == 0 {
				continue
			}
			daily = append(daily, tpdstore.DailyEdgeBandwidth{
				Date: date,
				A:    &tpdstore.EdgeBandwidth{Sent: uint64(i*100 + d)},
				B:    &tpdstore.EdgeBandwidth{Recv: uint64(i*200 + d)},
			})
		}
		out = append(out, tpdstore.TransportMetric{
			ID:      fmt.Sprintf("tp-%02d", i),
			Type:    "dmsg",
			Live:    i%2 == 0,
			Edges:   []string{fmt.Sprintf("02%02d", i), fmt.Sprintf("03%02d", i)},
			Latency: &tpdstore.TransportLatency{Min: int64(i), Max: int64(i + 10), Avg: int64(i + 5)},
			Daily:   daily,
		})
	}
	return out
}

// The reader's half of the round trip: a window assembled from day
// leaves must hold every record and every daily row TPD computed, and
// must decode as the single JSON array every caller expects.
func TestAssembleTransportMetricDaysRebuildsTheWindow(t *testing.T) {
	dates := tpdstore.MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 7)
	window := metricsWindowFixture(dates)
	snap := dayLeaves(t, window, dates)

	body, ts, err := readTransportMetricsCXO(snap, 7)
	if err != nil {
		t.Fatalf("readTransportMetricsCXO: %v", err)
	}
	if ts.IsZero() {
		t.Error("no sync timestamp reported")
	}
	var got []tpdstore.TransportMetric
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("assembled window is not a JSON array of TransportMetric: %v", err)
	}
	if len(got) != len(window) {
		t.Fatalf("assembled %d records, want %d", len(got), len(window))
	}

	var wantRows, gotRows int
	for i := range window {
		wantRows += len(window[i].Daily)
	}
	byID := map[string]tpdstore.TransportMetric{}
	for _, m := range got {
		gotRows += len(m.Daily)
		byID[m.ID] = m
	}
	if gotRows != wantRows {
		t.Errorf("assembled window holds %d daily rows, want %d", gotRows, wantRows)
	}
	for i := range window {
		have, ok := byID[window[i].ID]
		if !ok {
			t.Fatalf("%s is missing from the assembled window", window[i].ID)
		}
		if have.Latency == nil || have.Latency.Avg != window[i].Latency.Avg {
			t.Errorf("%s: latency = %+v, want %+v", window[i].ID, have.Latency, window[i].Latency)
		}
		if have.Live != window[i].Live {
			t.Errorf("%s: live = %v, want %v", window[i].ID, have.Live, window[i].Live)
		}
	}
}

// Asking for fewer days than are published must narrow the window,
// not return everything TPD happens to be holding.
func TestAssembleTransportMetricDaysHonoursTheDayCount(t *testing.T) {
	dates := tpdstore.MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 30)
	snap := dayLeaves(t, metricsWindowFixture(dates), dates)

	for _, days := range []int{1, 3, 7, 30} {
		body, _, err := readTransportMetricsCXO(snap, days)
		if err != nil {
			t.Fatalf("days=%d: %v", days, err)
		}
		var got []tpdstore.TransportMetric
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("days=%d: %v", days, err)
		}
		for _, m := range got {
			for _, row := range m.Daily {
				if row.Date < dates[days-1] {
					t.Errorf("days=%d: %s carries a row dated %s, outside the window", days, m.ID, row.Date)
				}
			}
		}
	}
}

// Assembly must be byte-stable: the same leaves have to produce the
// same window however the snapshot map iterates.
func TestAssembleTransportMetricDaysIsDeterministic(t *testing.T) {
	dates := tpdstore.MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 9)
	snap := dayLeaves(t, metricsWindowFixture(dates), dates)

	first, _, err := readTransportMetricsCXO(snap, 9)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		again, _, err := readTransportMetricsCXO(snap, 9)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatal("two assemblies of the same leaves differ")
		}
	}
}

// A visor can outrun the TPD it reads: both update from the same
// develop-latest binary on a ~5 minute timer. With no day leaves the
// reader must still serve the previous layout instead of reporting a
// cold feed and pushing the hvui onto the HTTP path.
func TestReadFallsBackToTheLegacyWindowLayout(t *testing.T) {
	legacy := []byte(`[{"id":"tp-legacy","type":"stcpr","live":true,"daily":[]}]`)
	snap := &fakeSnapshot{
		leaves: map[string][]byte{"metrics/days/7": cxoutils.Gzip(legacy)},
		at:     time.Unix(1767225600, 0).UTC(),
	}
	body, _, err := readTransportMetricsCXO(snap, 7)
	if err != nil {
		t.Fatalf("readTransportMetricsCXO: %v", err)
	}
	if !bytes.Equal(body, legacy) {
		t.Errorf("got %s, want the legacy window body %s", body, legacy)
	}

	// A legacy window split across part leaves stitches the same way.
	snap.leaves = map[string][]byte{
		"metrics/days/7/part/0000": cxoutils.Gzip([]byte(`[{"id":"a"}]`)),
		"metrics/days/7/part/0001": cxoutils.Gzip([]byte(`[{"id":"b"}]`)),
	}
	body, _, err = readTransportMetricsCXO(snap, 7)
	if err != nil {
		t.Fatalf("readTransportMetricsCXO (parts): %v", err)
	}
	if want := `[{"id":"a"},{"id":"b"}]`; string(body) != want {
		t.Errorf("got %s, want %s", body, want)
	}

	// Nothing at all is still a miss, not a panic or an empty array.
	snap.leaves = map[string][]byte{}
	if _, _, err := readTransportMetricsCXO(snap, 7); !errors.Is(err, ErrTPDMetricsNotReady) {
		t.Errorf("empty feed returned %v, want ErrTPDMetricsNotReady", err)
	}
}

// A day too large for one CXO object is published as part leaves; the
// reader has to stitch them before merging, or that day's records
// vanish from every window containing it.
func TestAssembleStitchesASplitDayLeaf(t *testing.T) {
	dates := tpdstore.MetricsWindowDates(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), 2)
	snap := &fakeSnapshot{leaves: map[string][]byte{}, at: time.Unix(1767225600, 0).UTC()}
	snap.leaves[tpdstore.MetricsDayPath(dates[0])] = cxoutils.Gzip(
		[]byte(`[{"id":"x","type":"dmsg","live":true,"daily":[]}]`))
	snap.leaves[tpdstore.MetricsDayPartPath(dates[1], 0)] = cxoutils.Gzip(
		[]byte(`[{"id":"x","type":"dmsg","daily":[{"date":"` + dates[1] + `","a":{"sent":5,"recv":0}}]}]`))
	snap.leaves[tpdstore.MetricsDayPartPath(dates[1], 1)] = cxoutils.Gzip(
		[]byte(`[{"id":"y","type":"stcpr","daily":[{"date":"` + dates[1] + `","a":{"sent":9,"recv":0}}]}]`))

	body, _, err := readTransportMetricsCXO(snap, 2)
	if err != nil {
		t.Fatalf("readTransportMetricsCXO: %v", err)
	}
	var got []tpdstore.TransportMetric
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("assembled %d records, want 2 (x and y)", len(got))
	}
	if got[0].ID != "x" || !got[0].Live || len(got[0].Daily) != 1 {
		t.Errorf("x = %+v, want the live current-day record joined to its split-day row", got[0])
	}
	if got[1].ID != "y" || len(got[1].Daily) != 1 {
		t.Errorf("y = %+v, want the record that only existed in the second part", got[1])
	}
}
