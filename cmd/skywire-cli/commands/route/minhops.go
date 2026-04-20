// Package cliroute cmd/skywire-cli/commands/route/minhops.go
package cliroute

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	RootCmd.AddCommand(minHopsCmd)
}

var minHopsCmd = &cobra.Command{
	Use:   "minhops <n>",
	Short: "Set minimum hops for route calculation at runtime",
	Long: `Set the routing.min_hops value on the running visor.

This affects all subsequent route calculations without restarting.
The change is not persisted to the config file — use SKYENV or
config update to make it permanent.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		n, err := strconv.ParseUint(args[0], 10, 16)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid hops value: %s", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.SetMinHops(uint16(n)); err != nil { //nolint:gosec
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Minimum hops set to %d\n", n)
	},
}
