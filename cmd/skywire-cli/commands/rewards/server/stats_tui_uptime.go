// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_uptime.go c5-reward-server
//
// `skywire cli ut tpd graph`, folded into the statistics panel.
//
// The command already renders the uptime tracker's v3 per-5-minute
// bitmaps as shaded hourly blocks, one bar per visor. That rendering
// existed only at a terminal; the site showed the same tracker data
// summed into a single visors-online line, which says how many were up
// and never which ones. A fleet holding steady at 880 online looks
// identical whether the same 880 visors are up all week or a churning
// 1,200 take turns, and only the per-visor bars tell those apart.
//
// TWO THINGS ARE DELIBERATE HERE.
//
// It does not shell out. The reward server's liveness chart ran
// `skywire cli ut tpd graph --json` as a subprocess. This file reads the
// SAME sources that command's fetch chain reads, in-process: the
// tpd-uptime CXO feed first (clirpc maps /uptimes?v=v3 onto exactly the
// uptimes/days/<n> leaf read below), then HTTP over dmsg. It cannot
// literally call clirpc — pkg/visor imports this package, so importing
// clirpc here is an import cycle — but it reads the same leaf and the
// same endpoint, and fetchLivenessSeries now shares the result, so the
// ~2 MB dump is pulled once for both renderings rather than once per
// subprocess.
//
// The glyphs are not re-implemented. ShadeForCount / RollingBar were
// hoisted into pkg/uptimestats (they had already been copy-pasted once,
// into cmd/skywire-cli/commands/visor/info.go), so the bars in this panel
// are byte-for-byte what the CLI prints.
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/uptimestats"
)

const (
	// uptimeGraphHours is the rolling window the panel draws. One block
	// per hour at 72 columns fits inside tuiWidth beside a two-space
	// indent, which is what fixes the number: a wider window would
	// either overflow the 80-column target or need the public key
	// truncated, and public keys are never truncated.
	uptimeGraphHours = 72
	// uptimeFetchMaxAge bounds how often the tracker dump is pulled. The
	// underlying data advances one five-minute slot at a time.
	uptimeFetchMaxAge = 10 * time.Minute
	// uptimeCXODays is the published window. TPD writes uptimes/days/<n>
	// for n in {1,7,30}; 30 is the bucket clirpc maps /uptimes?v=v3 onto,
	// so this reads the leaf the CLI would have read.
	uptimeCXODays = 30
)

// uptimeGraphRow is one visor's line, in the order the command emits.
type uptimeGraphRow struct {
	PK      string
	Version string
	Online  bool
	// Bar is uptimeGraphHours block glyphs, oldest hour on the left.
	Bar string
	// OnlineSlots of TotalSlots five-minute slots in the window.
	OnlineSlots, TotalSlots int
}

// uptimeGraphStats is the panel's data.
type uptimeGraphStats struct {
	Rows    []uptimeGraphRow
	Hours   int
	EndedAt time.Time
	Src     string
	Err     string
}

// uptimeEntriesCache memoizes the tracker dump across the two panels that
// read it. Megabytes, and both would otherwise pull it per page load.
var uptimeEntriesCache struct {
	sync.Mutex
	at      time.Time
	entries []uptimestats.VisorSummary
	src     string
	err     error
}

// fetchUptimeEntries pulls the v3 uptime dump, CXO first and HTTP over
// dmsg behind it — the same order, and the same two sources, the CLI's
// fetch chain uses for this URL. v3 is not optional: only v3 carries the
// per-5-minute bitmaps the bars are drawn from.
//
// Returns the entries and a source label, on the rule the rest of these
// panels follow: a snapshot minutes old and a fetch made just now are
// different claims and must not print identically.
func fetchUptimeEntries() ([]uptimestats.VisorSummary, string, error) {
	uptimeEntriesCache.Lock()
	defer uptimeEntriesCache.Unlock()
	if uptimeEntriesCache.entries != nil && time.Since(uptimeEntriesCache.at) <= uptimeFetchMaxAge {
		return uptimeEntriesCache.entries, uptimeEntriesCache.src, nil
	}
	// A failure is held only briefly: these failures are transient (a lost
	// dmsg session), and pinning one for ten minutes would blank both
	// panels long after the cause cleared.
	if uptimeEntriesCache.err != nil && time.Since(uptimeEntriesCache.at) <= time.Minute {
		return nil, "", uptimeEntriesCache.err
	}

	leaf := fmt.Sprintf("uptimes/days/%d", uptimeCXODays)
	entries, src, err := cxoUptimeEntries(leaf)
	if err != nil {
		httpURL := strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/") + "/uptimes?v=v3"
		label := httpStatsSource("/uptimes?v=v3", err.Error())
		var out []uptimestats.VisorSummary
		if hErr := statsGetJSON(httpURL, &out); hErr != nil {
			uptimeEntriesCache.at, uptimeEntriesCache.err = time.Now(), hErr
			return nil, "", hErr
		}
		entries, src = out, label.String()
	}

	uptimeEntriesCache.at = time.Now()
	if len(entries) == 0 {
		uptimeEntriesCache.err = fmt.Errorf("the uptime tracker returned no visors")
		return nil, "", uptimeEntriesCache.err
	}
	uptimeEntriesCache.entries, uptimeEntriesCache.src, uptimeEntriesCache.err = entries, src, nil
	return entries, src, nil
}

