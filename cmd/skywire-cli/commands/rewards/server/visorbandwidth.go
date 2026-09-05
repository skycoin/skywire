// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/visorbandwidth.go c4-vis-cli
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skycoin/skywire/deployment"
)

// Per-visor bandwidth history.
//
// The network-wide daily aggregate (/metric) carries no per-visor breakdown, so
// this page cannot be served from it. The heavy /metrics?days=N per-transport
// body it used to use dies with EOF over dmsg. What works is TPD's
// /metric/visor/{pks} — the same daily reduction, per visor, for a caller-named
// set of public keys: 32 KB and ~1.5 s for twenty visors over thirty days.
//
// The catch, and the reason this page's heading changed: TPD publishes no
// bandwidth-ranked visor index. Ranking the whole network by bandwidth would
// mean asking /metric/visor for all ~900 visors, which is neither cheap nor
// quick. The cheap index that DOES exist is /all-transports/per-key-stats
// (~100 KB, under a second) — transport COUNTS per visor. So the page now
// selects the most-connected visors by transport count and reports their real
// measured bandwidth, and says so on the page rather than implying these are
// the network's top bandwidth carriers.

const (
	visorBWCacheFile = "tpd_visor_bw.json"
	// visorBWCacheMaxAge is longer than the summary cache: this costs two TPD
	// round trips, one of them ~100 KB, and per-visor daily totals do not move
	// within a quarter hour.
	visorBWCacheMaxAge = 15 * time.Minute
	// visorBWTopN is how many visors get their own band. Each adds ~1.6 KB to
	// the TPD request and one path to the chart.
	visorBWTopN = 20
)

// visorBWPoint is one visor's bandwidth on one day.
type visorBWPoint struct {
	Date  string `json:"date"`
	Total uint64 `json:"total"`
}

// visorBWEntry is one visor's series plus the transport count it was selected
// on.
type visorBWEntry struct {
	PK         string         `json:"pk"`
	Transports int            `json:"transports"`
	Daily      []visorBWPoint `json:"daily"`
	Total      uint64         `json:"total"`
	// Reported is false when TPD returned no bandwidth record for this visor on
	// any day of the window. TPD omits the bandwidth object rather than sending
	// a zero, so "not reported" and "moved no bytes" are indistinguishable in
	// the total — and printing "0 B" for the former would read as a
	// measurement. Unreported visors are listed but not charted.
	Reported bool `json:"reported"`
}

// visorBandwidthData is the cached page payload. BandwidthOK/BandwidthErr
// follow tpdNetworkSummary: the transport-count ranking is useful on its own,
// so a failed bandwidth fetch degrades to "ranking shown, bandwidth
// unavailable" rather than to a page of zeroes.
type visorBandwidthData struct {
	Dates       []string       `json:"dates"`
	Visors      []visorBWEntry `json:"visors"`
	TotalVisors int            `json:"total_visors"`
	BandwidthOK bool           `json:"bandwidth_ok"`
	BandwidthE  string         `json:"bandwidth_err,omitempty"`
	LastUpdated string         `json:"last_updated"`
}

// fetchVisorBandwidth builds the per-visor series, cached on disk.
func fetchVisorBandwidth() (*visorBandwidthData, error) {
	cachePath := filepath.Join(tempStatsPath, visorBWCacheFile)
	if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) <= visorBWCacheMaxAge {
		if data, rErr := os.ReadFile(cachePath); rErr == nil { //nolint:gosec
			var d visorBandwidthData
			if json.Unmarshal(data, &d) == nil && len(d.Visors) > 0 {
				return &d, nil
			}
		}
	}

	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")

	counts, err := fetchPerKeyTransportCounts(tpdURL)
	if err != nil {
		return nil, err
	}
	if len(counts) == 0 {
		return nil, fmt.Errorf("TPD per-key stats carried no visors")
	}

	ranked := sortedMapKeys(counts)
	if len(ranked) > visorBWTopN {
		ranked = ranked[:visorBWTopN]
	}

	out := &visorBandwidthData{
		TotalVisors: len(counts),
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
	}
	for _, pk := range ranked {
		out.Visors = append(out.Visors, visorBWEntry{PK: pk, Transports: counts[pk]})
	}

	if bwErr := fillVisorBandwidth(tpdURL, out); bwErr != nil {
		out.BandwidthE = bwErr.Error()
	}

	if data, mErr := json.Marshal(out); mErr == nil {
		os.WriteFile(cachePath, data, 0600) //nolint:errcheck,gosec
	}
	return out, nil
}

