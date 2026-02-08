// Package clivisor cmd/skywire-cli/commands/visor/ping.go
package clivisor

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	tries      int
	pcktSize   int
	pubVisCount int
	localRoute bool
	createTp   bool
	tpType     string
)

func init() {
	RootCmd.AddCommand(pingCmd)
	pingCmd.Flags().IntVarP(&tries, "tries", "t", 1, "Number of tries")
	pingCmd.Flags().IntVarP(&pcktSize, "size", "s", 2, "Size of packet, in KB, default is 2KB")
	pingCmd.Flags().BoolVar(&localRoute, "local-route", false, "Calculate routes locally using cached TPD data instead of querying route finder")
	pingCmd.Flags().BoolVar(&createTp, "create-tp", false, "Create a direct transport to the target if none exists")
	pingCmd.Flags().StringVar(&tpType, "tp-type", "stcpr", "Transport type to create when using --create-tp (stcpr or sudph)")
	RootCmd.AddCommand(testCmd)
	testCmd.Flags().IntVarP(&tries, "tries", "t", 1, "Number of tries per public visors")
	testCmd.Flags().IntVarP(&pcktSize, "size", "s", 2, "Size of packet, in KB, default is 2KB")
	testCmd.Flags().IntVarP(&pubVisCount, "count", "c", 2, "Count of Public Visors for using in test.")
}

var pingCmd = &cobra.Command{
	Use:   "ping <pk>",
	Short: "Ping the visor with given pk",
	Long: `
  Creates a route to the visor with the provided public key and measures
  round-trip latency. Requires an existing transport to the target visor
  unless --create-tp is specified.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pk := internal.ParsePK(cmd.Flags(), "pk", args[0])
		pingConfig := visor.PingConfig{PK: pk, Tries: tries, PcktSize: pcktSize, LocalRoute: localRoute}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		// Create transport if requested
		if createTp {
			fmt.Printf("Creating %s transport to %s...\n", tpType, pk)
			_, err := rpcClient.AddTransport(pk, tpType, 30*time.Second)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to create transport: %w", err))
			}
			fmt.Println("Transport created.")
		}

		// Time the route setup separately
		setupStart := time.Now()
		err = rpcClient.DialPing(pingConfig)
		setupTime := time.Since(setupStart)
		internal.Catch(cmd.Flags(), err)

		fmt.Printf("Route setup: %0.2f ms\n", 1000*setupTime.Seconds())

		latencies, err := rpcClient.Ping(pingConfig)
		internal.Catch(cmd.Flags(), err)

		for i, latency := range latencies {
			internal.PrintOutput(cmd.Flags(), latency, fmt.Sprintf("Ping %d: %0.2f ms | Speed: %0.3f KB/s\n", i+1, 1000*latency.Seconds(), float64(pcktSize)/float64(latency.Seconds())))
		}
		err = rpcClient.StopPing(pk)
		internal.Catch(cmd.Flags(), err)
	},
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test the visor with public visors on network",
	Long:  "\n  Creates routes to public visors and measures round-trip latency.",
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
