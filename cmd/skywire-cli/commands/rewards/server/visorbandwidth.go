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
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// Per-visor bandwidth history.
//
// The network-wide daily aggregate (/metric) carries no per-visor breakdown, so
// this page cannot be served from it.
//
// The bandwidth series now comes from the CXO per-transport metrics feed and
// there is NO per-visor request at all. TPD's day leaves carry each
// transport's `edges` and its per-day, per-edge sent/recv, and the store
// increments the per-visor daily rollup from exactly those same deltas — the
// reporter writes its own bytes into both places — so summing edge A's bytes
// under Edges[0] and edge B's under Edges[1] reproduces what
// /metric/visor/{pks} returns, computed locally.
//
// That deletes a request rather than shrinking one. The URL it replaces was
// ~1,400 characters (twenty 66-character public keys, comma-separated) and was
// failing with EOF over dmsg — not for its size, but because the reward
// server's dmsg client periodically loses its sessions and every request in
// that window dies (skycoin/skywire#4538). A subscriber reads a snapshot it
// already holds and has no equivalent failure.
//
// One number does still come over HTTP: the RANKING. TPD publishes no
// bandwidth-ranked visor index, and ranking the whole network by bandwidth
// would have meant a per-visor query per registered key. The cheap index that
// exists is /all-transports/per-key-stats (~100 KB) — transport COUNTS per
// visor — and it has no CXO feed: it is two orders of magnitude larger than
// the stats feed's other bodies and was deliberately left off that feed
// (see cxo_stats_publisher.go). So the page selects the most-connected visors
// by transport count and reports their real measured bandwidth, saying so
// rather than implying these are the network's top bandwidth carriers. When
// that one fetch fails, the ranking degrades to a count derived from the CXO
// leaf's edges — a near-equivalent, labeled as such — so the page still
// renders instead of collapsing to a single error line.

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
	// BandwidthSrc and RankSrc name where each half of the page came from.
	// The two halves can legitimately disagree in freshness — a CXO
	// snapshot minutes old alongside an HTTP fetch made just now — and the
	// page must not present them as one measurement taken at one time.
	BandwidthSrc string `json:"bandwidth_src,omitempty"`
	RankSrc      string `json:"rank_src,omitempty"`
}

// fetchVisorBandwidth builds the per-visor series, cached on disk.
func fetchVisorBandwidth() (*visorBandwidthData, error) {
	cachePath := filepath.Join(tempStatsPath, visorBWCacheFile)
	if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) <= visorBWCacheMaxAge {
		if data, rErr := os.ReadFile(cachePath); rErr == nil { //nolint:gosec
			var d visorBandwidthData
			if json.Unmarshal(data, &d) == nil && len(d.Visors) > 0 {
				// The stored sources describe how the body was ORIGINALLY
				// obtained; say the page is serving them from cache rather
				// than letting them read as current.
				age := statsAgeString(time.Since(info.ModTime()))
				d.RankSrc = cachedSourceLabel(age, d.RankSrc)
				d.BandwidthSrc = cachedSourceLabel(age, d.BandwidthSrc)
				return &d, nil
			}
		}
	}

	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")

	// One CXO read serves both halves of this page: the bandwidth series,
	// and — only if the per-key index fetch fails — the ranking too.
	metrics, metricsSrc, metricsErr := cxoTransportMetrics(tpdDailyDays)

	counts, rankSrc, err := visorTransportCounts(tpdURL, metrics)
	if err != nil {
		return nil, err
	}

	ranked := sortedMapKeys(counts)
	if len(ranked) > visorBWTopN {
		ranked = ranked[:visorBWTopN]
	}

	out := &visorBandwidthData{
		TotalVisors: len(counts),
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
		RankSrc:     rankSrc.String(),
	}
	for _, pk := range ranked {
		out.Visors = append(out.Visors, visorBWEntry{PK: pk, Transports: counts[pk]})
	}

	cxoMiss := "not attempted"
	if metricsErr != nil {
		cxoMiss = metricsErr.Error()
	} else if bwErr := fillVisorBandwidthFromMetrics(out, metrics); bwErr != nil {
		cxoMiss = bwErr.Error()
	} else {
		out.BandwidthSrc = metricsSrc.String()
	}
	// Fall back to the per-visor HTTP query only when the local derivation
	// produced nothing. It is the request this page exists to stop making,
	// so it is the last resort rather than the first.
	if !out.BandwidthOK {
		if bwErr := fillVisorBandwidth(tpdURL, out); bwErr != nil {
			out.BandwidthE = bwErr.Error()
		} else {
			out.BandwidthSrc = httpStatsSource("/metric/visor/{pks}", cxoMiss).String()
		}
	}

	if data, mErr := json.Marshal(out); mErr == nil {
		os.WriteFile(cachePath, data, 0600) //nolint:errcheck,gosec
	}
	return out, nil
}

