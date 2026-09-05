// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/liveness.go c4-vis-cli
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
)

// Visors online over time, at five-minute resolution.
//
// The uptime tracker already records this and nothing displayed it: `ut tpd
// graph --json` returns, per visor, a per-day timeline string of 288 slots —
// one per five minutes — where '.' marks the visor as online. Summing a column
// across every visor gives the number online in that slot, a week of network
// liveness at 5-minute resolution.

const (
	// livenessSlotsPerDay is the timeline's fixed resolution: 288 five-minute
	// slots covering 24 hours.
	livenessSlotsPerDay = 288
	// livenessOnline is the character the tracker writes for an online slot.
	livenessOnline = '.'
	// livenessCacheMaxAge bounds how often the ~2 MB tracker dump is pulled.
	livenessCacheMaxAge = 10 * time.Minute
)

// livenessSeries is the summed column counts plus the labels for each slot.
type livenessSeries struct {
	// Counts[i] is the number of visors online in slot i, oldest first.
	Counts []int
	// Dates[i] is the calendar date slot i belongs to.
	Dates []string
	// DayStarts indexes the first slot of each day, for the x-axis ticks.
	DayStarts []int
	// Visors is how many visors carried a timeline at all.
	Visors int
}

// livenessCache memoizes the tracker dump; it is megabytes and the underlying
// data only advances one five-minute slot at a time.
var livenessCache struct {
	sync.Mutex
	at     time.Time
	series *livenessSeries
	err    error
}

// utGraphVisor is one visor's entry in `ut tpd graph --json`.
type utGraphVisor struct {
	PK string `json:"pk"`
	// Timeline maps a date to a 288-character slot string.
	Timeline map[string]string `json:"timeline"`
}

// fetchLivenessSeries pulls the tracker's timelines and sums them by slot.
func fetchLivenessSeries() (*livenessSeries, error) {
	livenessCache.Lock()
	defer livenessCache.Unlock()
	if livenessCache.series != nil && time.Since(livenessCache.at) <= livenessCacheMaxAge {
		return livenessCache.series, nil
	}
	if livenessCache.err != nil && time.Since(livenessCache.at) <= time.Minute {
		return nil, livenessCache.err
	}

	out, err := script.Exec(`skywire cli ut tpd graph --json`).String()
	if err != nil {
		livenessCache.at, livenessCache.err = time.Now(), fmt.Errorf("uptime tracker query failed: %w", err)
		return nil, livenessCache.err
	}
	series, err := parseLivenessSeries([]byte(out))
	livenessCache.at = time.Now()
	if err != nil {
		livenessCache.err = err
		return nil, err
	}
	livenessCache.series, livenessCache.err = series, nil
	return series, nil
}

// parseLivenessSeries sums the per-visor timelines into one series.
func parseLivenessSeries(raw []byte) (*livenessSeries, error) {
	var visors []utGraphVisor
	if err := json.Unmarshal(raw, &visors); err != nil {
		return nil, fmt.Errorf("failed to parse uptime tracker graph: %w", err)
	}

	// Collect the dates present, sorted, so the series reads left to right.
	dateSet := make(map[string]struct{})
	counted := 0
	for _, v := range visors {
		if len(v.Timeline) == 0 {
			continue
		}
		counted++
		for d := range v.Timeline {
			dateSet[d] = struct{}{}
		}
	}
	if counted == 0 {
		return nil, fmt.Errorf("uptime tracker returned no visor timelines")
	}
	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	dayIndex := make(map[string]int, len(dates))
	for i, d := range dates {
		dayIndex[d] = i
	}
	counts := make([]int, len(dates)*livenessSlotsPerDay)
	for _, v := range visors {
		for d, tl := range v.Timeline {
			base, ok := dayIndex[d]
			if !ok {
				continue
			}
			base *= livenessSlotsPerDay
			for i := 0; i < len(tl) && i < livenessSlotsPerDay; i++ {
				if tl[i] == livenessOnline {
					counts[base+i]++
				}
			}
		}
	}

	// The current day's timeline is pre-allocated for a full 24 hours, so every
	// slot after "now" is a zero that has not happened yet. Charting those
	// would show the network going dark at a midnight that has not arrived.
	// Trim the trailing run of zeroes; the series then ends at the last slot
	// that was actually observed.
	end := len(counts)
	for end > 0 && counts[end-1] == 0 {
		end--
	}
	if end == 0 {
		return nil, fmt.Errorf("uptime tracker timelines recorded no online slots")
	}
	counts = counts[:end]

	slotDates := make([]string, len(counts))
	var dayStarts []int
	for i := range counts {
		day := i / livenessSlotsPerDay
		slotDates[i] = dates[day]
		if i%livenessSlotsPerDay == 0 {
			dayStarts = append(dayStarts, i)
		}
	}
	return &livenessSeries{Counts: counts, Dates: slotDates, DayStarts: dayStarts, Visors: counted}, nil
}

// renderLivenessChart draws visors-online over time.
//
// Deliberately labeled "visors online", never "uptime": the timeline disagrees
// with the same endpoint's daily percentage for intermittent visors (#4533), so
// this is a liveness count and must not be read as the uptime figure the reward
// calculation uses.
func renderLivenessChart(s *livenessSeries, err error) string {
	if err != nil {
		return fmt.Sprintf("<h3>Visors Online</h3><p style='color:#FF6384;'>Visors-online series unavailable: %s</p>",
			html.EscapeString(err.Error()))
	}

	vals := make([]float64, len(s.Counts))
	minOn, maxOn := s.Counts[0], s.Counts[0]
	for i, c := range s.Counts {
		vals[i] = float64(c)
		if c < minOn {
			minOn = c
		}
		if c > maxOn {
			maxOn = c
		}
	}

	labels := make([]string, len(s.Dates))
	copy(labels, s.Dates)
	for i := range labels {
		labels[i] = shortSlotLabel(labels[i], i)
	}

	opts := chartOpts{
		Width: 900, Height: 260,
		Labels:  labels,
		XTicks:  s.DayStarts,
		Title:   fmt.Sprintf("Visors Online (%d days, 5-minute resolution)", len(s.DayStarts)),
		FormatY: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		YAxisLabel: fmt.Sprintf("visors reporting online, of %d with a recorded timeline — range %d to %d",
			s.Visors, minOn, maxOn),
	}
	out := renderLineSVG(opts, []chartSeries{{
		Name:  "visors online",
		Color: "#4BC0C0",
		Vals:  vals,
		Note:  fmt.Sprintf("%d–%d online", minOn, maxOn),
	}}, nil)
	out += "<p style='color:#888;font-size:11px;'>Source: uptime tracker per-visor timelines " +
		"(<code>ut tpd graph</code>), 288 five-minute slots per day, summed across visors. " +
		"This is a <b>liveness count, not uptime</b>: the timeline disagrees with the same endpoint's " +
		"daily percentage for intermittent visors (skywire#4533), and the reward calculation uses the " +
		"daily percentage. The current day is trimmed at the last observed slot rather than padded to midnight.</p>"
	return out
}

// shortSlotLabel keeps the year on the first tick only.
func shortSlotLabel(date string, i int) string {
	if i == 0 {
		return date
	}
	parts := strings.Split(date, "-")
	if len(parts) == 3 {
		return parts[1] + "-" + parts[2]
	}
	return date
}
