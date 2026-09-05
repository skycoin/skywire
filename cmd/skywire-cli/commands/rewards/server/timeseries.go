// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/timeseries.go c4-vis-cli
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skycoin/skywire/deployment"
)

// TPD's daily aggregate: the cheap time series behind the /stats charts.
//
// The per-transport body (/metrics?days=N&bandwidth=true) is tens of megabytes
// over dmsg and was failing outright — /stats/bandwidth-history rendered
// "failed to read response: EOF" after six seconds. TPD already publishes the
// reduction the charts actually want at /metric?days=N: the whole 30-day series
// with a per-transport-type breakdown, in under three kilobytes, carrying
// LATENCY as well as bandwidth.

// tpdDailyByType is one transport type's contribution on one day.
type tpdDailyByType struct {
	Bandwidth uint64  `json:"bandwidth"`
	Latency   float64 `json:"latency"`
}

// tpdDailyPoint is one day of the network-wide aggregate.
type tpdDailyPoint struct {
	Date      string                    `json:"date"`
	Bandwidth uint64                    `json:"bandwidth"`
	Latency   float64                   `json:"latency"`
	ByType    map[string]tpdDailyByType `json:"by_type"`
}

// tpdDailyAggregate is the parsed /metric?days=N response plus the fetch
// outcome. OK/Err follow the BandwidthOK/BandwidthErr convention in
// tpdNetworkSummary: a failed fetch must never render as a zero, because a
// "0 B" on a bandwidth page reads as a measurement rather than as a gap.
type tpdDailyAggregate struct {
	Daily []tpdDailyPoint `json:"daily"`
	// Cumulative is TPD's running total, kept for the summary line.
	Cumulative struct {
		Bandwidth uint64                    `json:"bandwidth"`
		ByType    map[string]tpdDailyByType `json:"by_type"`
	} `json:"cumulative"`
	Fetched string `json:"fetched"`
}

const (
	tpdDailyCacheFile = "tpd_daily.json"
	// tpdDailyDays is the window requested. TPD returns only the history it
	// holds — currently far fewer points than this — and the pages report what
	// came back rather than padding the series out to the requested length.
	tpdDailyDays = 30
)

// fetchTPDDailyAggregate GETs TPD's daily aggregate, cached on disk for
// tpdSummaryCacheMaxAge the same way getTPDNetworkSummary caches its summary.
func fetchTPDDailyAggregate() (*tpdDailyAggregate, error) {
	cachePath := filepath.Join(tempStatsPath, tpdDailyCacheFile)
	if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) <= tpdSummaryCacheMaxAge {
		if data, rErr := os.ReadFile(cachePath); rErr == nil { //nolint:gosec
			var agg tpdDailyAggregate
			if json.Unmarshal(data, &agg) == nil && len(agg.Daily) > 0 {
				return &agg, nil
			}
		}
	}

	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
	url := fmt.Sprintf("%s/metric?days=%d", tpdURL, tpdDailyDays)

	resp, err := statsHTTPGet(url)
	if err != nil {
		return nil, fmt.Errorf("TPD request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TPD returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read TPD response: %w", err)
	}

	var agg tpdDailyAggregate
	if err := json.Unmarshal(body, &agg); err != nil {
		return nil, fmt.Errorf("failed to parse TPD daily aggregate: %w", err)
	}
	if len(agg.Daily) == 0 {
		return nil, fmt.Errorf("TPD daily aggregate carried no days")
	}
	// TPD returns newest-first; charts read left-to-right oldest-first.
	sort.Slice(agg.Daily, func(i, j int) bool { return agg.Daily[i].Date < agg.Daily[j].Date })
	agg.Fetched = time.Now().UTC().Format(time.RFC3339)

	if data, mErr := json.Marshal(&agg); mErr == nil {
		os.WriteFile(cachePath, data, 0600) //nolint:errcheck,gosec
	}
	return &agg, nil
}

// tpdAggregateCaveat is the provenance note the bandwidth and latency charts
// must carry. These are TPD's own cumulative aggregates, not the min()-verified
// figures the reward calculation uses (see the three-branch trust model in
// fillTPDBandwidth). Correct as a trend; not a reward-verified number.
const tpdAggregateCaveat = "Source: Transport Discovery's own daily aggregate (<code>/metric</code>). " +
	"These are TPD's reported totals, <b>not</b> the min()-verified per-edge figures used by the " +
	"reward calculation — a transport whose two edges disagree is counted here as reported. " +
	"Correct as a trend; do not read these as reward-verified bandwidth."

// transportTypeSeries builds one chart series per transport type, ordered by
// total contribution so the legend and the band order are stable between
// renders — ranging the map directly reshuffles them between requests. pick
// selects bandwidth or latency from a day's per-type record.
//
// missingIsGap decides what a day with no record for that type means. For
// bandwidth it is a genuine zero: the type moved no bytes, and the stack's
// total is still right. For latency it is a GAP — a type that was not measured
// has no average, and plotting one as 0 ms would invent a measurement — so the
// value is NaN and the line breaks.
func transportTypeSeries(daily []tpdDailyPoint, missingIsGap bool, pick func(tpdDailyByType) float64) []chartSeries {
	totals := make(map[string]float64)
	for _, d := range daily {
		for t, v := range d.ByType {
			totals[t] += pick(v)
		}
	}
	types := sortedMapKeys(totals)

	series := make([]chartSeries, 0, len(types))
	for i, t := range types {
		vals := make([]float64, len(daily))
		for di, d := range daily {
			rec, ok := d.ByType[t]
			switch {
			case ok:
				vals[di] = pick(rec)
			case missingIsGap:
				vals[di] = math.NaN()
			default:
				vals[di] = 0
			}
		}
		series = append(series, chartSeries{
			Name:  t,
			Color: chartColors[i%len(chartColors)],
			Vals:  vals,
		})
	}
	return series
}

