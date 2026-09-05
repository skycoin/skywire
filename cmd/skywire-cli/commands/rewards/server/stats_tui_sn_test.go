// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_sn_test.go c5-reward-server
package clirewardsserver

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
)

const (
	snPKa = "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
	snPKb = "024fbd3997d4260f731b01abcfce60b8967a6d4c6a11d1008812810ea1437ce438"
)

// snSample mirrors the shape a live setup node returned on 2026-09-05.
func snSample() snStats {
	return snStats{Nodes: []snNode{{
		PK: snPKa,
		Snap: setupmetrics.StatsSnapshot{
			UptimeSec: 7320, TotalRequests: 63, Successful: 4, Failed: 59,
			ConcurrencyDrops: 0, ActiveRequests: 0, SuccessRatePct: 6.349206349206349,
			FailuresByReason: map[setupmetrics.FailureReason]uint64{
				setupmetrics.ReasonCircuitOpen:             46,
				setupmetrics.ReasonDestinationRules:        10,
				setupmetrics.ReasonIntermediateUnreachable: 3,
			},
			LatencyMs: setupmetrics.LatencyStats{
				Count: 4, Min: 1170, Max: 5110, Mean: 2821, P50: 1910, P95: 3097, P99: 3097,
			},
			RouteLengthHist: map[int]uint64{1: 4},
			// PKs arrive blank: the setup node strips them before marshaling.
			TopDestinations: []setupmetrics.DestStat{
				{Total: 44, Failed: 40, Circuit: "closed"},
				{Total: 18, Failed: 16, Circuit: "closed"},
				{Total: 0, Failed: 0, Circuit: "open"},
			},
		},
		DestsTracked: 3, DestsBreakerOpen: 1,
	}, {
		PK:  snPKb,
		Err: "request failed: context deadline exceeded",
	}}}
}

// An unreachable setup node must be an explicit row. Dropping it makes a dead
// node indistinguishable from a deployment that has one fewer.
func TestSetupNodePanelDrawsUnreachableNodesExplicitly(t *testing.T) {
	out := renderSetupNodePanelANSI(snSample())
	if !strings.Contains(out, snPKb) {
		t.Fatal("the unreachable node was omitted from the panel")
	}
	if !strings.Contains(out, "unreachable") {
		t.Error("the unreachable node was not labeled")
	}
	if !strings.Contains(out, "context deadline exceeded") {
		t.Error("the reason the node did not answer was not surfaced")
	}
	if !strings.Contains(out, "1 of 2 configured route setup nodes answered") {
		t.Error("the reachable-node count is wrong or missing")
	}
}

// Public keys are never truncated: an operator matching a node against their
// own config needs the whole key.
func TestSetupNodePanelPrintsFullPublicKeys(t *testing.T) {
	out := renderSetupNodePanelANSI(snSample())
	for _, pk := range []string{snPKa, snPKb} {
		if !strings.Contains(out, pk) {
			t.Errorf("public key %s is not present in full", pk)
		}
	}
}

// The four required readings: volume and rate, percentiles, failure reasons,
// route length.
func TestSetupNodePanelRendersEveryRequiredReading(t *testing.T) {
	out := renderSetupNodePanelANSI(snSample())
	for _, want := range []string{
		"ROUTE SETUP NODES", "ROUTE SETUP LATENCY", "ROUTE SETUP FAILURES",
		"ROUTE SETUP ROUTE LENGTH",
		"63", "6.3%", // volume and success rate
		"1910", "3097", "5110", // p50, p95/p99, max
		"circuit_open", "destination_rules", "intermediate_unreachable",
		"1 hop",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("panel is missing %q", want)
		}
	}
}

// The setup node blanks destination PKs on purpose. The panel must say so
// rather than printing an unexplained table of nameless destinations.
func TestSetupNodePanelSaysDestinationKeysAreStripped(t *testing.T) {
	out := renderSetupNodePanelANSI(snSample())
	if !strings.Contains(out, "3 destinations tracked") {
		t.Error("the tracked-destination count is missing")
	}
	if !strings.Contains(out, "1 with the circuit breaker not closed") {
		t.Error("the open-breaker count is missing")
	}
	if !strings.Contains(out, "blanked") {
		t.Error("the panel does not explain that destination keys are unavailable")
	}
}

// An idle node has a 0% success rate over zero attempts. That is not a failing
// node, and a full-width red bar reporting it as one is the misleading-empty-
// chart failure this panel is supposed to avoid.
func TestSetupNodePanelDoesNotDrawARateForZeroRequests(t *testing.T) {
	out := renderSetupNodePanelANSI(snStats{Nodes: []snNode{{PK: snPKa}}})
	if !strings.Contains(out, "no route-setup requests recorded") {
		t.Error("an idle node did not say it had recorded no requests")
	}
	if strings.Contains(out, "0.0%") {
		t.Error("a percentage was drawn over zero attempts")
	}
	if !strings.Contains(out, "no successful setups recorded — no latency samples") {
		t.Error("an empty latency ring was not named")
	}
	if !strings.Contains(out, "no route lengths sampled") {
		t.Error("an empty route-length histogram was not named")
	}
	if !strings.Contains(out, "nothing to classify") {
		t.Error("an empty failure breakdown was not named")
	}
}

