// Package clirg cmd/skywire-cli/commands/rg/rg.go
package clirg

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var statusJSON bool

// RootCmd is the root command for route group operations.
var RootCmd = &cobra.Command{
	Use:   "rg",
	Short: "Route group management",
	Long:  "View active route groups, their associated apps, and live traffic stats.",
}

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	RootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:   "ls",
	Short: "List active route groups with app associations and live stats",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		routes, err := rpcClient.ActiveRoutes()
		internal.Catch(cmd.Flags(), err)

		if statusJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			internal.Catch(cmd.Flags(), enc.Encode(routes))
			return
		}

		if len(routes) == 0 {
			fmt.Println("No active route groups")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "APP\tREMOTE\tPORTS\tLATENCY\tTX\tRX\tUP\tDOWN\tROUTES\tMUX") //nolint:errcheck,gosec
		for _, r := range routes {
			remote := r.Route.RemotePK.String()[:8] + ".."
			ports := fmt.Sprintf("%d:%d", r.Route.LocalPort, r.Route.RemotePort)
			latency := "-"
			if r.Route.Latency > 0 {
				latency = r.Route.Latency.Truncate(time.Millisecond).String()
			}
			tx := formatBytes(r.Route.BandwidthSent)
			rx := formatBytes(r.Route.BandwidthReceived)
			up := formatSpeed(r.Route.UploadSpeed)
			down := formatSpeed(r.Route.DownloadSpeed)
			nRoutes := len(r.Route.Transports)
			mux := "no"
			if r.Route.MuxEnabled {
				mux = "yes"
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n", //nolint:errcheck,gosec
				r.AppName, remote, ports, latency, tx, rx, up, down, nRoutes, mux)

			// Show transport details for mux routes
			if nRoutes > 1 {
				for _, tp := range r.Route.Transports {
					tpID := tp.ID.String()[:8] + ".."
					lat := "-"
					if tp.Latency > 0 {
						lat = fmt.Sprintf("%.0fms", tp.Latency)
					}
					fmt.Fprintf(w, "  └─\t%s\t%s\t%s\t\t\t\t\t%d\t\n", //nolint:errcheck,gosec
						tpID, tp.Type, lat, tp.FwdRuleID)
				}
			}
		}
		w.Flush() //nolint:errcheck,gosec
	},
}

func init() {
	listCmd.Flags().BoolVar(&statusJSON, "json", false, "output as JSON")
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func formatSpeed(bps uint32) string {
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%.1fMB/s", float64(bps)/float64(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.1fKB/s", float64(bps)/float64(1<<10))
	case bps > 0:
		return fmt.Sprintf("%dB/s", bps)
	default:
		return "-"
	}
}
