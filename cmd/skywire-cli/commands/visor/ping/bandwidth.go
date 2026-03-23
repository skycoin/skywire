// Package clivisor cmd/skywire-cli/commands/visor/bandwidth.go
package ping

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

var (
	bwDuration   time.Duration
	bwPcktSize   int
	bwUseDmsg    bool
	bwDmsgOnly   bool
	bwLocalRoute bool
)

func init() {
	bandwidthCmd.Flags().DurationVarP(&bwDuration, "duration", "d", 10*time.Second, "Duration of the bandwidth test")
	bandwidthCmd.Flags().IntVarP(&bwPcktSize, "size", "s", 32, "Packet size in KB (default 32KB)")
	bandwidthCmd.Flags().BoolVar(&bwUseDmsg, "dmsg", false, "Also test over dmsg before testing via skywire route")
	bandwidthCmd.Flags().BoolVar(&bwDmsgOnly, "dmsg-only", false, "Only test over dmsg (skip skywire route test)")
	bandwidthCmd.Flags().BoolVar(&bwLocalRoute, "local-route", false, "Calculate routes locally using cached TPD data")
	RootCmd.AddCommand(bandwidthCmd)
}

var bandwidthCmd = &cobra.Command{
	Use:   "bandwidth <pk>",
	Short: "Test bandwidth to a visor",
	Long: `Performs a sustained bandwidth test to measure actual throughput.

Unlike ping which measures latency with small packets, this test:
- Sends and receives data continuously for the specified duration
- Measures real upload and download speeds in KB/s
- Provides progress updates during the test

Use --dmsg to also test over dmsg connection.
Use --dmsg-only to only test over dmsg.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pk := internal.ParsePK(cmd.Flags(), "pk", args[0])

		// Use gRPC streaming for real-time output
		grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to connect to gRPC server: %w", err))
		}
		defer grpcClient.Close() //nolint:errcheck

		// Create context that cancels on SIGINT/SIGTERM
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Println("\nCanceled.")
			cancel()
		}()

		// Progress callback
		progressCallback := func(bytesSent, bytesReceived uint64, elapsed time.Duration, uploadSpeed, downloadSpeed float64, isFinal bool, testErr error) {
			if testErr != nil {
				fmt.Printf("Error: %v\n", testErr)
				return
			}
			if isFinal {
				fmt.Printf("\n=== Final Results ===\n")
				fmt.Printf("Duration: %.2f seconds\n", elapsed.Seconds())
				fmt.Printf("Bytes Sent: %d (%.2f MB)\n", bytesSent, float64(bytesSent)/1024/1024)
				fmt.Printf("Bytes Received: %d (%.2f MB)\n", bytesReceived, float64(bytesReceived)/1024/1024)
				fmt.Printf("Upload Speed: %.2f KB/s (%.2f Mbps)\n", uploadSpeed, uploadSpeed*8/1024)
				fmt.Printf("Download Speed: %.2f KB/s (%.2f Mbps)\n", downloadSpeed, downloadSpeed*8/1024)
			} else {
				fmt.Printf("\rProgress: %.1fs | Up: %.2f KB/s | Down: %.2f KB/s", elapsed.Seconds(), uploadSpeed, downloadSpeed)
				os.Stdout.Sync() //nolint:errcheck,gosec
			}
		}

		// Test over dmsg if requested
		if bwUseDmsg || bwDmsgOnly {
			fmt.Printf("Starting DMSG bandwidth test to %s (duration: %v, packet size: %dKB)...\n", pk, bwDuration, bwPcktSize)
			err = grpcClient.StreamDmsgBandwidthTest(ctx, pk.String(), bwDuration, int32(bwPcktSize), progressCallback)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				fmt.Printf("\nDMSG Bandwidth Test Error: %v\n", err)
			}
			fmt.Println()
		}

		// Test over skywire route (unless dmsg-only)
		if !bwDmsgOnly {
			fmt.Printf("Starting bandwidth test to %s (duration: %v, packet size: %dKB)...\n", pk, bwDuration, bwPcktSize)
			err = grpcClient.StreamBandwidthTest(ctx, pk.String(), bwDuration, int32(bwPcktSize), bwLocalRoute, progressCallback)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				fmt.Printf("\nBandwidth Test Error: %v\n", err)
			}
		}
	},
}
