// Package cliroute cmd/skywire-cli/commands/route/table_stats.go c4-vis-cli
package cliroute

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	routeCmd.AddCommand(tableStatsCmd)
}

var tableStatsCmd = &cobra.Command{
	Use:   "table-stats",
	Short: "Show routing-table observability counters (rule count, route-ID high-water, per-type)",
	Long: `Report this visor's routing-table counters:

  count    — live routing rules currently installed
  next_id  — the monotonic route-ID high-water mark. RouteIDs are never
             reused, so this only ever grows; watching it on a RELAY across a
             churn soak surfaces reserved-ID leaks (it should climb only in
             proportion to genuine new routes, not rotate-churn).
  by_type  — rule count broken down by type (forward / consume / intermediary).

On an intermediate/relay visor this is the metric the route-setup soak harness
samples to prove that rotating-policy churn reclaims rules/IDs rather than
leaking them.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		stats, err := rpcClient.RoutingStats()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if isJSON, _ := cmd.Flags().GetBool(internal.JSONString); isJSON { //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), stats, "")
			return
		}
		fmt.Printf("rules:    %d\n", stats.Count)
		fmt.Printf("next_id:  %d  (route-ID high-water; never reused)\n", stats.NextID)
		if len(stats.ByType) > 0 {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "by type\tcount") //nolint:errcheck,gosec
			types := make([]string, 0, len(stats.ByType))
			for t := range stats.ByType {
				types = append(types, t)
			}
			sort.Strings(types)
			for _, t := range types {
				fmt.Fprintf(tw, "  %s\t%d\n", t, stats.ByType[t]) //nolint:errcheck,gosec
			}
			tw.Flush() //nolint:errcheck,gosec
		}
	},
}
