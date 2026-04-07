// Package clitp cmd/skywire-cli/commands/tp/tp-stats.go
package clitp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
)

var (
	statsTop    int
	statsType   string
	statsMinTps int
)

func init() {
	tpdStatsCmd.Flags().IntVarP(&statsTop, "top", "n", 0, "show top N visors by transport count (0 = all)")
	tpdStatsCmd.Flags().StringVarP(&statsType, "type", "t", "", "filter by transport type (e.g. stcpr, sudph)")
	tpdStatsCmd.Flags().IntVar(&statsMinTps, "min", 0, "minimum transport count to display")
	tpCmd.AddCommand(tpdStatsCmd)
}

type visorStats struct {
	PK      string
	Total   int
	ByType  map[string]int
}

var tpdStatsCmd = &cobra.Command{
	Use:   "tpd-stats",
	Short: "List visors by transport count from transport discovery",
	Long: `Query the transport discovery for per-visor transport statistics.
Shows transport counts broken down by type, sorted by total.

Examples:
  skywire cli tp tpd-stats --top 20
  skywire cli tp tpd-stats --type stcpr --min 5
  skywire cli tp tpd-stats --json`,
	Run: func(cmd *cobra.Command, _ []string) {
		tpdURL := deployment.Prod.TransportDiscovery
		if tpdURL == "" {
			tpdURL = "http://tpd.skywire.skycoin.com"
		}
		statsURL := strings.TrimRight(tpdURL, "/") + "/all-transports/per-key-stats"

		resp, err := http.Get(statsURL) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to fetch TPD stats: %w", err))
		}
		defer resp.Body.Close() //nolint:errcheck

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to read response: %w", err))
		}

		// Parse: {"pk1": {"total": 15, "stcpr": 1, "sudph": 14}, ...}
		var raw map[string]map[string]int
		if err := json.Unmarshal(body, &raw); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse response: %w", err))
		}

		// Build sorted list
		var visors []visorStats
		for pk, counts := range raw {
			total := counts["total"]
			if total == 0 {
				for _, c := range counts {
					total += c
				}
			}

			// Filter by type
			if statsType != "" {
				typeCount, ok := counts[statsType]
				if !ok || typeCount == 0 {
					continue
				}
			}

			// Filter by minimum
			if statsMinTps > 0 && total < statsMinTps {
				continue
			}

			byType := make(map[string]int)
			for k, v := range counts {
				if k != "total" {
					byType[k] = v
				}
			}
			visors = append(visors, visorStats{PK: pk, Total: total, ByType: byType})
		}

		// Sort by total descending
		sort.Slice(visors, func(i, j int) bool {
			return visors[i].Total > visors[j].Total
		})

		// Limit
		if statsTop > 0 && len(visors) > statsTop {
			visors = visors[:statsTop]
		}

		// JSON output
		jsonOut := struct {
			Count  int          `json:"count"`
			Visors []visorStats `json:"visors"`
		}{
			Count:  len(visors),
			Visors: visors,
		}

		// Text output
		var buf strings.Builder
		w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "PK\tTOTAL\tSTCPR\tSUDPH\tDMSG\n")
		for _, v := range visors {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\n",
				v.PK, v.Total, v.ByType["stcpr"], v.ByType["sudph"], v.ByType["dmsg"])
		}
		w.Flush()
		fmt.Fprintf(&buf, "\nTotal visors: %d\n", len(visors))

		internal.PrintOutput(cmd.Flags(), jsonOut, buf.String())

		if statsTop == 0 && statsMinTps == 0 && statsType == "" {
			fmt.Fprintf(os.Stderr, "Showing all %d visors. Use --top N to limit.\n", len(visors))
		}
	},
}
