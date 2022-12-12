// Package clivisor cmd/skywire-cli/commands/visor/ping.go
package clivisor

import (
	"fmt"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var no int

func init() {
	RootCmd.AddCommand(pingCmd)
	pingCmd.Flags().IntVarP(&no, "no", "n", 1, "Number of pings")
}

var pingCmd = &cobra.Command{
	Use:   "ping <pk>",
	Short: "Ping the visor with given pk",
	Long:  "\n	Creates a route with the provided pk as a hop and returns latency on the conn",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pk := internal.ParsePK(cmd.Flags(), "pk", args[0])

		err := clirpc.Client(cmd.Flags()).DialPing(pk)
		internal.Catch(cmd.Flags(), err)
		for i := 1; i <= no; i++ {
			latency, err := clirpc.Client(cmd.Flags()).Ping(pk)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), latency, fmt.Sprintf(latency+"\n"))
		}
		err = clirpc.Client(cmd.Flags()).StopPing(pk)
		internal.Catch(cmd.Flags(), err)

	},
}
