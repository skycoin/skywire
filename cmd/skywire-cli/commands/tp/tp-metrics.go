// Package clitp cmd/skywire-cli/commands/tp/tp-metrics.go
package clitp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	store "github.com/skycoin/skywire/pkg/transport-discovery/store"
)

var (
	metricsDays   int
	metricsPK     string
	metricsTop    int
	metricsTpdURL string
)

func init() {
	metricsCmd.Flags().SortFlags = false
	metricsCmd.Flags().IntVarP(&metricsDays, "days", "d", 1, "number of days of metrics (0 = all, max 35)")
	metricsCmd.Flags().StringVarP(&metricsPK, "pk", "p", "", "filter by public key")
	metricsCmd.Flags().IntVarP(&metricsTop, "top", "n", 0, "show only top N visors by bandwidth (0 = all)")
	metricsCmd.Flags().StringVar(&metricsTpdURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery url")
}

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Transport discovery bandwidth metrics",
	Long: `	Query transport discovery for bandwidth metrics per visor.

	Displays total bandwidth (sent + received) per public key,
	sorted by total bandwidth descending.`,
	Run: func(cmd *cobra.Command, _ []string) {
		url := fmt.Sprintf("%s/metrics?days=%d&bandwidth=true&latency=false&edges=true",
			strings.TrimSuffix(metricsTpdURL, "/"), metricsDays)

		resp, err := http.Get(url) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to query TPD metrics: %w", err))
			return
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body) //nolint:errcheck
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("TPD returned status %d: %s", resp.StatusCode, string(body)))
			return
		}

		var metrics []store.TransportMetric
		if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to decode metrics: %w", err))
			return
		}

		// Aggregate bandwidth per public key
		type visorBW struct {
			Sent       uint64 `json:"sent"`
			Recv       uint64 `json:"recv"`
			Total      uint64 `json:"total"`
			Transports int    `json:"transports"`
		}
		byPK := make(map[string]*visorBW)

		for _, m := range metrics {
			if len(m.Edges) < 2 {
				continue
			}
			for edgeIdx, pk := range m.Edges {
				if metricsPK != "" && pk != metricsPK {
					continue
				}
				vbw, ok := byPK[pk]
				if !ok {
					vbw = &visorBW{}
					byPK[pk] = vbw
				}
				vbw.Transports++
				for _, daily := range m.Daily {
					var eb *store.EdgeBandwidth
					if edgeIdx == 0 {
						eb = daily.A
					} else {
						eb = daily.B
					}
					if eb != nil {
						vbw.Sent += eb.Sent
						vbw.Recv += eb.Recv
						vbw.Total += eb.Sent + eb.Recv
					}
				}
			}
		}

		// Sort by total bandwidth descending
		type visorEntry struct {
			PK string  `json:"pk"`
			BW visorBW `json:"bandwidth"`
		}
		var sorted []visorEntry
		for pk, bw := range byPK {
			sorted = append(sorted, visorEntry{PK: pk, BW: *bw})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].BW.Total > sorted[j].BW.Total
		})

		if metricsTop > 0 && metricsTop < len(sorted) {
			sorted = sorted[:metricsTop]
		}

		if len(sorted) == 0 {
			fmt.Fprintln(os.Stderr, "No bandwidth data found")
			return
		}

		// Print table
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
		fmt.Fprintln(w, "public_key\ttransports\tsent\trecv\ttotal") //nolint:errcheck
		var networkTotal uint64
		for _, v := range sorted {
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", //nolint:errcheck
				v.PK, v.BW.Transports,
				formatBytes(v.BW.Sent), formatBytes(v.BW.Recv), formatBytes(v.BW.Total))
			networkTotal += v.BW.Total
		}
		w.Flush() //nolint:errcheck,gosec

		summary := fmt.Sprintf("\n%d visors, %s total network bandwidth (%d days)\n",
			len(sorted), formatBytes(networkTotal), metricsDays)

		internal.PrintOutput(cmd.Flags(), sorted, b.String()+summary)
	},
}
