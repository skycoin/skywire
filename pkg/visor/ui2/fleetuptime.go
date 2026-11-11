// Package ui2 pkg/visor/ui2/fleetuptime.go c3-vis-core
package ui2

import (
	"sort"
	"strconv"
	"time"
)

// The uptime of every visor, from the transport discovery's uptime data,
// as multi-visor-uptime.component.ts draws it: one bar per visor of hour
// blocks shaded by how many of the hour's 5 minute slots were up.

// FleetSummary is one visor of /api/network/visor-uptime.
type FleetSummary struct {
	PK       string            `json:"pk"`
	On       bool              `json:"on"`
	Version  string            `json:"version"`
	Daily    map[string]string `json:"daily"`
	Timeline map[string]string `json:"timeline"`
}

// FleetUptime is the page.
type FleetUptime struct {
	Days     int
	All      bool
	Rows     []FleetRow
	Ticks    []FleetTick
	Err      string
	Fetched  string
	Blocks   int
	Managed  int
	Reported int
}

// FleetRow is one visor's bar.
type FleetRow struct {
	PK        string
	Online    bool
	Version   string
	Managed   bool
	Runs      []Run
	WindowPct float64
	RecentPct float64
}

// FleetTick marks a day boundary along the bars.
type FleetTick struct {
	Label string
	Left  string
}

const slotsPerHour = 12

// block is an hour: its up slot count, or -1 for an hour still to come.
type block = int

func hourBlocks(s FleetSummary, dates []string, today string, nowSlot int) []block {
	out := make([]block, 0, len(dates)*24)
	nowHour := nowSlot / slotsPerHour
	for _, d := range dates {
		line := s.Timeline[d]
		for h := range 24 {
			start, end := h*slotsPerHour, (h+1)*slotsPerHour
			if d == today && h > nowHour {
				out = append(out, -1)
				continue
			}
			if d == today && h == nowHour {
				end = min(end, nowSlot)
			}
			n := 0
			for i := start; i < end && i < len(line); i++ {
				if line[i] == '.' {
					n++
				}
			}
			out = append(out, n)
		}
	}
	return out
}

func blockLevel(b block) string {
	switch {
	case b < 0:
		return "future"
	case b == 0:
		return "lvl0"
	case b <= 3:
		return "lvl1"
	case b <= 6:
		return "lvl2"
	case b <= 9:
		return "lvl3"
	}
	return "lvl4"
}

// FleetUptimeOf lays out the bars. managed are the visors this hypervisor
// manages; unless all is set only they are shown.
func FleetUptimeOf(sums []FleetSummary, managed map[string]bool, all bool, days int, now time.Time) FleetUptime {
	f := FleetUptime{Days: days, All: all, Reported: len(sums)}
	dateSet := map[string]bool{}
	for _, s := range sums {
		for d := range s.Timeline {
			dateSet[d] = true
		}
		for d := range s.Daily {
			dateSet[d] = true
		}
	}
	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	now = now.UTC()
	today := now.Format(time.DateOnly)
	nowSlot := (now.Hour()*60 + now.Minute()) / 5

	all2 := make([][]block, len(sums))
	for i, s := range sums {
		all2[i] = hourBlocks(s, dates, today, nowSlot)
	}
	// Trim the hours no visor has data for at both ends, so the bars
	// line up on the data, as the CLI and the Angular page do.
	n := 0
	if len(all2) > 0 {
		n = len(all2[0])
	}
	empty := func(i int) bool {
		for _, bs := range all2 {
			if bs[i] != 0 {
				return false
			}
		}
		return true
	}
	lead, trail := 0, 0
	for lead < n && empty(lead) {
		lead++
	}
	for trail < n-lead && empty(n-1-trail) {
		trail++
	}
	f.Blocks = n - lead - trail
	for i, s := range sums {
		bs := all2[i][lead : n-trail]
		r := FleetRow{PK: s.PK, Online: s.On, Version: s.Version, Managed: managed[s.PK]}
		if r.Managed {
			f.Managed++
		}
		if !all && !r.Managed {
			continue
		}
		up, known := 0, 0
		for j, b := range bs {
			if b >= 0 {
				up += b
				known += slotsPerHour
			}
			lvl := blockLevel(b)
			if k := len(r.Runs) - 1; k >= 0 && r.Runs[k].State == lvl {
				r.Runs[k].N++
			} else {
				r.Runs = append(r.Runs, Run{State: lvl, N: 1, From: strconv.Itoa(j)})
			}
		}
		if known > 0 {
			r.WindowPct = float64(up) / float64(known) * 100
		}
		r.RecentPct = recentPct(s.Daily, today)
		f.Rows = append(f.Rows, r)
	}
	sort.SliceStable(f.Rows, func(i, j int) bool { return f.Rows[i].WindowPct > f.Rows[j].WindowPct })
	if f.Blocks >= 48 {
		for i, d := 24, 1; i < f.Blocks; i, d = i+24, d+1 {
			f.Ticks = append(f.Ticks, FleetTick{Label: "+" + strconv.Itoa(d) + "d", Left: strconv.FormatFloat(float64(i)/float64(f.Blocks)*100, 'f', 2, 64) + "%"})
		}
	}
	return f
}

// recentPct is today's daily figure, else the latest day's.
func recentPct(daily map[string]string, today string) float64 {
	if v, err := strconv.ParseFloat(daily[today], 64); err == nil {
		return v
	}
	ds := make([]string, 0, len(daily))
	for d := range daily {
		ds = append(ds, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ds)))
	for _, d := range ds {
		if v, err := strconv.ParseFloat(daily[d], 64); err == nil {
			return v
		}
	}
	return 0
}

// FleetPct prints a percentage as the Angular page does.
func FleetPct(p float64) string {
	switch {
	case p >= 99.95:
		return "100%"
	case p > 0 && p < 1:
		return "<1%"
	}
	return strconv.FormatFloat(p, 'f', 1, 64) + "%"
}

func fleetTone(p float64) string {
	switch {
	case p >= 99:
		return "ok"
	case p >= 80:
		return "warn-text"
	}
	return "error"
}
