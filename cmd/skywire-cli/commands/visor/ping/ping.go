// Package ping cmd/skywire-cli/commands/visor/ping/ping.go
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
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

var (
	tries          int
	pcktSize       int
	pubVisCount    int
	localRoute     bool
	createTp       bool
	tpType         string
	useDmsg        bool
	showRoute      bool
	pingTimeout    time.Duration
	setupTimeout   time.Duration
	allDmsgServers bool
	dmsgServerPK   string
)

func init() {
	// Register flags on RootCmd so they work with `ping <pk> --flags`
	RootCmd.Flags().IntVarP(&tries, "tries", "t", 1, "Number of tries")
	RootCmd.Flags().IntVarP(&pcktSize, "size", "s", 2, "Size of packet, in KB, default is 2KB")
	RootCmd.Flags().BoolVar(&localRoute, "local-route", false, "Calculate routes locally using cached TPD data instead of querying route finder")
	RootCmd.Flags().BoolVar(&createTp, "create-tp", false, "Create a direct transport to the target if none exists")
	RootCmd.Flags().StringVar(&tpType, "tp-type", "stcpr", "Transport type to create when using --create-tp (stcpr or sudph)")
	RootCmd.Flags().BoolVar(&useDmsg, "dmsg", false, "Ping over dmsg connection instead of skywire route")
	RootCmd.Flags().BoolVar(&showRoute, "show-route", false, "Show the route hops used for the ping")
	RootCmd.Flags().DurationVarP(&pingTimeout, "timeout", "o", 0, "Timeout per ping attempt; fails if exceeded (e.g., 5s, 30s)")
	RootCmd.Flags().DurationVar(&setupTimeout, "setup-timeout", 30*time.Second, "Timeout for route setup phase")
	RootCmd.Flags().BoolVar(&allDmsgServers, "all-servers", false, "Ping through all DMSG servers the remote visor is connected to (only with --dmsg)")
	RootCmd.Flags().StringVar(&dmsgServerPK, "via-server", "", "Ping through specific DMSG server (only with --dmsg)")

	// Set RootCmd.Run to handle direct PK arguments
	RootCmd.Run = pingCmd.Run
	RootCmd.Args = cobra.ArbitraryArgs // Allow args so subcommands or PK can be passed

	RootCmd.AddCommand(testCmd)
	testCmd.Flags().IntVarP(&tries, "tries", "t", 1, "Number of tries per public visors")
	testCmd.Flags().IntVarP(&pcktSize, "size", "s", 2, "Size of packet, in KB, default is 2KB")
	testCmd.Flags().IntVarP(&pubVisCount, "count", "c", 2, "Count of Public Visors for using in test.")

	RootCmd.AddCommand(stopAllPingsCmd)
}

