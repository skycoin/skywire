// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/statscxo_test.go c5-reward-server
//
// Tests for the two arithmetic claims the CXO move rests on:
//
//   - per-visor bandwidth summed by edge public key is what
//     GET /metric/visor/{pks} returns, so deleting that request loses
//     nothing; and
//   - the min()-verified network total keeps its three-branch trust
//     model when the records arrive over CXO instead of HTTP.
//
// Both are pinned here because the constraint on this change was that
// the SOURCE moves and the NUMBER does not.
package clirewardsserver

import (
	"strings"
	"testing"
	"time"

	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

const (
	pkA = "020000000000000000000000000000000000000000000000000000000000000001"
	pkB = "020000000000000000000000000000000000000000000000000000000000000002"
	pkC = "020000000000000000000000000000000000000000000000000000000000000003"
)

// TestFillVisorBandwidthFromMetricsAttributesEdgesByPosition pins the
// mapping the derivation depends on: edge A is Edges[0] and edge B is
// Edges[1]. The store keys the per-day hash by reporter public key and
// reads it back in that order, and each reporter's deltas go into the
// per-transport hash and its own per-visor rollup in one pipeline — so
// getting this backwards would silently swap two visors' bandwidth
// while still producing a plausible-looking chart.
func TestFillVisorBandwidthFromMetricsAttributesEdgesByPosition(t *testing.T) {
	metrics := []tpdstore.TransportMetric{
		{
			ID:    "tp-1",
			Type:  "stcpr",
			Edges: []string{pkA, pkB},
			Daily: []tpdstore.DailyEdgeBandwidth{
				{
					Date: "2026-09-04",
					A:    &tpdstore.EdgeBandwidth{Sent: 100, Recv: 20},
					B:    &tpdstore.EdgeBandwidth{Sent: 7, Recv: 3},
				},
			},
		},
		{
			ID:    "tp-2",
			Type:  "dmsg",
			Edges: []string{pkB, pkA},
			Daily: []tpdstore.DailyEdgeBandwidth{
				{
					Date: "2026-09-04",
					A:    &tpdstore.EdgeBandwidth{Sent: 1000, Recv: 0},
					B:    &tpdstore.EdgeBandwidth{Sent: 0, Recv: 5},
				},
			},
		},
	}

	out := &visorBandwidthData{Visors: []visorBWEntry{{PK: pkA}, {PK: pkB}}}
	if err := fillVisorBandwidthFromMetrics(out, metrics); err != nil {
		t.Fatalf("fill: %v", err)
	}

	// pkA: edge A of tp-1 (120) + edge B of tp-2 (5) = 125.
	// pkB: edge B of tp-1 (10)  + edge A of tp-2 (1000) = 1010.
	want := map[string]uint64{pkA: 125, pkB: 1010}
	for _, v := range out.Visors {
		if v.Total != want[v.PK] {
			t.Fatalf("visor %s total = %d, want %d", v.PK, v.Total, want[v.PK])
		}
		if !v.Reported {
			t.Fatalf("visor %s should be marked reported", v.PK)
		}
	}
	if !out.BandwidthOK {
		t.Fatal("BandwidthOK not set on a successful derivation")
	}
}

// TestFillVisorBandwidthFromMetricsReportsOnlyMeasuredDays checks the
// window is trimmed to the days that carry a measurement. Padding it out
// with zeroes would assert the network moved nothing on days TPD simply
// has no record for — the same rule the HTTP path applies, and the
// reason the entry carries Reported at all.
func TestFillVisorBandwidthFromMetricsReportsOnlyMeasuredDays(t *testing.T) {
	metrics := []tpdstore.TransportMetric{
		{
			ID:    "tp-1",
			Edges: []string{pkA, pkB},
			Daily: []tpdstore.DailyEdgeBandwidth{
				{Date: "2026-09-02", A: &tpdstore.EdgeBandwidth{Sent: 50}},
				{Date: "2026-09-04", A: &tpdstore.EdgeBandwidth{Recv: 70}},
			},
		},
	}

	// pkC is selected but appears on no transport: listed, not charted.
	out := &visorBandwidthData{Visors: []visorBWEntry{{PK: pkA}, {PK: pkC}}}
	if err := fillVisorBandwidthFromMetrics(out, metrics); err != nil {
		t.Fatalf("fill: %v", err)
	}

	if got := strings.Join(out.Dates, ","); got != "2026-09-02,2026-09-04" {
		t.Fatalf("dates = %q, want only the two days that reported, oldest first", got)
	}
	for _, v := range out.Visors {
		switch v.PK {
		case pkA:
			if !v.Reported || v.Total != 120 {
				t.Fatalf("pkA = (reported=%v, total=%d), want (true, 120)", v.Reported, v.Total)
			}
			if len(v.Daily) != len(out.Dates) {
				t.Fatalf("pkA has %d points for %d dates", len(v.Daily), len(out.Dates))
			}
		case pkC:
			if v.Reported || v.Total != 0 {
				t.Fatalf("pkC = (reported=%v, total=%d), want an unreported visor", v.Reported, v.Total)
			}
		}
	}
}

// TestFillVisorBandwidthFromMetricsRefusesAnEmptyWindow checks a window
// with nothing in it is an error the caller can fall back on, never a
// page of zeroes.
func TestFillVisorBandwidthFromMetricsRefusesAnEmptyWindow(t *testing.T) {
	out := &visorBandwidthData{Visors: []visorBWEntry{{PK: pkA}}}
	err := fillVisorBandwidthFromMetrics(out, []tpdstore.TransportMetric{
		{ID: "tp-1", Edges: []string{pkB, pkC}, Daily: []tpdstore.DailyEdgeBandwidth{
			{Date: "2026-09-04", A: &tpdstore.EdgeBandwidth{Sent: 9}},
		}},
	})
	if err == nil {
		t.Fatal("expected an error when no selected visor reported")
	}
	if out.BandwidthOK {
		t.Fatal("BandwidthOK must stay false so the caller falls back")
	}
}

// TestAccumulateVerifiedBandwidthThreeBranchModel pins the trust model
// the reward calculation mirrors. The branch that matters is the third:
// an edge record present but {0,0} means "has not reported", NOT
// "verified zero" — keying on record presence instead would take the
// min() branch and zero the counterparty's real bandwidth.
func TestAccumulateVerifiedBandwidthThreeBranchModel(t *testing.T) {
	cases := []struct {
		name string
		day  tpdstore.DailyEdgeBandwidth
		want uint64
	}{
		{
			name: "both edges report: min per direction",
			day: tpdstore.DailyEdgeBandwidth{
				A: &tpdstore.EdgeBandwidth{Sent: 100, Recv: 40},
				B: &tpdstore.EdgeBandwidth{Sent: 30, Recv: 90},
			},
			// aToB = min(100, 90) = 90; bToA = min(40, 30) = 30.
			want: 120,
		},
		{
			name: "only A reports: A is taken whole",
			day: tpdstore.DailyEdgeBandwidth{
				A: &tpdstore.EdgeBandwidth{Sent: 100, Recv: 40},
			},
			want: 140,
		},
		{
			name: "B present but zero: still only A, never min(0, …)",
			day: tpdstore.DailyEdgeBandwidth{
				A: &tpdstore.EdgeBandwidth{Sent: 100, Recv: 40},
				B: &tpdstore.EdgeBandwidth{},
			},
			want: 140,
		},
		{
			name: "only B reports",
			day: tpdstore.DailyEdgeBandwidth{
				A: &tpdstore.EdgeBandwidth{},
				B: &tpdstore.EdgeBandwidth{Sent: 11, Recv: 22},
			},
			want: 33,
		},
		{
			name: "neither reports",
			day:  tpdstore.DailyEdgeBandwidth{},
			want: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			summary := &tpdNetworkSummary{ByType: map[string]int{}}
			accumulateVerifiedBandwidth([]tpdstore.TransportMetric{
				{ID: "tp-1", Daily: []tpdstore.DailyEdgeBandwidth{c.day}},
			}, summary)
			if summary.TotalBandwidth != c.want {
				t.Fatalf("total = %d, want %d", summary.TotalBandwidth, c.want)
			}
		})
	}
}

