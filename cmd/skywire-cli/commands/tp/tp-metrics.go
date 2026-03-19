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
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	store "github.com/skycoin/skywire/pkg/transport-discovery/store"
)

var (
	metricsDays        int
	metricsPK          string
	metricsTop         int
	metricsTpdURL      string
	metricsByTransport bool
)

func init() {
	metricsCmd.Flags().SortFlags = false
	metricsCmd.Flags().IntVarP(&metricsDays, "days", "d", 1, "number of days of metrics (0 = all, max 35)")
	metricsCmd.Flags().StringVarP(&metricsPK, "pk", "p", "", "filter by public key")
	metricsCmd.Flags().IntVarP(&metricsTop, "top", "n", 0, "show only top N results by bandwidth (0 = all)")
	metricsCmd.Flags().BoolVarP(&metricsByTransport, "by-transport", "t", false, "show bandwidth per transport ID instead of per visor")
	metricsCmd.Flags().StringVar(&metricsTpdURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery url")
}

// verifiedBandwidth returns the bandwidth both edges agree on for a transport.
// A→B verified = min(A.Sent, B.Recv), B→A verified = min(A.Recv, B.Sent)
func verifiedBandwidth(m store.TransportMetric) (aToB, bToA uint64) {
	for _, daily := range m.Daily {
		if daily.A != nil && daily.B != nil {
			aToB += minBW(daily.A.Sent, daily.B.Recv)
			bToA += minBW(daily.A.Recv, daily.B.Sent)
		} else if daily.A != nil {
			aToB += daily.A.Sent
			bToA += daily.A.Recv
		} else if daily.B != nil {
			aToB += daily.B.Recv
			bToA += daily.B.Sent
		}
	}
	return
}

func minBW(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Transport discovery bandwidth metrics",
	Long: `	Query transport discovery for bandwidth metrics.

	Shows verified bandwidth — the amount both transport edges agree on.
	Default: aggregate bandwidth per visor (public key).
	With --by-transport: show bandwidth per transport ID.`,
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

		if metricsByTransport {
			printByTransport(cmd, metrics)
		} else {
			printByVisor(cmd, metrics)
		}
	},
}

func printByTransport(cmd *cobra.Command, metrics []store.TransportMetric) {
	type tpEntry struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		EdgeA     string `json:"edge_a"`
		EdgeB     string `json:"edge_b"`
		AtoB      uint64 `json:"a_to_b"`
		BtoA      uint64 `json:"b_to_a"`
		Bandwidth uint64 `json:"bandwidth"`
	}

	var entries []tpEntry
	for _, m := range metrics {
		if len(m.Edges) < 2 {
			continue
		}
		if metricsPK != "" && m.Edges[0] != metricsPK && m.Edges[1] != metricsPK {
			continue
		}
		aToB, bToA := verifiedBandwidth(m)
		bw := aToB + bToA
		if bw == 0 {
			continue
		}
		entries = append(entries, tpEntry{
			ID:        m.ID,
			Type:      m.Type,
			EdgeA:     m.Edges[0],
			EdgeB:     m.Edges[1],
			AtoB:      aToB,
			BtoA:      bToA,
			Bandwidth: bw,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Bandwidth > entries[j].Bandwidth
	})

	if metricsTop > 0 && metricsTop < len(entries) {
		entries = entries[:metricsTop]
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No bandwidth data found")
		return
	}

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
	fmt.Fprintln(w, "transport_id\ttype\tedge_a\tedge_b\ta→b\tb→a\tbandwidth") //nolint:errcheck
	var networkTotal uint64
	for _, e := range entries {
		edgeA := e.EdgeA
		edgeB := e.EdgeB
		if len(edgeA) > 16 {
			edgeA = edgeA[:16] + "..."
		}
		if len(edgeB) > 16 {
			edgeB = edgeB[:16] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck
			e.ID, e.Type, edgeA, edgeB,
			formatBytes(e.AtoB), formatBytes(e.BtoA),
			formatBytes(e.Bandwidth))
		networkTotal += e.Bandwidth
	}
	w.Flush() //nolint:errcheck,gosec

	date := time.Now().UTC().Format("2006-01-02")
	summary := fmt.Sprintf("\n%d transports, %s network bandwidth (%d days) %s\n",
		len(entries), formatBytes(networkTotal), metricsDays, date)

	internal.PrintOutput(cmd.Flags(), entries, b.String()+summary)
}

func printByVisor(cmd *cobra.Command, metrics []store.TransportMetric) {
	type visorBW struct {
		Bandwidth  uint64 `json:"bandwidth"`
		Transports int    `json:"transports"`
	}
	byPK := make(map[string]*visorBW)

	var networkTotal uint64

	for _, m := range metrics {
		if len(m.Edges) < 2 {
			continue
		}

		aToB, bToA := verifiedBandwidth(m)
		tpBW := aToB + bToA
		networkTotal += tpBW

		// Edge A's share: data it sent (a→b) + data it received (b→a)
		edgeABW := aToB + bToA
		// Edge B's share is the same (same data, other perspective)
		edgeBBW := aToB + bToA

		pkA := m.Edges[0]
		pkB := m.Edges[1]

		if metricsPK == "" || pkA == metricsPK {
			vbw, ok := byPK[pkA]
			if !ok {
				vbw = &visorBW{}
				byPK[pkA] = vbw
			}
			vbw.Transports++
			vbw.Bandwidth += edgeABW
		}

		if metricsPK == "" || pkB == metricsPK {
			vbw, ok := byPK[pkB]
			if !ok {
				vbw = &visorBW{}
				byPK[pkB] = vbw
			}
			vbw.Transports++
			vbw.Bandwidth += edgeBBW
		}
	}

	type visorEntry struct {
		PK string  `json:"pk"`
		BW visorBW `json:"bandwidth"`
	}
	var sorted []visorEntry
	for pk, bw := range byPK {
		sorted = append(sorted, visorEntry{PK: pk, BW: *bw})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].BW.Bandwidth > sorted[j].BW.Bandwidth
	})

	if metricsTop > 0 && metricsTop < len(sorted) {
		sorted = sorted[:metricsTop]
	}

	if len(sorted) == 0 {
		fmt.Fprintln(os.Stderr, "No bandwidth data found")
		return
	}

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
	fmt.Fprintln(w, "public_key\ttransports\tbandwidth") //nolint:errcheck
	for _, v := range sorted {
		fmt.Fprintf(w, "%s\t%d\t%s\n", //nolint:errcheck
			v.PK, v.BW.Transports, formatBytes(v.BW.Bandwidth))
	}
	w.Flush() //nolint:errcheck,gosec

	date := time.Now().UTC().Format("2006-01-02")
	summary := fmt.Sprintf("\n%d visors, %s network bandwidth (%d days) %s\n",
		len(sorted), formatBytes(networkTotal), metricsDays, date)

	internal.PrintOutput(cmd.Flags(), sorted, b.String()+summary)
}
