// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_sn.go c5-reward-server
//
// The route setup nodes, in the terminal panel.
//
// Every route in the network is negotiated by a route setup node, and until now
// nothing on the reward site said anything about them. The transport and uptime
// views describe what the network is made of; the setup nodes describe whether a
// route across it can actually be established. A setup node refusing nearly
// every request — circuit breakers open, destinations unreachable — leaves no
// mark on any existing chart: the transports are still registered, the visors
// are still online, and routing is simply not working.
//
// The nodes are enumerated from the deployment's route_setup_nodes list rather
// than hardcoded (that list is NOT transport_setup, which is a different set of
// keys for a different job), and a node that does not answer is drawn as an
// explicit unreachable row. Silently omitting it would make a dead setup node
// look like a deployment that has one fewer.
package clirewardsserver

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/guptarohit/asciigraph"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
)

// snNode is one route setup node's /stats snapshot, or the reason it has none.
type snNode struct {
	PK string
	// Err is set when the node did not answer. The row is still drawn.
	Err  string
	Snap setupmetrics.StatsSnapshot
	// DestsTracked and DestsBreakerOpen summarize the per-destination table.
	// The destinations themselves cannot be named: the setup node's /stats
	// handler deliberately blanks every PK in top_destinations,
	// top_failed_destinations and recent_failures so the endpoint does not
	// leak network topology. The counts are still a measurement; the names
	// are simply not on the wire.
	DestsTracked     int
	DestsBreakerOpen int
}

type snStats struct {
	Nodes []snNode
}

// gatherSetupNodeStats reads /stats from every configured route setup node.
// Concurrently: the dmsg HTTP client's timeout is 45s and an unreachable node
// spends all of it, which would otherwise be paid once per node in series.
// As with every other source here, a failure is recorded, never returned.
func gatherSetupNodeStats() snStats {
	pks := deployment.Prod.RouteSetupNodes
	if len(pks) == 0 {
		return snStats{}
	}
	nodes := make([]snNode, len(pks))
	var wg sync.WaitGroup
	for i, pk := range pks {
		nodes[i].PK = pk.Hex()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			url := fmt.Sprintf("dmsg://%s:%d/stats", nodes[i].PK, dmsg.DefaultDmsgHTTPPort)
			if err := statsGetJSON(url, &nodes[i].Snap); err != nil {
				nodes[i].Err = err.Error()
				return
			}
			seen := make(map[string]bool)
			for _, d := range nodes[i].Snap.TopDestinations {
				nodes[i].DestsTracked++
				if d.Circuit != "" && d.Circuit != string(setupmetrics.CircuitClosed) {
					nodes[i].DestsBreakerOpen++
				}
				seen[d.PK] = true
			}
			// top_failed_destinations is a re-ranking of the same table, so it
			// adds nothing to the count — except that both are PK-blanked, so
			// they cannot be de-duplicated by key and only the larger of the
			// two is a defensible count.
			if n := len(nodes[i].Snap.TopFailedDestinations); n > nodes[i].DestsTracked {
				nodes[i].DestsTracked = n
			}
		}(i)
	}
	wg.Wait()
	return snStats{Nodes: nodes}
}

// tuiDuration renders an uptime in the largest two units that are non-zero.
func tuiDuration(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	d, h, m := sec/86400, (sec%86400)/3600, (sec%3600)/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, sec%60)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

// renderSetupNodePanelANSI draws the route-setup control plane: volume and
// outcome, latency percentiles, failures by reason, and route length.
func renderSetupNodePanelANSI(s snStats) string {
	width := tuiWidth
	if len(s.Nodes) == 0 {
		return tuiMissing("ROUTE SETUP NODES", "no route setup nodes configured") + "\n"
	}
	var b strings.Builder
	b.WriteString(renderSNOutcomeANSI(s, width))
	b.WriteString(renderSNLatencyANSI(s, width))
	b.WriteString(renderSNFailuresANSI(s, width))
	b.WriteString(renderSNRouteLenANSI(s, width))
	return b.String()
}

