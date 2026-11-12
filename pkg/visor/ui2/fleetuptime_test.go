package ui2

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFleetUptimeOf(t *testing.T) {
	now := time.Date(2026, 11, 11, 2, 30, 0, 0, time.UTC) // slot 30, hour 2
	full := strings.Repeat(".", 288)
	sums := []FleetSummary{
		{PK: "a", On: true, Daily: map[string]string{"2026-11-10": "100.00", "2026-11-11": "99.50"},
			Timeline: map[string]string{"2026-11-10": full, "2026-11-11": full}},
		{PK: "b", Daily: map[string]string{"2026-11-10": "50.00"},
			Timeline: map[string]string{"2026-11-10": strings.Repeat(" ", 144) + strings.Repeat(".", 144)}},
	}
	f := FleetUptimeOf(sums, map[string]bool{"a": true}, false, 7, now)
	require.Len(t, f.Rows, 1, "only managed visors unless all is asked for")
	require.Equal(t, 99.5, f.Rows[0].RecentPct)
	require.Equal(t, 1, f.Managed)

	f = FleetUptimeOf(sums, map[string]bool{"a": true}, true, 7, now)
	require.Len(t, f.Rows, 2)
	require.Equal(t, "a", f.Rows[0].PK, "best window first")
	require.Equal(t, 48, f.Blocks, "nothing to trim: a has data from the first hour and the future hours stay")
	a := f.Rows[0]
	require.Equal(t, "future", a.Runs[len(a.Runs)-1].State)
	require.Equal(t, 21, a.Runs[len(a.Runs)-1].N, "hours 3 to 23 of today are still to come")
	require.InDelta(t, 318.0/324*100, a.WindowPct, 0.01, "the current hour counts its slots so far against a whole hour, as Angular does")
	require.Equal(t, 50.0, f.Rows[1].RecentPct, "the latest day when today has none")
	require.Equal(t, "<1%", FleetPct(0.5))
	require.Equal(t, "100%", FleetPct(99.96))
}

func TestVersionHistoryOf(t *testing.T) {
	sums := []FleetSummary{
		{PK: "a", Version: "v1.3.110+dirty", Daily: map[string]string{"2026-11-10": "100", "2026-11-11": "80"}},
		{PK: "b", Version: "v1.3.109", Daily: map[string]string{"2026-11-10": "90", "2026-11-11": "10"}},
		{PK: "c", Version: "v1.3.110", Daily: map[string]string{"2026-11-11": "99"}},
	}
	h := VersionHistoryOf(sums)
	require.Len(t, h.Areas, 2)
	require.Equal(t, "v1.3.110", h.Areas[0].Version, "newest version first, dirty folded in")
	require.Equal(t, 2, h.Areas[0].Latest)
	require.Equal(t, 0, h.Areas[1].Latest, "b fell under 75% on the last day")
	require.Equal(t, 2, h.LatestTotal)
	require.Empty(t, VersionHistoryOf(sums[2:]).Areas, "one day is not a history")
	out := renderEN(t, FleetUptimeBody(FleetUptime{Rows: []FleetRow{{PK: "a"}}, Versions: h}))
	require.Contains(t, out, `style="background:#3399FF;"`)
}