// renderBandwidthTimeSeries renders daily total bandwidth as a stacked area by
// transport type. On a fetch failure it says so; it never draws a zero series.
func renderBandwidthTimeSeries(agg *tpdDailyAggregate, err error) string {
	if err != nil {
		return fmt.Sprintf("<h3>Bandwidth Over Time</h3><p style='color:#FF6384;'>Bandwidth time series unavailable: %s</p>",
			html.EscapeString(err.Error()))
	}
	daily := agg.Daily
	labels := shortDates(datesOf(daily))
	series := transportTypeSeries(daily, false, func(v tpdDailyByType) float64 { return float64(v.Bandwidth) })
	for i := range series {
		var tot uint64
		for _, d := range daily {
			tot += d.ByType[series[i].Name].Bandwidth
		}
		series[i].Note = formatBytesChart(tot) + " over the window"
	}

	opts := chartOpts{
		Width: 900, Height: 280, Labels: labels,
		Title:      fmt.Sprintf("Bandwidth Over Time (%d days reported)", len(daily)),
		YAxisLabel: "daily bytes across all transports, stacked by transport type",
		FormatY:    func(v float64) string { return formatBytesChart(uint64(v)) },
	}
	out := renderStackedAreaSVG(opts, series)
	out += fmt.Sprintf("<p style='color:#888;font-size:11px;'>%s</p>", tpdAggregateCaveat)
	out += renderDailyTable(daily, "Bandwidth", func(d tpdDailyPoint) string {
		return formatBytesChart(d.Bandwidth)
	})
	return out
}

// renderLatencyTimeSeries renders average transport latency per day, per
// transport type. Latency is an average, so the types are drawn as separate
// lines — stacking averages would be meaningless.
func renderLatencyTimeSeries(agg *tpdDailyAggregate, err error) string {
	if err != nil {
		return fmt.Sprintf("<h3>Latency Over Time</h3><p style='color:#FF6384;'>Latency time series unavailable: %s</p>",
			html.EscapeString(err.Error()))
	}
	daily := agg.Daily
	labels := shortDates(datesOf(daily))

	series := []chartSeries{{
		Name:  "all transports",
		Color: "#FFFFFF",
		Vals:  make([]float64, len(daily)),
	}}
	for i, d := range daily {
		series[0].Vals[i] = d.Latency
	}
	byType := transportTypeSeries(daily, true, func(v tpdDailyByType) float64 { return v.Latency })
	series = append(series, byType...)

	// One readout per day covering every line, so hovering a column reports the
	// whole cross-section without an element per pixel.
	hover := make([]string, len(daily))
	for i, d := range daily {
		parts := []string{fmt.Sprintf("%s — all: %.0f ms", d.Date, d.Latency)}
		for _, t := range sortedMapKeys(latencyOf(d)) {
			parts = append(parts, fmt.Sprintf("%s: %.0f ms", t, d.ByType[t].Latency))
		}
		hover[i] = strings.Join(parts, "\n")
	}

	opts := chartOpts{
		Width: 900, Height: 260, Labels: labels,
		Title:      fmt.Sprintf("Latency Over Time (%d days reported)", len(daily)),
		YAxisLabel: "average transport latency, milliseconds",
		FormatY:    func(v float64) string { return fmt.Sprintf("%.0f ms", v) },
	}
	out := renderLineSVG(opts, series, hover)
	out += fmt.Sprintf("<p style='color:#888;font-size:11px;'>%s</p>", tpdAggregateCaveat)
	out += renderDailyTable(daily, "Avg latency", func(d tpdDailyPoint) string {
		return fmt.Sprintf("%.1f ms", d.Latency)
	})
	return out
}

// latencyOf returns a day's per-type latency map for stable ordering.
func latencyOf(d tpdDailyPoint) map[string]float64 {
	m := make(map[string]float64, len(d.ByType))
	for t, v := range d.ByType {
		m[t] = v.Latency
	}
	return m
}

func datesOf(daily []tpdDailyPoint) []string {
	out := make([]string, len(daily))
	for i, d := range daily {
		out[i] = d.Date
	}
	return out
}

// renderDailyTable prints the same series as text under the chart — the pages
// are terminal-themed and the numbers are worth reading directly.
func renderDailyTable(daily []tpdDailyPoint, header string, cell func(tpdDailyPoint) string) string {
	var sb strings.Builder
	sb.WriteString("<details style='margin:8px 0;'><summary style='cursor:pointer;color:#3399FF;'>daily values</summary><pre>")
	fmt.Fprintf(&sb, "%-12s %s\n", "Date", header)
	for i := len(daily) - 1; i >= 0; i-- {
		fmt.Fprintf(&sb, "%-12s %s\n", daily[i].Date, cell(daily[i]))
	}
	sb.WriteString("</pre></details>")
	return sb.String()
}
