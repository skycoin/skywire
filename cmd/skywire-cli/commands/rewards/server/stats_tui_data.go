// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_data.go c5-reward-server
//
// Data gathering for the terminal-rendered statistics panel.
//
// Every source is fetched independently and every failure is recorded rather
// than returned: the page renders whatever succeeded and names what did not.
// That matters here more than usual, because the previous version reduced a
// 24 MB per-transport body to produce three numbers and, when that fetch died
// with EOF, the whole summary rendered as an error string.
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/skycoin/skywire/deployment"
)

// gatherStatsTUI collects everything the panel draws. It never returns an
// error: a failed source becomes a named absence in the rendering.
func gatherStatsTUI() statsTUIData {
	var d statsTUIData
	tpdURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")

	// Counts — 138 bytes. The cheap aggregate, not a reduction of the bulk
	// bodies.
	var stats struct {
		TotalTransports int            `json:"total_transports"`
		ByType          map[string]int `json:"by_type"`
		UniqueVisors    int            `json:"unique_visors"`
	}
	if err := statsGetJSON(tpdURL+"/all-transports/stats", &stats); err != nil {
		d.CountsErr = err.Error()
	} else {
		d.Transports, d.ByType, d.UniqueVisors = stats.TotalTransports, stats.ByType, stats.UniqueVisors
	}

	// Daily bandwidth and latency — 2.7 KB for the whole series, against the
	// tens of megabytes the per-transport route costs for the same shape.
	var metric struct {
		Daily []struct {
			Date      string  `json:"date"`
			Bandwidth uint64  `json:"bandwidth"`
			Latency   float64 `json:"latency"`
			ByType    map[string]struct {
				Bandwidth uint64 `json:"bandwidth"`
			} `json:"by_type"`
		} `json:"daily"`
	}
	if err := statsGetJSON(fmt.Sprintf("%s/metric?days=30", tpdURL), &metric); err != nil {
		d.DailyErr = err.Error()
	} else {
		// TPD returns newest-first; a time axis has to run oldest to newest or
		// every chart reads backwards.
		for i := len(metric.Daily) - 1; i >= 0; i-- {
			m := metric.Daily[i]
			day := statsTUIDay{Date: m.Date, Bandwidth: m.Bandwidth, Latency: m.Latency,
				ByType: make(map[string]uint64, len(m.ByType))}
			for t, v := range m.ByType {
				day.ByType[t] = v.Bandwidth
			}
			d.Daily = append(d.Daily, day)
		}
	}

	// Fleet version histogram — 300 bytes.
	versions := map[string]int{}
	if err := statsGetJSON(tpdURL+"/version", &versions); err != nil {
		d.VersionsErr = err.Error()
	} else {
		d.Versions = versions
	}

	// Visors online, summed from the uptime tracker's per-5-minute timelines.
	// Shares the cache the SVG chart uses, so the ~2 MB tracker dump is pulled
	// once for both renderings rather than twice.
	//
	// This was missing when the panel first shipped: the field existed and the
	// renderer drew it, but nothing populated it, so the section vanished with
	// no error — the one failure mode the design is supposed to make
	// impossible. An unattempted source is now an explicit one.
	if series, err := fetchLivenessSeries(); err != nil {
		d.LivenessErr = err.Error()
	} else {
		d.Liveness = livenessToTUIDays(series)
	}

	d.Dmsg = gatherDmsgStats()
	d.Coverage = gatherServiceCoverage()

	return d
}

// livenessToTUIDays regroups the flat slot series into per-day runs, which is
// the shape the panel's x-axis labels are built from.
func livenessToTUIDays(s *livenessSeries) []statsTUILivenessDay {
	if s == nil || len(s.Counts) == 0 {
		return nil
	}
	var out []statsTUILivenessDay
	for i, c := range s.Counts {
		date := ""
		if i < len(s.Dates) {
			date = s.Dates[i]
		}
		if len(out) == 0 || out[len(out)-1].Date != date {
			out = append(out, statsTUILivenessDay{Date: date})
		}
		out[len(out)-1].Slots = append(out[len(out)-1].Slots, c)
	}
	return out
}

// statsGetJSON fetches and decodes one JSON body. Decoding is the validity
// test: a body that arrives truncated under a 200 — which large reads through
// dmsg have repeatedly done — fails to parse and is reported, never partially
// believed.
func statsGetJSON(url string, v any) error {
	resp, err := statsHTTPGet(url)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read failed: %w", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("parse failed: %w", err)
	}
	return nil
}
