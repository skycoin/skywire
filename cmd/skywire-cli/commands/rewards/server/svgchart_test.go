// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/svgchart_test.go c4-vis-cli
package clirewardsserver

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func lineOpts(n int) chartOpts {
	labels := make([]string, n)
	for i := range labels {
		labels[i] = "d"
	}
	return chartOpts{Width: 900, Height: 300, Labels: labels}
}

// A flat run must keep BOTH of its ends. Dropping only the interior would leave
// the path sloping across the whole run instead of running flat then stepping,
// which redraws the data while looking like a size optimization.
func TestCumulativePathKeepsBothEndsOfAFlatRun(t *testing.T) {
	o := lineOpts(6)
	edge := []float64{10, 10, 10, 10, 50, 50}
	yOf := func(v float64) float64 { return 200 - v }

	d := cumulativePath(o, edge, yOf, 260)
	// Points at index 0 and 3 bound the flat run; index 1 and 2 are interior.
	x0, x3 := int(chartX(o, 0)+0.5), int(chartX(o, 3)+0.5)
	if !strings.Contains(d, "M"+itoa(x0)+" 190") {
		t.Fatalf("missing start of flat run in %q", d)
	}
	if !strings.Contains(d, itoa(x3)+" 190") {
		t.Fatalf("missing end of flat run in %q", d)
	}
	x1 := int(chartX(o, 1) + 0.5)
	if strings.Contains(d, itoa(x1)+" 190") {
		t.Errorf("interior point of flat run was kept in %q", d)
	}
}

// The whole point of the rewrite: a long series must not cost one element per
// point. The predecessor emitted 14,417 absolutely-positioned divs.
func TestStackedAreaEmitsOneElementPerSeries(t *testing.T) {
	n := 600
	o := lineOpts(n)
	o.Labels = make([]string, n)
	for i := range o.Labels {
		o.Labels[i] = "2026-01-01"
	}
	var series []chartSeries
	for s := 0; s < 5; s++ {
		vals := make([]float64, n)
		for i := range vals {
			vals[i] = float64(i%7 + s)
		}
		series = append(series, chartSeries{Name: "s", Color: "#fff", Vals: vals})
	}
	out := renderStackedAreaSVG(o, series)
	if got := strings.Count(out, "<path"); got != len(series) {
		t.Errorf("got %d paths for %d series, want one each", got, len(series))
	}
	if strings.Contains(out, "position: absolute") {
		t.Error("stacked area fell back to positioned divs")
	}
	if strings.Contains(out, "cdn.") || strings.Contains(out, "<script") {
		t.Error("chart pulled in external script; these pages must be self-contained")
	}
}

// A NaN is a recorded gap, not a zero. Bridging it — or worse, drawing the line
// down to the axis — would assert a measurement that was never taken.
func TestLineChartBreaksOnGapsRatherThanDrawingZero(t *testing.T) {
	o := lineOpts(5)
	s := chartSeries{Name: "sudph", Color: "#36A2EB",
		Vals: []float64{300, math.NaN(), math.NaN(), 400, 420}}
	out := renderLineSVG(o, []chartSeries{s}, nil)

	// Two pen-downs: one before the gap, one after.
	if got := strings.Count(out, "M"); got != 2 {
		t.Errorf("got %d subpaths, want 2 (the line must break at the gap): %s", got, out)
	}
	baseY := chartMarginTop + o.plotHeight()
	if strings.Contains(out, " "+itoa(baseY)+"L") {
		t.Error("gap was drawn down to the axis, which reads as a zero measurement")
	}
}

// Missing latency records are gaps; missing bandwidth records are real zeroes.
func TestTransportTypeSeriesDistinguishesGapFromZero(t *testing.T) {
	daily := []tpdDailyPoint{
		{Date: "2026-09-01", ByType: map[string]tpdDailyByType{"sudph": {Bandwidth: 10, Latency: 300}}},
		{Date: "2026-09-02", ByType: map[string]tpdDailyByType{}},
	}
	bw := transportTypeSeries(daily, false, func(v tpdDailyByType) float64 { return float64(v.Bandwidth) })
	if len(bw) != 1 || bw[0].Vals[1] != 0 {
		t.Errorf("absent bandwidth should be zero, got %v", bw)
	}
	lat := transportTypeSeries(daily, true, func(v tpdDailyByType) float64 { return v.Latency })
	if len(lat) != 1 || !math.IsNaN(lat[0].Vals[1]) {
		t.Errorf("absent latency should be a gap, got %v", lat)
	}
}