// fetchPerKeyTransportCounts reads the cheap per-visor transport index.
func fetchPerKeyTransportCounts(tpdURL string) (map[string]int, error) {
	resp, err := statsHTTPGet(tpdURL + "/all-transports/per-key-stats")
	if err != nil {
		return nil, fmt.Errorf("TPD per-key request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TPD returned status %d for per-key stats", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read TPD per-key stats: %w", err)
	}
	// An OBJECT keyed by public key, not an array — each value is that visor's
	// transport counts, with "total" alongside the per-type entries.
	var raw map[string]map[string]int
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse TPD per-key stats: %w", err)
	}
	counts := make(map[string]int, len(raw))
	for pk, byType := range raw {
		counts[pk] = byType["total"]
	}
	return counts, nil
}

// fillVisorBandwidth asks TPD for the daily series of the selected visors in
// one request and fills in their points. Best effort: a failure here leaves the
// ranking intact and is reported on the page.
func fillVisorBandwidth(tpdURL string, out *visorBandwidthData) error {
	pks := make([]string, 0, len(out.Visors))
	for _, v := range out.Visors {
		pks = append(pks, v.PK)
	}
	url := fmt.Sprintf("%s/metric/visor/%s?days=%d", tpdURL, strings.Join(pks, ","), tpdDailyDays)

	resp, err := statsHTTPGet(url)
	if err != nil {
		return fmt.Errorf("TPD per-visor metric request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TPD returned status %d for per-visor metrics", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read TPD per-visor metrics: %w", err)
	}

	var raw map[string]struct {
		Daily []struct {
			Date      string `json:"date"`
			Bandwidth *struct {
				Total uint64 `json:"total"`
			} `json:"bandwidth,omitempty"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("failed to parse TPD per-visor metrics: %w", err)
	}

	// TPD pads the response out to the requested window with date entries that
	// carry no bandwidth object at all. Charting those as zero would assert
	// that the network moved nothing on days TPD simply has no record for, so
	// the window is trimmed to the span that actually reported. Same rule as
	// the network aggregate: report what exists, do not pad.
	reported := make(map[string]struct{})
	for _, v := range raw {
		for _, d := range v.Daily {
			if d.Bandwidth != nil {
				reported[d.Date] = struct{}{}
			}
		}
	}
	if len(reported) == 0 {
		return fmt.Errorf("TPD reported no per-visor bandwidth over the last %d days", tpdDailyDays)
	}
	dates := make([]string, 0, len(reported))
	for d := range reported {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	out.Dates = dates

	for i := range out.Visors {
		series, ok := raw[out.Visors[i].PK]
		if !ok {
			continue
		}
		byDate := make(map[string]uint64, len(series.Daily))
		for _, d := range series.Daily {
			if d.Bandwidth != nil {
				byDate[d.Date] = d.Bandwidth.Total
				out.Visors[i].Reported = true
			}
		}
		for _, d := range dates {
			out.Visors[i].Daily = append(out.Visors[i].Daily, visorBWPoint{Date: d, Total: byDate[d]})
			out.Visors[i].Total += byDate[d]
		}
	}
	out.BandwidthOK = true
	return nil
}

// renderVisorBandwidthHTML fetches and renders the per-visor page body.
func renderVisorBandwidthHTML() string {
	data, err := fetchVisorBandwidth()
	if err != nil {
		return fmt.Sprintf("<p style='color:#FF6384;'>Per-visor bandwidth unavailable: %s</p>",
			html.EscapeString(err.Error()))
	}
	return renderVisorBandwidthBody(data)
}

// renderVisorBandwidthBody renders an already-fetched payload.
func renderVisorBandwidthBody(data *visorBandwidthData) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<p>The %d most-connected visors of %d registered with Transport Discovery, "+
		"ranked by transport count, with their measured daily bandwidth.</p>", len(data.Visors), data.TotalVisors)
	sb.WriteString("<p style='color:#888;font-size:11px;'>TPD publishes no bandwidth-ranked visor index, " +
		"and ranking the whole network by bandwidth would mean a per-visor query for every registered key. " +
		"The selection here is therefore by <b>transport count</b> — these are the best-connected visors, " +
		"not necessarily the highest-bandwidth ones. Bandwidth figures are TPD's own reported totals, " +
		"not the min()-verified figures used by the reward calculation.</p>")

	if !data.BandwidthOK {
		fmt.Fprintf(&sb, "<p style='color:#FF6384;'>Bandwidth series unavailable: %s — the transport-count ranking below is still current.</p>",
			html.EscapeString(data.BandwidthE))
		sb.WriteString(renderVisorRankTable(data, false))
		fmt.Fprintf(&sb, "<p style='color:#888;font-size:0.8em;'>Last updated: %s</p>", html.EscapeString(data.LastUpdated))
		return sb.String()
	}

	// Order bands by measured bandwidth so the chart reads largest-first.
	visors := make([]visorBWEntry, len(data.Visors))
	copy(visors, data.Visors)
	sort.Slice(visors, func(i, j int) bool {
		if visors[i].Total != visors[j].Total {
			return visors[i].Total > visors[j].Total
		}
		return visors[i].PK < visors[j].PK
	})

	series := make([]chartSeries, 0, len(visors))
	unreported := 0
	for _, v := range visors {
		if !v.Reported {
			unreported++
			continue
		}
		vals := make([]float64, len(data.Dates))
		for di, p := range v.Daily {
			if di < len(vals) {
				vals[di] = float64(p.Total)
			}
		}
		series = append(series, chartSeries{
			// Public keys are never truncated here; the legend wraps instead.
			Name:  v.PK,
			Color: chartColors[len(series)%len(chartColors)],
			Vals:  vals,
			Note:  formatBytesChart(v.Total) + " over the window",
		})
	}
	if len(series) == 0 {
		sb.WriteString("<p style='color:#FFCE56;'>None of the selected visors has reported bandwidth to Transport Discovery over this window.</p>")
		sb.WriteString(renderVisorRankTable(data, true))
		fmt.Fprintf(&sb, "<p style='color:#888;font-size:0.8em;'>Last updated: %s</p>", html.EscapeString(data.LastUpdated))
		return sb.String()
	}

	opts := chartOpts{
		Width: 900, Height: 320,
		Labels:     shortDates(data.Dates),
		Title:      fmt.Sprintf("Per-Visor Bandwidth (%d days reported)", len(data.Dates)),
		YAxisLabel: "daily bytes per visor, stacked",
		FormatY:    func(v float64) string { return formatBytesChart(uint64(v)) },
	}
	sb.WriteString(renderStackedAreaSVG(opts, series))
	if unreported > 0 {
		fmt.Fprintf(&sb, "<p style='color:#888;font-size:11px;'>%d of the %d selected visors reported no bandwidth to "+
			"Transport Discovery over this window and are listed below as <i>not reported</i> rather than charted as zero.</p>",
			unreported, len(visors))
	}
	sb.WriteString(renderVisorRankTable(&visorBandwidthData{Visors: visors, TotalVisors: data.TotalVisors}, true))
	fmt.Fprintf(&sb, "<p style='color:#888;font-size:0.8em;'>Last updated: %s</p>", html.EscapeString(data.LastUpdated))
	return sb.String()
}

// renderVisorRankTable lists the selected visors. withBandwidth is false when
// the bandwidth fetch failed, so the column is omitted rather than filled with
// zeroes that would read as measurements.
func renderVisorRankTable(data *visorBandwidthData, withBandwidth bool) string {
	var sb strings.Builder
	sb.WriteString("<pre>")
	if withBandwidth {
		fmt.Fprintf(&sb, "%-4s %-66s %-11s %s\n", "#", "Public Key", "Transports", "Bandwidth")
	} else {
		fmt.Fprintf(&sb, "%-4s %-66s %s\n", "#", "Public Key", "Transports")
	}
	for i, v := range data.Visors {
		if withBandwidth {
			bw := "not reported"
			if v.Reported {
				bw = formatBytesChart(v.Total)
			}
			fmt.Fprintf(&sb, "%-4d %-66s %-11d %s\n", i+1, html.EscapeString(v.PK), v.Transports, bw)
			continue
		}
		fmt.Fprintf(&sb, "%-4d %-66s %d\n", i+1, html.EscapeString(v.PK), v.Transports)
	}
	sb.WriteString("</pre>")
	return sb.String()
}
