// Package clirg cmd/skywire-cli/commands/rg/rg.go
package clirg

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var (
	statusJSON   bool
	statusFilter string
	statusHops   bool
)

// RootCmd is the root command for route group operations.
var RootCmd = &cobra.Command{
	Use:   "rg",
	Short: "Route group management",
	Long:  "View active route groups, their associated apps, and live traffic stats.",
	Run: func(cmd *cobra.Command, args []string) {
		// Default to listing route groups when no subcommand given
		listCmd.Run(cmd, args)
	},
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

		switch strings.ToLower(statusFilter) {
		case "", "all":
		case "initiator":
			filtered := routes[:0]
			for _, r := range routes {
				if r.Route.Initiator {
					filtered = append(filtered, r)
				}
			}
			routes = filtered
		case "responder":
			filtered := routes[:0]
			for _, r := range routes {
				if !r.Route.Initiator {
					filtered = append(filtered, r)
				}
			}
			routes = filtered
		default:
			internal.PrintFatalError(cmd.Flags(),
				fmt.Errorf("invalid --filter %q; expected all|initiator|responder", statusFilter))
		}

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
		fmt.Fprintln(w, "APP\tROLE\tREMOTE\tPORTS\tLATENCY\tTX\tRX\tUP\tDOWN\tROUTES\tMUX") //nolint:errcheck,gosec
		for _, r := range routes {
			remote := r.Route.RemotePK.String() + ".."
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
			role := "responder"
			if r.Route.Initiator {
				role = "initiator"
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n", //nolint:errcheck,gosec
				r.AppName, role, remote, ports, latency, tx, rx, up, down, nRoutes, mux)

			// Show transport details for mux routes
			if nRoutes > 1 {
				for _, tp := range r.Route.Transports {
					tpID := tp.ID.String() + ".."
					lat := "-"
					if tp.Latency > 0 {
						lat = fmt.Sprintf("%.0fms", tp.Latency)
					}
					fmt.Fprintf(w, "  └─\t\t%s\t%s\t%s\t\t\t\t\t%d\t\n", //nolint:errcheck,gosec
						tpID, tp.Type, lat, tp.FwdRuleID)
				}
			}

			// Optionally show the full forward hop path.
			if statusHops {
				if len(r.Route.Hops) == 0 {
					fmt.Fprintln(w, "    hops:\t(not recorded)") //nolint:errcheck,gosec
					continue
				}
				for j, h := range r.Route.Hops {
					tpType := h.TpType
					if tpType == "" {
						tpType = "?"
					}
					fmt.Fprintf(w, "  hop %d/%d:\t%s -> %s @ %s (%s)\n", //nolint:errcheck,gosec
						j+1, len(r.Route.Hops), h.From, h.To, h.TpID, tpType)
				}
			}
		}
		w.Flush() //nolint:errcheck,gosec
	},
}

func init() {
	listCmd.Flags().BoolVar(&statusJSON, "json", false, "output as JSON")
	listCmd.Flags().StringVar(&statusFilter, "filter", "all",
		"role filter: all | initiator | responder")
	listCmd.Flags().BoolVar(&statusHops, "hops", false,
		"also print the full forward hop path for each route group")
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