// TestStatsSourceNamesTheTransport checks the provenance line actually
// says which of the two answered. A page that printed the same string
// for a stale snapshot and a live fetch would be the failure this label
// exists to prevent.
func TestStatsSourceNamesTheTransport(t *testing.T) {
	cxo := statsSource{Via: "CXO", Path: "stats/network", At: time.Now().Add(-30 * time.Second)}
	line := cxo.String()
	if !strings.Contains(line, "CXO") || !strings.Contains(line, "stats/network") || !strings.Contains(line, "30s old") {
		t.Fatalf("CXO source line = %q", line)
	}

	httpLine := httpStatsSource("/all-transports/stats", "feed not synced").String()
	if !strings.Contains(httpLine, "HTTP over dmsg") || !strings.Contains(httpLine, "feed not synced") {
		t.Fatalf("HTTP source line = %q", httpLine)
	}
	if strings.Contains(httpLine, "snapshot") {
		t.Fatalf("HTTP source line claims a snapshot age: %q", httpLine)
	}

	if (statsSource{}).String() != "" {
		t.Fatal("an unset source must render as nothing, not as a bare label")
	}
}

// TestCompletenessNoteSurfacesAPartialSample checks an incomplete feed
// body is shown WITH its caveat rather than suppressed. A feed that
// froze on real news would be worse than one that publishes it marked.
func TestCompletenessNoteSurfacesAPartialSample(t *testing.T) {
	if note := completenessNote(true, "settled"); note != "" {
		t.Fatalf("a complete sample should carry no caveat, got %q", note)
	}
	note := completenessNote(false, "refilling")
	if !strings.Contains(note, "INCOMPLETE") || !strings.Contains(note, "refilling") {
		t.Fatalf("incomplete note = %q", note)
	}
}
