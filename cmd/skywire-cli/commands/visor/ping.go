// Package clivisor cmd/skywire-cli/commands/visor/ping.go
package clivisor

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor"
)

var tries int
var pcktSize int

func init() {
	RootCmd.AddCommand(pingCmd)
	pingCmd.Flags().IntVarP(&tries, "tries", "t", 1, "Number of pings")
	pingCmd.Flags().IntVarP(&pcktSize, "size", "s", 32, "Size of packet, in KB, default is 32KB")
}

var pingCmd = &cobra.Command{
	Use:   "ping <pk>",
	Short: "Ping the visor with given pk",
	Long:  "\n	Creates a route with the provided pk as a hop and returns latency on the conn",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pk := internal.ParsePK(cmd.Flags(), "pk", args[0])
		pingConfig := visor.PingConfig{PK: pk, Tries: tries, PcktSize: pcktSize}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		err = rpcClient.DialPing(pingConfig)
		internal.Catch(cmd.Flags(), err)

		latencies, err := rpcClient.Ping(pingConfig)
		internal.Catch(cmd.Flags(), err)

		for _, latency := range latencies {
			internal.PrintOutput(cmd.Flags(), latency, fmt.Sprintf(latency+"\n"))
		}
		err = rpcClient.StopPing(pk)
		internal.Catch(cmd.Flags(), err)
	},
}
