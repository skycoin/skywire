// Package clirg cmd/skywire-cli/commands/rg/rg.go
package clirg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil/livetui"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	statusJSON   bool
	statusFilter string
	statusHops   bool
	statusLive   bool
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

		// --live: bubbletea watch view. Honors --filter and --hops;
		// not compatible with --json (the structured stream is its own
		// thing). Re-fetches ActiveRoutes() every tick so bandwidth
		// counters tick up in place.
		if statusLive {
			if statusJSON {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--live cannot be combined with --json"))
			}
			err := livetui.Run(func(ctx context.Context) (string, error) {
				return renderRouteGroupListLive(rpcClient)
			}, livetui.Options{
				Title:    "skywire cli rg ls",
				Interval: time.Second,
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}

		routes, err := rpcClient.ActiveRoutes()
		internal.Catch(cmd.Flags(), err)

		routes, err = filterActiveRoutes(routes)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
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
		writeRouteGroupTable(w, routes)
		w.Flush() //nolint:errcheck,gosec
	},
}

// filterActiveRoutes applies --filter (initiator/responder/all) to the
// route-group list. Returns the filtered slice or an error if the
// filter value is unknown.
func filterActiveRoutes(routes []visor.AppRouteStatus) ([]visor.AppRouteStatus, error) {
	switch strings.ToLower(statusFilter) {
	case "", "all":
		return routes, nil
	case "initiator":
		filtered := routes[:0]
		for _, r := range routes {
			if r.Route.Initiator {
				filtered = append(filtered, r)
			}
		}
		return filtered, nil
	case "responder":
		filtered := routes[:0]
		for _, r := range routes {
			if !r.Route.Initiator {
				filtered = append(filtered, r)
			}
		}
		return filtered, nil
	default:
		return nil, fmt.Errorf("invalid --filter %q; expected all|initiator|responder", statusFilter)
	}
}

// writeRouteGroupTable renders the active-route-groups table to w.
// Pulled out of listCmd so both the one-shot and --live paths share a
// single formatting implementation.
func writeRouteGroupTable(w *tabwriter.Writer, routes []visor.AppRouteStatus) {
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
}

// renderRouteGroupListLive returns a fresh snapshot of the route-group
// table as a string for the livetui watcher. Same columns as the
// one-shot path; bandwidth/latency from the latest ActiveRoutes() RPC.
func renderRouteGroupListLive(rpcClient visor.API) (string, error) {
	routes, err := rpcClient.ActiveRoutes()
	if err != nil {
		return "", err
	}
	routes, err = filterActiveRoutes(routes)
	if err != nil {
		return "", err
	}
	if len(routes) == 0 {
		return "No active route groups\n", nil
	}
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	writeRouteGroupTable(w, routes)
	if err := w.Flush(); err != nil {
		return "", err
	}
	out := b.String()
	out += fmt.Sprintf("\n%d route group(s)\n", len(routes))
	return out, nil
}

func init() {
	listCmd.Flags().BoolVar(&statusJSON, "json", false, "output as JSON")
	listCmd.Flags().StringVar(&statusFilter, "filter", "all",
		"role filter: all | initiator | responder")
	listCmd.Flags().BoolVar(&statusHops, "hops", false,
		"also print the full forward hop path for each route group")
	listCmd.Flags().BoolVarP(&statusLive, "live", "L", false,
		"live-refresh mode (bubbletea TUI, 1s tick); shows route groups + bandwidth updating in place")
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
