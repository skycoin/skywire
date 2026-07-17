// Package clidmsg cmd/skywire-cli/commands/dmsg/port_hits.go c4-vis-cli
package clidmsg

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	portHitsCmd.Flags().SortFlags = false
	portHitsCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
		"RPC server address (env: SKYWIRE_RPC)")
	portHitsCmd.Flags().Bool(internal.JSONString, false, "print output in json")
	RootCmd.AddCommand(portHitsCmd)
}

var portHitsCmd = &cobra.Command{
	Use:   "port-hits",
	Short: "Inbound dmsg requests to ports with no listener (would-be connectors)",
	Long: `List inbound dmsg stream requests that hit a local port this visor has
NO listener bound on (dmsg error 306). Each row is a distinct
(source PK, destination port) with a hit count and last-seen time.

Useful for spotting peers trying to reach a service you aren't
serving — e.g. managed visors hitting a disabled hypervisor port,
misconfigured peers pointed at the wrong port, or port scans —
even when nothing is listening to accept them.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		hits := rpcClient.DmsgPortHits()
		if isJSON, _ := cmd.Flags().GetBool(internal.JSONString); isJSON { //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), hits, "")
			return
		}
		if len(hits) == 0 {
			fmt.Println("No no-listener port hits recorded.")
			return
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "DST_PORT\tSRC_PK\tCOUNT\tFIRST_SEEN\tLAST_SEEN") //nolint:errcheck
		for _, h := range hits {
			fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\n", //nolint:errcheck
				h.DstPort, h.SrcPK, h.Count,
				h.FirstSeen.Format(time.RFC3339), h.LastSeen.Format(time.RFC3339))
		}
		tw.Flush() //nolint:errcheck,gosec
	},
}
