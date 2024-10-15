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

var (
	tries       int
	pcktSize    int
	pubVisCount int
)

func init() {
	RootCmd.AddCommand(pingCmd)
	pingCmd.Flags().IntVarP(&tries, "tries", "t", 1, "Number of tries")
	pingCmd.Flags().IntVarP(&pcktSize, "size", "s", 2, "Size of packet, in KB, default is 2KB")
	RootCmd.AddCommand(testCmd)
	testCmd.Flags().IntVarP(&tries, "tries", "t", 1, "Number of tries per public visors")
	testCmd.Flags().IntVarP(&pcktSize, "size", "s", 2, "Size of packet, in KB, default is 2KB")
	testCmd.Flags().IntVarP(&pubVisCount, "count", "c", 2, "Count of Public Visors for using in test.")
}

var pingCmd = &cobra.Command{
	Use:   "ping <pk>",
	Short: "Ping the visor with given pk",
	Long:  "\n  Creates a route with the provided pk as a hop and returns latency on the conn",
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
			internal.PrintOutput(cmd.Flags(), latency, fmt.Sprintf("Latency: %0.2f ms | Speed: %0.3f KB/s\n", 1000*latency.Seconds(), float64(pcktSize)/float64(latency.Seconds())))
		}
		err = rpcClient.StopPing(pk)
		internal.Catch(cmd.Flags(), err)
	},
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test the visor with public visors on network",
	Long:  "\n  Creates a route with public visors as a hop and returns latency on the conn",
	Run: func(cmd *cobra.Command, _ []string) {
		pingConfig := visor.PingConfig{Tries: tries, PcktSize: pcktSize, PubVisCount: pubVisCount}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		results, err := rpcClient.TestVisor(pingConfig)
		internal.Catch(cmd.Flags(), err)
		for i, result := range results {
			internal.PrintOutput(cmd.Flags(), result, fmt.Sprintf("Test No. %d\nPK: %s\nMax: %s\nMin: %s\nMean: %s\nStatus: %s\n\n", i+1, result.PK, result.Max, result.Min, result.Mean, result.Status))
		}
	},
}