// A failed fetch must say so. Rendering a zero would read as a measurement.
func TestTimeSeriesSurfaceFetchErrorsInsteadOfZeroes(t *testing.T) {
	boom := errTest("failed to read response: EOF")
	for name, out := range map[string]string{
		"bandwidth": renderBandwidthTimeSeries(nil, boom),
		"latency":   renderLatencyTimeSeries(nil, boom),
	} {
		if !strings.Contains(out, "unavailable") || !strings.Contains(out, "EOF") {
			t.Errorf("%s: error not surfaced on the page: %s", name, out)
		}
		if strings.Contains(out, "0 B") || strings.Contains(out, "0 ms") {
			t.Errorf("%s: rendered a zero for a failed fetch: %s", name, out)
		}
	}
}

// Series order comes from a map and must not reshuffle between renders.
func TestSortedMapKeysIsStable(t *testing.T) {
	m := map[string]float64{"a": 3, "b": 9, "c": 3, "d": 1}
	want := []string{"b", "a", "c", "d"}
	for i := 0; i < 50; i++ {
		if got := sortedMapKeys(m); !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: got %v, want %v", i, got, want)
		}
	}
}

func TestShortDatesKeepsYearOnlyWhenItChanges(t *testing.T) {
	got := shortDates([]string{"2025-12-30", "2025-12-31", "2026-01-01", "2026-01-02"})
	want := []string{"2025-12-30", "12-31", "2026-01-01", "01-02"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// The current day's timeline is pre-allocated to a full 24 hours, so its
// not-yet-elapsed slots are zeroes. Charting them shows the network going dark
// at a midnight that has not arrived.
func TestLivenessTrimsTheNotYetElapsedTail(t *testing.T) {
	full := strings.Repeat(".", livenessSlotsPerDay)
	half := strings.Repeat(".", 100) + strings.Repeat(" ", livenessSlotsPerDay-100)
	raw := `[{"pk":"a","timeline":{"2026-09-01":"` + full + `","2026-09-02":"` + half + `"}}]`

	s, err := parseLivenessSeries([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if want := livenessSlotsPerDay + 100; len(s.Counts) != want {
		t.Errorf("got %d slots, want %d (the unelapsed tail must be trimmed)", len(s.Counts), want)
	}
	if s.Counts[len(s.Counts)-1] == 0 {
		t.Error("series still ends on a zero slot")
	}
	if len(s.DayStarts) != 2 {
		t.Errorf("got %d day ticks, want 2", len(s.DayStarts))
	}
}

// The timeline disagrees with the tracker's own daily percentage for
// intermittent visors (#4533), so this series must never be labeled uptime.
func TestLivenessChartIsLabeledOnlineNotUptime(t *testing.T) {
	s := &livenessSeries{
		Counts: []int{5, 6, 7}, Dates: []string{"2026-09-01", "2026-09-01", "2026-09-01"},
		DayStarts: []int{0}, Visors: 9,
	}
	out := renderLivenessChart(s, nil)
	if !strings.Contains(out, "Visors Online") {
		t.Error("chart is not labeled as a visors-online count")
	}
	if strings.Contains(out, ">Uptime") || strings.Contains(out, "uptime over") {
		t.Errorf("chart presents itself as uptime: %s", out)
	}
	if !strings.Contains(out, "4533") {
		t.Error("the daily-percentage discrepancy caveat is missing")
	}
}

func TestLivenessSurfacesFetchErrors(t *testing.T) {
	out := renderLivenessChart(nil, errTest("uptime tracker query failed"))
	if !strings.Contains(out, "unavailable") || !strings.Contains(out, "uptime tracker query failed") {
		t.Errorf("error not surfaced: %s", out)
	}
}