// Below ten samples p95 and p99 land on the same ring element, so the
// percentile plot draws a step that is an artifact of the ring size. The
// ladder still renders; only the curve is withheld.
func TestSetupNodePanelWithholdsThePercentileCurveOnATinySample(t *testing.T) {
	out := renderSetupNodePanelANSI(snSample()) // Count == 4
	if strings.Contains(out, "percentile curve") {
		t.Error("a percentile curve was plotted from four samples")
	}
	if !strings.Contains(out, "4 samples") {
		t.Error("the sample count is not stated")
	}

	s := snSample()
	s.Nodes[0].Snap.LatencyMs.Count = 240
	out = renderSetupNodePanelANSI(s)
	if !strings.Contains(out, "percentile curve") {
		t.Error("the curve was withheld from a sample large enough to draw one")
	}
}

// No node at all is a configuration statement, not an empty panel.
func TestSetupNodePanelNeverSilentlyEmpty(t *testing.T) {
	out := renderSetupNodePanelANSI(snStats{})
	if !strings.Contains(out, "ROUTE SETUP NODES") || !strings.Contains(out, "unavailable") {
		t.Error("an empty panel disappeared instead of reporting itself absent")
	}
}

// Every node dead must still name the sections rather than dropping three of
// the four panels.
func TestSetupNodePanelNamesSectionsWhenEveryNodeIsDead(t *testing.T) {
	out := renderSetupNodePanelANSI(snStats{Nodes: []snNode{{PK: snPKa, Err: "status 502"}}})
	for _, want := range []string{
		"ROUTE SETUP LATENCY", "ROUTE SETUP FAILURES", "ROUTE SETUP ROUTE LENGTH",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("section %q vanished when every node was unreachable", want)
		}
	}
	if strings.Count(out, "no route setup node answered") != 3 {
		t.Error("the three data sections did not each say why they are empty")
	}
}

// The panel is 78 columns and its explanatory lines are sentences. An
// unwrapped one runs off the rule in the 80-column target.
func TestSetupNodePanelStaysWithinTheRule(t *testing.T) {
	s := snSample()
	s.Nodes[0].Snap.LatencyMs.Count = 240
	s.Nodes[0].Snap.FailuresByReason[setupmetrics.ReasonIntermediaryRules] = 999999
	s.Nodes[0].Snap.RouteLengthHist[12] = 4000000
	// A real dmsg fetch error embeds the full 66-character key in the URL it
	// failed on, so the reason alone is wider than the panel.
	s.Nodes[1].Err = `Get "dmsg://` + snPKb + `:80/stats": EOF`
	for _, line := range strings.Split(renderSetupNodePanelANSI(s), "\n") {
		// Columns, not bytes: the rules are box-drawing runes.
		plain := []rune(stripANSI(line))
		if len(plain) > tuiWidth+2 {
			t.Errorf("line runs to %d columns: %q", len(plain), string(plain))
		}
	}
}

// The node list comes from route_setup_nodes, NOT transport_setup — a
// different set of keys for a different job. Confusing the two would render
// the transport setup nodes under a route-setup heading.
func TestGatherSetupNodeStatsEnumeratesTheConfiguredRouteSetupNodes(t *testing.T) {
	// No dmsg client in the test binary, so every fetch fails; what is under
	// test is the enumeration and that failures become rows.
	got := gatherSetupNodeStats()
	if len(got.Nodes) == 0 {
		t.Fatal("no route setup nodes enumerated from the deployment config")
	}
	for _, n := range got.Nodes {
		if len(n.PK) != 66 {
			t.Errorf("node key %q is not a full public key", n.PK)
		}
	}
	if len(got.Nodes) != len(deployment.Prod.RouteSetupNodes) {
		t.Error("the enumerated node count does not match route_setup_nodes")
	}
	// transport_setup is a different list for a different job; the panel must
	// not be enumerating it.
	for _, n := range got.Nodes {
		for _, tsn := range deployment.Prod.TransportSetupPKs {
			if n.PK == tsn.Hex() {
				t.Errorf("transport setup node %s was enumerated as a route setup node", n.PK)
			}
		}
	}
}

func TestTuiDurationPicksTheTopTwoUnits(t *testing.T) {
	for _, c := range []struct {
		sec  int64
		want string
	}{
		{0, "0s"}, {45, "45s"}, {125, "2m 5s"}, {7320, "2h 2m"}, {200000, "2d 7h"}, {-5, "0s"},
	} {
		if got := tuiDuration(c.sec); got != c.want {
			t.Errorf("tuiDuration(%d) = %q, want %q", c.sec, got, c.want)
		}
	}
}