// renderSNOutcomeANSI draws request volume and the success rate per node.
func renderSNOutcomeANSI(s snStats, width int) string {
	var b strings.Builder
	b.WriteString(tuiRule("ROUTE SETUP NODES — requests & outcome", width))
	reachable, stripped := 0, false
	for _, n := range s.Nodes {
		b.WriteString(fmt.Sprintf("  %s%s%s\n", aDim, n.PK, aReset))
		if n.Err != "" {
			// The reason is wrapped: a dmsg fetch error embeds the whole
			// 66-character key in the URL it failed on, which alone is wider
			// than the panel.
			b.WriteString(fmt.Sprintf("    %sunreachable%s\n", aRed, aReset))
			b.WriteString(tuiWrap("      " + n.Err))
			continue
		}
		reachable++
		snap := n.Snap
		b.WriteString(fmt.Sprintf("    %sup%s %s  %s%d%s req  %s%d%s ok  %s%d%s fail  %s%d%s dropped  %s%d%s active\n",
			aDim, aReset, tuiDuration(snap.UptimeSec),
			aBold, snap.TotalRequests, aReset,
			aGreen, snap.Successful, aReset,
			aRed, snap.Failed, aReset,
			aYellow, snap.ConcurrencyDrops, aReset,
			aCyan, snap.ActiveRequests, aReset))
		// A zero-request node draws no rate bar. A 0% success rate over zero
		// attempts is not a failing node, it is an idle one, and a red empty
		// bar would report the first while measuring the second.
		if snap.TotalRequests == 0 {
			b.WriteString(fmt.Sprintf("    %sno route-setup requests recorded in this uptime window%s\n", aDim, aReset))
		} else {
			frac := snap.SuccessRatePct / 100
			col := aRed
			switch {
			case snap.SuccessRatePct >= 90:
				col = aGreen
			case snap.SuccessRatePct >= 50:
				col = aYellow
			}
			b.WriteString(fmt.Sprintf("    %ssuccess%s %s %s%5.1f%%%s\n",
				aDim, aReset, tuiBar(frac, 24, col), col, snap.SuccessRatePct, aReset))
		}
		if n.DestsTracked > 0 {
			stripped = true
			col := aDim
			if n.DestsBreakerOpen > 0 {
				col = aRed
			}
			b.WriteString(fmt.Sprintf("    %s%d destinations tracked · %s%d with the circuit breaker not closed%s\n",
				aDim, n.DestsTracked, col, n.DestsBreakerOpen, aReset))
		}
	}
	b.WriteString(tuiWrap(fmt.Sprintf("  %d of %d configured route setup nodes answered.",
		reachable, len(s.Nodes))))
	if stripped {
		b.WriteString(tuiWrap("  Destination public keys are blanked by the setup node's own /stats " +
			"handler so the endpoint does not leak network topology, so hot and failing " +
			"destinations are counted here but cannot be named."))
	}
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	return b.String()
}

// renderSNLatencyANSI draws the successful-setup latency percentiles. Scaled to
// the node's own max, so the ladder reads as a shape rather than five numbers.
func renderSNLatencyANSI(s snStats, width int) string {
	var b strings.Builder
	b.WriteString(tuiRule("ROUTE SETUP LATENCY — successful setups", width))
	var curves []string
	drew := false
	for _, n := range s.Nodes {
		if n.Err != "" {
			continue
		}
		drew = true
		l := n.Snap.LatencyMs
		b.WriteString(fmt.Sprintf("  %s%s%s\n", aDim, n.PK, aReset))
		if l.Count == 0 {
			// No successful setup means no sample. A ladder of zeroes here
			// would read as "every setup completed instantly".
			b.WriteString(fmt.Sprintf("    %sno successful setups recorded — no latency samples%s\n", aDim, aReset))
			continue
		}
		maxMs := l.Max
		if maxMs <= 0 {
			maxMs = 1
		}
		for _, row := range []struct {
			name string
			v    int64
		}{{"min", l.Min}, {"p50", l.P50}, {"p95", l.P95}, {"p99", l.P99}, {"max", l.Max}} {
			frac := float64(row.v) / float64(maxMs)
			col := aGreen
			switch {
			case row.v >= 5000:
				col = aRed
			case row.v >= 1000:
				col = aYellow
			}
			b.WriteString(fmt.Sprintf("    %s%-3s%s %s %s%6d ms%s\n",
				aDim, row.name, aReset, tuiBar(frac, 24, col), aBold, row.v, aReset))
		}
		b.WriteString(fmt.Sprintf("    %s%d samples · mean %d ms%s\n", aDim, l.Count, l.Mean, aReset))
		// The percentile curve is only a curve once the percentiles are drawn
		// from distinct samples. Below ten, p95 and p99 land on the same
		// element and the plot draws a step that is an artifact of the ring
		// size, not of the network.
		if l.Count >= 10 {
			curves = append(curves, tuiPlot("ROUTE SETUP LATENCY — percentile curve (ms)",
				[]float64{float64(l.Min), float64(l.P50), float64(l.P95), float64(l.P99), float64(l.Max)},
				[]string{"min", "p50", "p95", "p99", "max"}, 7, 0, asciigraph.Yellow, width,
				fmt.Sprintf("%s · %d samples", n.PK, l.Count)))
		}
	}
	if !drew {
		return tuiMissing("ROUTE SETUP LATENCY", "no route setup node answered") + "\n"
	}
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	for _, c := range curves {
		b.WriteString(c)
	}
	return b.String()
}