var pingCmd = &cobra.Command{
	Use:   "<pk>",
	Short: "Ping the visor with given pk",
	Long: `
  Creates a route to the visor with the provided public key and measures
  round-trip latency. Requires an existing transport to the target visor
  unless --create-tp is specified.

  Use --dmsg to ping over a dmsg connection instead of a skywire route.
  This is useful for testing dmsg connectivity without transports.

  Use --all-servers with --dmsg to ping through all DMSG servers the
  remote visor is connected to. This helps identify which servers have
  the best connectivity.`,
	Run: func(cmd *cobra.Command, args []string) {
		// If no args provided, show help
		if len(args) == 0 {
			cmd.Help() //nolint:errcheck,gosec
			return
		}
		pk := internal.ParsePK(cmd.Flags(), "pk", args[0])

		// Create transport if requested (only for skywire route mode)
		if createTp && !useDmsg {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				os.Exit(1)
			}
			fmt.Printf("Creating %s transport to %s...\n", tpType, pk)
			_, err = rpcClient.AddTransport(pk, tpType, 30*time.Second, "", false, false)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to create transport: %w", err))
			}
			fmt.Println("Transport created.")
		}

		// Use gRPC streaming for real-time output
		grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to connect to gRPC server: %w", err))
		}
		defer grpcClient.Close() //nolint:errcheck

		// Create context that cancels on SIGINT/SIGTERM
		// Note: pingTimeout is passed to the server and applies only to the ping phase
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Println("\nCanceled.")
			cancel()
		}()

		// Callback for ping results
		callback := func(seq int32, latency time.Duration, isSetup bool, routeHops []rpcgrpc.RouteHopDetail, serverPK string, routeCalcTime time.Duration, pingErr error) {
			if isSetup {
				if useDmsg {
					fmt.Printf("Dmsg dial: %0.2f ms", 1000*latency.Seconds())
					if serverPK != "" {
						fmt.Printf(" (via server %s)", serverPK)
					}
					fmt.Println()
				} else {
					// Show calculation time separately if local route mode
					if routeCalcTime > 0 {
						fmt.Printf("Route calc: %0.2f ms, Setup: %0.2f ms (total: %0.2f ms)\n",
							1000*routeCalcTime.Seconds(), 1000*(latency-routeCalcTime).Seconds(), 1000*latency.Seconds())
					} else {
						fmt.Printf("Route setup: %0.2f ms\n", 1000*latency.Seconds())
					}
					// Print route if requested and available
					if showRoute && len(routeHops) > 0 {
						// Format matching route finder: [From -> To @ TpID (type) ...]
						fmt.Printf("Route: [")
						for i, hop := range routeHops {
							if i > 0 {
								fmt.Printf(" ")
							}
							fmt.Printf("%s -> %s @ %s (%s)", hop.From, hop.To, hop.TpID, hop.TpType)
						}
						fmt.Printf("]\n")
					}
				}
				return
			}
			if pingErr != nil {
				if useDmsg {
					fmt.Printf("Dmsg Ping %d: error: %v\n", seq, pingErr)
				} else {
					fmt.Printf("Ping %d: error: %v\n", seq, pingErr)
				}
				return
			}
			if useDmsg {
				internal.PrintOutput(cmd.Flags(), latency, fmt.Sprintf("Dmsg Ping %d: %0.2f ms | Speed: %0.3f KB/s\n", seq, 1000*latency.Seconds(), float64(pcktSize)/float64(latency.Seconds())))
			} else {
				internal.PrintOutput(cmd.Flags(), latency, fmt.Sprintf("Ping %d: %0.2f ms | Speed: %0.3f KB/s\n", seq, 1000*latency.Seconds(), float64(pcktSize)/float64(latency.Seconds())))
			}
		}

		if useDmsg {
			// Handle --all-servers mode: ping through each DMSG server the remote visor is connected to
			if allDmsgServers {
				servers, err := grpcClient.GetRemoteDmsgServers(ctx, pk.String())
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get remote DMSG servers: %w", err))
				}
				if len(servers) == 0 {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("remote visor not connected to any DMSG servers"))
				}
				fmt.Printf("Remote visor is connected to %d DMSG server(s)\n", len(servers))
				for i, server := range servers {
					fmt.Printf("\n=== Server %d/%d: %s ===\n", i+1, len(servers), server)
					err = grpcClient.StreamDmsgPing(ctx, pk.String(), int32(tries), int32(pcktSize), pingTimeout, server, callback)
					if err != nil {
						if ctx.Err() != nil {
							return
						}
						fmt.Printf("Error: %v\n", err)
					}
				}
				return
			}

			// Normal DMSG ping (optionally via specific server)
			fmt.Printf("Dialing dmsg ping to %s...\n", pk)
			err = grpcClient.StreamDmsgPing(ctx, pk.String(), int32(tries), int32(pcktSize), pingTimeout, dmsgServerPK, callback)
		} else {
			fmt.Printf("Dialing ping to %s...\n", pk)
			err = grpcClient.StreamPing(ctx, pk.String(), int32(tries), int32(pcktSize), localRoute, pingTimeout, setupTimeout, callback)
		}
		// Handle errors appropriately
		if err != nil {
			if ctx.Err() != nil {
				// User canceled - already printed "Canceled."
			} else {
				internal.Catch(cmd.Flags(), err)
			}
		}
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

var stopAllPingsCmd = &cobra.Command{
	Use:   "stop-all",
	Short: "Stop all active ping connections",
	Long: `Stop all active ping connections and clean up their routes.

Use this to clean up orphaned routes from interrupted ping operations.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		stopped, errs, err := rpcClient.StopAllPings()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to stop pings: %w", err))
		}

		fmt.Printf("Stopped %d ping connections\n", stopped)
		for _, e := range errs {
			fmt.Printf("  Warning: %s\n", e)
		}
	},
}
