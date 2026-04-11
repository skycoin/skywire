// Package cliroute cmd/skywire-cli/commands/route/rsn_stats.go
package cliroute

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
)

var rsnStatsReset bool

func init() {
	rsnStatsCmd.Flags().SortFlags = false
	rsnStatsCmd.Flags().BoolVar(&rsnStatsReset, "reset", false, "reset all counters before reading (captures a fresh window)")
	routeCmd.AddCommand(rsnStatsCmd)
}

var rsnStatsCmd = &cobra.Command{
	Use:   "rsn-stats",
	Short: "Show embedded Route Setup Node request statistics",
	Long: `Query the visor's embedded Route Setup Node for per-request statistics.

Shows aggregate counters (total / successful / failed / concurrency drops),
a breakdown of failures by reason, latency percentiles for successful
setups, the distribution of route lengths, the most-requested and most-
failed destination PKs, and a ring buffer of the most recent failures
with error detail.

Requires the visor to have an embedded route setup-node configured
(route_setup_sk in the visor config).`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if rsnStatsReset {
			if err := rpcClient.ResetRouteSetupStats(); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("reset failed: %w", err))
			}
			internal.PrintOutput(cmd.Flags(), map[string]string{"status": "reset"}, "route-setup stats reset\n")
			return
		}

		snap, err := rpcClient.RouteSetupStats()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if isJSON {
			internal.PrintOutput(cmd.Flags(), snap, "")
			return
		}

		fmt.Print(formatRSNStats(snap))
	},
}

// formatRSNStats renders the snapshot as a human-readable multi-section
// text block. Kept as a pure string builder so it can be unit-tested
// without hitting the RPC layer.
func formatRSNStats(s *setupmetrics.StatsSnapshot) string {
	if s == nil {
		return "no stats returned\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Embedded Route Setup Node — request stats\n")
	fmt.Fprintf(&b, "=========================================\n\n")

	// ----- summary counters -----
	fmt.Fprintf(&b, "Started:          %s\n", s.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Uptime:           %s\n", time.Duration(s.UptimeSec)*time.Second)
	fmt.Fprintf(&b, "Total requests:   %d\n", s.TotalRequests)
	fmt.Fprintf(&b, "Successful:       %d\n", s.Successful)
	fmt.Fprintf(&b, "Failed:           %d\n", s.Failed)
	fmt.Fprintf(&b, "Concurrency drops:%d\n", s.ConcurrencyDrops)
	fmt.Fprintf(&b, "Active (in-flight):%d\n", s.ActiveRequests)
	fmt.Fprintf(&b, "Success rate:     %.1f%%\n", s.SuccessRatePct)
	if s.LastSuccessAt != nil {
		fmt.Fprintf(&b, "Last success:     %s\n", s.LastSuccessAt.Format(time.RFC3339))
	}
	if s.LastFailureAt != nil {
		fmt.Fprintf(&b, "Last failure:     %s\n", s.LastFailureAt.Format(time.RFC3339))
	}
	b.WriteString("\n")

	// ----- latency -----
	l := s.LatencyMs
	if l.Count > 0 {
		fmt.Fprintf(&b, "Latency (ms) over last %d successful setups:\n", l.Count)
		fmt.Fprintf(&b, "  min=%d  mean=%d  p50=%d  p95=%d  p99=%d  max=%d\n\n",
			l.Min, l.Mean, l.P50, l.P95, l.P99, l.Max)
	} else {
		fmt.Fprintf(&b, "Latency: no successful setups recorded yet\n\n")
	}

	// ----- failures by reason -----
	if s.Failed > 0 || len(s.FailuresByReason) > 0 {
		fmt.Fprintf(&b, "Failures by reason:\n")
		// Stable order by count desc, then name.
		type reasonKV struct {
			r setupmetrics.FailureReason
			n uint64
		}
		rs := make([]reasonKV, 0, len(s.FailuresByReason))
		for k, v := range s.FailuresByReason {
			rs = append(rs, reasonKV{k, v})
		}
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].n != rs[j].n {
				return rs[i].n > rs[j].n
			}
			return string(rs[i].r) < string(rs[j].r)
		})
		for _, kv := range rs {
			fmt.Fprintf(&b, "  %-22s %d\n", kv.r, kv.n)
		}
		b.WriteString("\n")
	}

	// ----- route length histogram -----
	if len(s.RouteLengthHist) > 0 {
		fmt.Fprintf(&b, "Successful route length distribution:\n")
		keys := make([]int, 0, len(s.RouteLengthHist))
		for k := range s.RouteLengthHist {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %d hops: %d\n", k, s.RouteLengthHist[k])
		}
		b.WriteString("\n")
	}

	// ----- top destinations -----
	if len(s.TopDestinations) > 0 {
		fmt.Fprintf(&b, "Top destinations (by total requests):\n")
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  pk\ttotal\tfailed") //nolint:errcheck,gosec
		for _, d := range s.TopDestinations {
			fmt.Fprintf(tw, "  %s\t%d\t%d\n", truncatePK(d.PK), d.Total, d.Failed) //nolint:errcheck,gosec
		}
		tw.Flush() //nolint:errcheck,gosec
		b.WriteString("\n")
	}
	if len(s.TopFailedDestinations) > 0 {
		fmt.Fprintf(&b, "Top failed destinations:\n")
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  pk\tfailed\ttotal") //nolint:errcheck,gosec
		for _, d := range s.TopFailedDestinations {
			fmt.Fprintf(tw, "  %s\t%d\t%d\n", truncatePK(d.PK), d.Failed, d.Total) //nolint:errcheck,gosec
		}
		tw.Flush() //nolint:errcheck,gosec
		b.WriteString("\n")
	}

	// ----- recent failures (newest first) -----
	if len(s.RecentFailures) > 0 {
		fmt.Fprintf(&b, "Recent failures (newest first, up to %d):\n", len(s.RecentFailures))
		for i, f := range s.RecentFailures {
			fmt.Fprintf(&b, "  [%d] %s  reason=%s  duration=%dms\n",
				i+1, f.Timestamp.Format("15:04:05"), f.Reason, f.DurationMs)
			if f.SrcPK != "" || f.DstPK != "" {
				fmt.Fprintf(&b, "      src=%s  dst=%s  hops=%d\n",
					truncatePK(f.SrcPK), truncatePK(f.DstPK), f.HopCount)
			}
			fmt.Fprintf(&b, "      error: %s\n", f.Error)
		}
	}

	return b.String()
}

// truncatePK shortens a public key for table display without hiding
// the identifying prefix.
func truncatePK(pk string) string {
	if len(pk) <= 16 {
		return pk
	}
	return pk[:16] + "…"
}