// renderSNFailuresANSI breaks the failures down by reason. This is the panel
// that distinguishes "the destination is down" from "this setup node is the
// problem": source_unreachable and concurrency_limit are the node's own,
// destination_rules and circuit_open are the network's.
func renderSNFailuresANSI(s snStats, width int) string {
	var b strings.Builder
	b.WriteString(tuiRule("ROUTE SETUP FAILURES — by reason", width))
	drew := false
	for _, n := range s.Nodes {
		if n.Err != "" {
			continue
		}
		drew = true
		b.WriteString(fmt.Sprintf("  %s%s%s\n", aDim, n.PK, aReset))
		total := uint64(0)
		for _, v := range n.Snap.FailuresByReason {
			total += v
		}
		if total == 0 {
			if n.Snap.TotalRequests == 0 {
				b.WriteString(fmt.Sprintf("    %sno requests recorded — nothing to classify%s\n", aDim, aReset))
			} else {
				b.WriteString(fmt.Sprintf("    %sno failures recorded across %d requests%s\n",
					aGreen, n.Snap.TotalRequests, aReset))
			}
			continue
		}
		type reasonRow struct {
			name string
			n    uint64
		}
		rows := make([]reasonRow, 0, len(n.Snap.FailuresByReason))
		for r, v := range n.Snap.FailuresByReason {
			rows = append(rows, reasonRow{string(r), v})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].n != rows[j].n {
				return rows[i].n > rows[j].n
			}
			return rows[i].name < rows[j].name
		})
		for _, r := range rows {
			frac := float64(r.n) / float64(total)
			b.WriteString(fmt.Sprintf("    %s%-24s%s %s%5d%s %s %s%5.1f%%%s\n",
				aRed, r.name, aReset, aBold, r.n, aReset,
				tuiBar(frac, 20, aRed), aDim, 100*frac, aReset))
		}
	}
	if !drew {
		return tuiMissing("ROUTE SETUP FAILURES", "no route setup node answered") + "\n"
	}
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	return b.String()
}

// renderSNRouteLenANSI draws the hop-count histogram of successful setups. It
// counts successes only, so a node failing everything has an empty histogram
// and says so rather than drawing a bar chart of nothing.
func renderSNRouteLenANSI(s snStats, width int) string {
	var b strings.Builder
	b.WriteString(tuiRule("ROUTE SETUP ROUTE LENGTH — hops per successful setup", width))
	drew := false
	for _, n := range s.Nodes {
		if n.Err != "" {
			continue
		}
		drew = true
		b.WriteString(fmt.Sprintf("  %s%s%s\n", aDim, n.PK, aReset))
		hist := n.Snap.RouteLengthHist
		if len(hist) == 0 {
			b.WriteString(fmt.Sprintf("    %sno successful setups recorded — no route lengths sampled%s\n",
				aDim, aReset))
			continue
		}
		hops := make([]int, 0, len(hist))
		total, maxN := uint64(0), uint64(0)
		for h, v := range hist {
			hops = append(hops, h)
			total += v
			if v > maxN {
				maxN = v
			}
		}
		sort.Ints(hops)
		if maxN == 0 {
			maxN = 1
		}
		for _, h := range hops {
			v := hist[h]
			pct := 0.0
			if total > 0 {
				pct = 100 * float64(v) / float64(total)
			}
			b.WriteString(fmt.Sprintf("    %s%2d hop%s %s%5d%s %s %s%5.1f%%%s\n",
				aCyan, h, aReset, aBold, v, aReset,
				tuiBar(float64(v)/float64(maxN), 24, aCyan), aDim, pct, aReset))
		}
	}
	if !drew {
		return tuiMissing("ROUTE SETUP ROUTE LENGTH", "no route setup node answered") + "\n"
	}
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	return b.String()
}