// cxoUptimeEntries reads one uptimes/days/<n> leaf off the tpd-uptime
// feed. Validated by parsing, never by size — reads through dmsg have
// repeatedly arrived truncated under a 200, and this is the largest body
// any of these panels touches.
func cxoUptimeEntries(leaf string) ([]uptimestats.VisorSummary, string, error) {
	body, ts, err := statsCXOFeedLeaf(cxosub.FeedTPDUptime, cxosub.TabUptime, leaf)
	if err != nil {
		return nil, "", err
	}
	var out []uptimestats.VisorSummary
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", fmt.Errorf("parse failed: %w", err)
	}
	if len(out) == 0 {
		return nil, "", fmt.Errorf("the uptime leaf carried no visors")
	}
	return out, statsSource{Via: "CXO", Path: leaf, At: ts}.String(), nil
}

// gatherUptimeGraph builds the rows. Ordering is the command's — sorted
// by public key — so a row here and a row from the CLI are the same row.
func gatherUptimeGraph() uptimeGraphStats {
	s := uptimeGraphStats{Hours: uptimeGraphHours}

	entries, src, err := fetchUptimeEntries()
	if err != nil {
		s.Err = err.Error()
		return s
	}
	now := time.Now().UTC()
	s.EndedAt = now
	start := now.Add(-time.Duration(uptimeGraphHours) * time.Hour)
	totalSlots := uptimeGraphHours * uptimestats.TimelineSlotsPerHour

	sorted := make([]uptimestats.VisorSummary, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PK.Hex() < sorted[j].PK.Hex() })

	for _, e := range sorted {
		if len(e.Timeline) == 0 {
			// A visor with no timeline has nothing to draw and a blank
			// bar would read as 72 hours offline, which is a different
			// claim from "the tracker holds no timeline for it".
			continue
		}
		s.Rows = append(s.Rows, uptimeGraphRow{
			PK:          e.PK.Hex(),
			Version:     e.Version,
			Online:      e.Online,
			Bar:         uptimestats.RollingBar(e.Timeline, now, uptimeGraphHours),
			OnlineSlots: uptimestats.CountOnlineSlots(e.Timeline, start, now),
			TotalSlots:  totalSlots,
		})
	}
	if len(s.Rows) == 0 {
		s.Err = "the uptime tracker returned no visor timelines"
		return s
	}
	s.Src = src + " — uptime tracker v3 timelines, the data `skywire cli ut tpd graph` renders"
	return s
}

// renderUptimeGraphPanelANSI draws the per-visor timeline bars.
//
// Two lines per visor rather than the command's one: the CLI prints
// "<pk> <bar>", which is 66 + 1 + 72 columns and does not fit the panel.
// Splitting the row keeps the whole public key — the dmsg panel splits
// its rows the same way and for the same reason.
func renderUptimeGraphPanelANSI(s uptimeGraphStats) string {
	const title = "UPTIME TIMELINE"
	width := tuiWidth

	if s.Err != "" {
		return tuiMissing(title, s.Err) + "\n"
	}
	if len(s.Rows) == 0 {
		return tuiMissing(title, "no data returned") + "\n"
	}

	var b strings.Builder
	b.WriteString(tuiRule(fmt.Sprintf("%s — ut tpd graph, last %dh", title, s.Hours), width))
	b.WriteString(fmt.Sprintf("  %sone block per hour, oldest left · %s%s\n",
		aDim, s.EndedAt.Format("2006-01-02 15:04 UTC"), aReset))
	b.WriteString(fmt.Sprintf("  %sdensity: ' ' none  ░ ≤3  ▒ ≤6  ▓ ≤9  █ 12 of 12 five-minute slots%s\n",
		aDim, aReset))
	b.WriteString(tuiHourRuler(s.Hours))

	full := 0
	for _, r := range s.Rows {
		pct := 0.0
		if r.TotalSlots > 0 {
			pct = 100 * float64(r.OnlineSlots) / float64(r.TotalSlots)
		}
		col := aGreen
		switch {
		case pct >= 99.5:
			full++
		case pct < 50:
			col = aRed
		case pct < 90:
			col = aYellow
		}
		ver := r.Version
		if ver == "" {
			ver = "—"
		}
		// The version rides on the key line, not the bar line: 72 blocks
		// plus a percentage is already the full panel width, and the
		// public key is never shortened to make room for anything.
		b.WriteString(fmt.Sprintf("  %s%s %s%s\n", aDim, r.PK, ver, aReset))
		// One SGR pair for the whole bar, not one per glyph: this panel
		// draws a line per visor and per-character color would multiply
		// the exported HTML by the span count for no added meaning.
		b.WriteString(fmt.Sprintf("  %s%s%s %s%3.0f%%%s\n",
			col, r.Bar, aReset, aBold, pct, aReset))
	}
	b.WriteString(tuiWrap(fmt.Sprintf("  %d visors with a timeline · %d up for the whole "+
		"window · liveness, NOT the uptime figure rewards use (skywire#4533)",
		len(s.Rows), full)))
	b.WriteString(tuiSource(s.Src))
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	return b.String()
}

// tuiHourRuler draws hour-offset ticks above the bars. Without it a block
// that is clearly bad tells you nothing about WHEN it was bad, which is
// the first question anyone asks of an outage.
func tuiHourRuler(hours int) string {
	if hours <= 0 {
		return ""
	}
	const step = 12
	ticks := []rune(strings.Repeat(" ", hours))
	labels := []rune(strings.Repeat(" ", hours))
	for off := hours; off >= 0; off -= step {
		col := hours - off
		if col >= hours {
			col = hours - 1
		}
		ticks[col] = '┬'
		lb := []rune(fmt.Sprintf("-%dh", off))
		if off == 0 {
			lb = []rune("now")
		}
		start := col
		if start+len(lb) > hours {
			start = hours - len(lb)
		}
		if start < 0 {
			continue
		}
		copy(labels[start:], lb)
	}
	return aDim + "  " + string(ticks) + aReset + "\n" +
		aDim + "  " + string(labels) + aReset + "\n"
}
