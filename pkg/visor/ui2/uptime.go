// Package ui2 pkg/visor/ui2/uptime.go c3-vis-core
package ui2

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// UptimeTiers is the order the Angular tab draws the tiers in.
var UptimeTiers = []string{"process", "dmsg", "skynet"}

// slotsPerDay is the 5-minute slots in a day of a tier bitmap.
const slotsPerDay = 288

// Uptime is the uptime tab.
type Uptime struct {
	PK      string
	Days    int // the window, 1, 7 or 30
	List    []UpDay
	Summary []TierPct
	Fetched string
	Err     string
}

// UpDay is one UTC day, newest first.
type UpDay struct {
	Date  string
	Today bool
	Tiers []UpTier
}

// UpTier is one tier's bar for a day, as runs of the same state.
type UpTier struct {
	Name  string
	Runs  []Run
	Pct   float64
	Empty bool
}

// Run is a stretch of slots in one state: on, off or future.
type Run struct {
	State    string
	N        int
	From, To string
}

// TierPct is a tier's share of known slots online over the window.
type TierPct struct {
	Name string
	Pct  float64
}

// UptimeOf lays out the visor's tier bitmaps. A '.' slot was up; a slot of
// today at or after now has not happened yet.
func UptimeOf(resp *visorapi.LocalUptimeResponse, now time.Time) ([]UpDay, []TierPct) {
	if resp == nil {
		return nil, nil
	}
	now = now.UTC()
	today := now.Format(time.DateOnly)
	nowSlot := (now.Hour()*60 + now.Minute()) / 5
	dates := map[string]bool{}
	for _, name := range UptimeTiers {
		for d := range resp.Tiers[name] {
			dates[d] = true
		}
	}
	sorted := make([]string, 0, len(dates))
	for d := range dates {
		sorted = append(sorted, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))
	on, known := map[string]int{}, map[string]int{}
	var days []UpDay
	for _, date := range sorted {
		day := UpDay{Date: date, Today: date == today}
		for _, name := range UptimeTiers {
			ascii := resp.Tiers[name][date]
			t := UpTier{Name: name, Empty: ascii == ""}
			up, kn := 0, 0
			for i := 0; i < slotsPerDay; i++ {
				st := "off"
				switch {
				case day.Today && i >= nowSlot:
					st = "future"
				case i < len(ascii) && ascii[i] == '.':
					st = "on"
				}
				if st != "future" {
					kn++
					if st == "on" {
						up++
					}
				}
				if n := len(t.Runs); n > 0 && t.Runs[n-1].State == st {
					t.Runs[n-1].N++
					t.Runs[n-1].To = slotTime(i + 1)
					continue
				}
				t.Runs = append(t.Runs, Run{State: st, N: 1, From: slotTime(i), To: slotTime(i + 1)})
			}
			if kn > 0 {
				t.Pct = float64(up) / float64(kn) * 100
			}
			on[name] += up
			known[name] += kn
			day.Tiers = append(day.Tiers, t)
		}
		days = append(days, day)
	}
	sum := make([]TierPct, 0, len(UptimeTiers))
	for _, name := range UptimeTiers {
		p := 0.0
		if known[name] > 0 {
			p = float64(on[name]) / float64(known[name]) * 100
		}
		sum = append(sum, TierPct{name, p})
	}
	return days, sum
}

func slotTime(i int) string {
	m := i * 5
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// Pct prints a percentage with one decimal.
func Pct(p float64) string { return fmt.Sprintf("%.1f%%", p) }

// PctClass colors a percentage: good, fair or poor.
func PctClass(p float64) string {
	switch {
	case p >= 90:
		return "ok"
	case p >= 50:
		return "warn-text"
	}
	return "error"
}

// ServiceUptime is one deployment service's daily uptime of the visor.
type ServiceUptime struct {
	Service string
	Days    []TierPct // Name is the date; newest first
	Err     string
}

// ParseServiceUptimes picks the visor's daily percentages out of a service's
// /uptimes?v=v2 answer, keeping the newest seven days.
func ParseServiceUptimes(body []byte, pk string) ([]TierPct, error) {
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(body, &entries); err != nil {
		var wrapped struct {
			Uptimes []map[string]json.RawMessage `json:"uptimes"`
			Data    []map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &wrapped) != nil {
			return nil, err
		}
		entries = append(wrapped.Uptimes, wrapped.Data...)
	}
	for _, e := range entries {
		var k string
		if json.Unmarshal(e["pk"], &k) != nil || !strings.EqualFold(k, pk) {
			if json.Unmarshal(e["public_key"], &k) != nil || !strings.EqualFold(k, pk) {
				continue
			}
		}
		raw := e["daily"]
		if raw == nil {
			raw = e["percent_daily"]
		}
		daily := map[string]any{}
		_ = json.Unmarshal(raw, &daily) //nolint:errcheck
		dates := make([]string, 0, len(daily))
		for d := range daily {
			dates = append(dates, d)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(dates)))
		var out []TierPct
		for i, d := range dates {
			if i == 7 {
				break
			}
			p, _ := strconv.ParseFloat(fmt.Sprint(daily[d]), 64) //nolint:errcheck
			out = append(out, TierPct{d, p})
		}
		return out, nil
	}
	return nil, nil
}