// visorTransportCounts returns the per-visor transport counts the ranking
// is built on.
//
// HTTP is tried FIRST here, against the CXO-first rule everywhere else in
// this file, and deliberately: /all-transports/per-key-stats counts
// REGISTERED transports and no CXO feed carries that number. The
// derivation below counts transports that appear in the metrics window,
// which is a near-equivalent but not the same set — a transport with no
// metrics row is missing from it. Using it as the primary would silently
// change what the column means; using it as a fallback keeps the page
// rendering when the index fetch dies, with the substitution labeled.
func visorTransportCounts(tpdURL string, metrics []tpdstore.TransportMetric) (map[string]int, statsSource, error) {
	counts, err := fetchPerKeyTransportCounts(tpdURL)
	if err == nil && len(counts) > 0 {
		return counts, statsSource{Via: "HTTP over dmsg", Path: "/all-transports/per-key-stats"}, nil
	}
	why := "carried no visors"
	if err != nil {
		why = err.Error()
	}
	if len(metrics) == 0 {
		return nil, statsSource{}, fmt.Errorf("TPD per-key stats unavailable (%s) and the CXO metrics feed is cold", why)
	}
	derived := make(map[string]int)
	for i := range metrics {
		for _, edge := range metrics[i].Edges {
			derived[edge]++
		}
	}
	if len(derived) == 0 {
		return nil, statsSource{}, fmt.Errorf("TPD per-key stats unavailable (%s) and the CXO metrics feed named no edges", why)
	}
	return derived, statsSource{
		Via:  "CXO",
		Path: tpdstore.MetricsDayPrefix + "* (edges)",
		Note: "per-key index unavailable (" + why + "); counts are transports SEEN IN THE METRICS WINDOW, not registered transports",
	}, nil
}

// fillVisorBandwidthFromMetrics sums the selected visors' daily bandwidth out
// of the per-transport records, with no request of any kind.
//
// Edge A is Edges[0] and edge B is Edges[1] — the store keys the per-day hash
// by reporter public key and reads it back in that order — and each reporter's
// deltas are written to the per-transport hash and to its own per-visor daily
// rollup in the same pipeline. So this sum and GET /metric/visor/{pks} are the
// same arithmetic over the same bytes.
//
// The one divergence is pre-per-edge data. A day whose hash holds only the
// combined `bandwidth` field is read back as an even A/B split, whereas the
// per-visor rollup recorded the reporter's real sent/recv; on those rows this
// derivation attributes half to each edge. Such rows expire at 35 days and the
// window here is 30.
func fillVisorBandwidthFromMetrics(out *visorBandwidthData, metrics []tpdstore.TransportMetric) error {
	selected := make(map[string]struct{}, len(out.Visors))
	for _, v := range out.Visors {
		selected[v.PK] = struct{}{}
	}

	// pk -> date -> bytes, for the selected visors only.
	byPK := make(map[string]map[string]uint64, len(selected))
	add := func(pk, date string, n uint64) {
		if n == 0 {
			return
		}
		if _, want := selected[pk]; !want {
			return
		}
		days, ok := byPK[pk]
		if !ok {
			days = make(map[string]uint64)
			byPK[pk] = days
		}
		days[date] += n
	}
	for i := range metrics {
		m := &metrics[i]
		if len(m.Edges) != 2 {
			continue
		}
		for _, d := range m.Daily {
			if d.A != nil {
				add(m.Edges[0], d.Date, d.A.Sent+d.A.Recv)
			}
			if d.B != nil {
				add(m.Edges[1], d.Date, d.B.Sent+d.B.Recv)
			}
		}
	}

	// Report the days that actually carry a measurement, never a window
	// padded out to the requested length: a zero on a day TPD has no record
	// for asserts the network moved nothing, which is a different claim.
	reported := make(map[string]struct{})
	for _, days := range byPK {
		for date := range days {
			reported[date] = struct{}{}
		}
	}
	if len(reported) == 0 {
		return fmt.Errorf("the CXO metrics window carries no bandwidth for the selected visors")
	}
	dates := make([]string, 0, len(reported))
	for d := range reported {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	out.Dates = dates

	for i := range out.Visors {
		days := byPK[out.Visors[i].PK]
		out.Visors[i].Daily = nil
		out.Visors[i].Total = 0
		out.Visors[i].Reported = len(days) > 0
		for _, d := range dates {
			out.Visors[i].Daily = append(out.Visors[i].Daily, visorBWPoint{Date: d, Total: days[d]})
			out.Visors[i].Total += days[d]
		}
	}
	out.BandwidthOK = true
	return nil
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
	sb.WriteString(renderVisorBandwidthSources(data))

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

// renderVisorBandwidthSources prints where each half of the page came from.
// Two figures fetched over different transports at different times are two
// measurements, and the page says so rather than letting the layout imply one.
func renderVisorBandwidthSources(data *visorBandwidthData) string {
	var sb strings.Builder
	for _, line := range []struct{ label, src string }{
		{"ranking", data.RankSrc},
		{"bandwidth", data.BandwidthSrc},
	} {
		if line.src == "" {
			continue
		}
		fmt.Fprintf(&sb, "<p style='color:#888;font-size:11px;'>%s — %s</p>",
			line.label, html.EscapeString(line.src))
	}
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
