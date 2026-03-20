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
	metricsTree        bool
	metricsVerbose     bool
)

func init() {
	metricsCmd.Flags().SortFlags = false
	metricsCmd.Flags().IntVarP(&metricsDays, "days", "d", 1, "number of days of metrics (0 = all, max 35)")
	metricsCmd.Flags().StringVarP(&metricsPK, "pk", "p", "", "filter by public key")
	metricsCmd.Flags().IntVarP(&metricsTop, "top", "n", 0, "show only top N results by bandwidth (0 = all)")
	metricsCmd.Flags().BoolVarP(&metricsByTransport, "by-transport", "t", false, "show bandwidth per transport ID instead of per visor")
	metricsCmd.Flags().BoolVar(&metricsTree, "tree", false, "tree view: visors with their transports as children")
	metricsCmd.Flags().BoolVarP(&metricsVerbose, "verbose", "v", false, "show full public keys (with --by-transport)")
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

func formatLatency(lat *store.TransportLatency) string {
	if lat == nil || lat.Avg == 0 {
		return "-"
	}
	ms := float64(lat.Avg) / 1000.0
	if ms < 1 {
		return fmt.Sprintf("%.0fμs", float64(lat.Avg))
	}
	if ms < 1000 {
		return fmt.Sprintf("%.1fms", ms)
	}
	return fmt.Sprintf("%.2fs", ms/1000.0)
}

func formatLatencyFull(lat *store.TransportLatency) string {
	if lat == nil || lat.Avg == 0 {
		return "-"
	}
	minMs := float64(lat.Min) / 1000.0
	avgMs := float64(lat.Avg) / 1000.0
	maxMs := float64(lat.Max) / 1000.0
	return fmt.Sprintf("%.1f/%.1f/%.1fms", minMs, avgMs, maxMs)
}

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Transport discovery bandwidth metrics",
	Long: `	Query transport discovery for bandwidth metrics.

	Shows verified bandwidth — the amount both transport edges agree on.
	Default: aggregate bandwidth per visor (public key).
	With --by-transport: show bandwidth per transport ID.
	With --tree: tree view with visors and their transports.`,
	Run: func(cmd *cobra.Command, _ []string) {
		url := fmt.Sprintf("%s/metrics?days=%d&bandwidth=true&latency=true&edges=true",
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

		if metricsTree {
			printTree(cmd, metrics)
		} else if metricsByTransport {
			printByTransport(cmd, metrics)
		} else {
			printByVisor(cmd, metrics)
		}
	},
}

func printByTransport(cmd *cobra.Command, metrics []store.TransportMetric) {
	type tpEntry struct {
		ID        string                  `json:"id"`
		Type      string                  `json:"type"`
		EdgeA     string                  `json:"edge_a"`
		EdgeB     string                  `json:"edge_b"`
		Sent      uint64                  `json:"sent"`
		Recv      uint64                  `json:"recv"`
		Bandwidth uint64                  `json:"bandwidth"`
		Latency   *store.TransportLatency `json:"latency,omitempty"`
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
		if bw == 0 && m.Latency == nil {
			continue
		}
		entries = append(entries, tpEntry{
			ID:        m.ID,
			Type:      m.Type,
			EdgeA:     m.Edges[0],
			EdgeB:     m.Edges[1],
			Sent:      aToB,
			Recv:      bToA,
			Bandwidth: bw,
			Latency:   m.Latency,
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
	var networkTotal uint64

	if metricsVerbose {
		fmt.Fprintln(w, "transport_id\ttype\tedge_a\tedge_b\tsent\trecv\tbandwidth\tlatency (min/avg/max)") //nolint:errcheck
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck
				e.ID, e.Type, e.EdgeA, e.EdgeB,
				formatBytes(e.Sent), formatBytes(e.Recv),
				formatBytes(e.Bandwidth), formatLatencyFull(e.Latency))
			networkTotal += e.Bandwidth
		}
	} else {
		fmt.Fprintln(w, "transport_id\ttype\tsent\trecv\tbandwidth\tlatency") //nolint:errcheck
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck
				e.ID, e.Type,
				formatBytes(e.Sent), formatBytes(e.Recv),
				formatBytes(e.Bandwidth), formatLatency(e.Latency))
			networkTotal += e.Bandwidth
		}
	}

	w.Flush() //nolint:errcheck,gosec

	date := time.Now().UTC().Format("2006-01-02")
	summary := fmt.Sprintf("\n%d transports, %s network bandwidth (%d days) %s\n",
		len(entries), formatBytes(networkTotal), metricsDays, date)

	internal.PrintOutput(cmd.Flags(), entries, b.String()+summary)
}

func printByVisor(cmd *cobra.Command, metrics []store.TransportMetric) {
	type visorBW struct {
		Sent       uint64 `json:"sent"`
		Recv       uint64 `json:"recv"`
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

		pkA := m.Edges[0]
		pkB := m.Edges[1]

		// Edge A sent a→b, received b→a
		if metricsPK == "" || pkA == metricsPK {
			vbw, ok := byPK[pkA]
			if !ok {
				vbw = &visorBW{}
				byPK[pkA] = vbw
			}
			vbw.Transports++
			vbw.Sent += aToB
			vbw.Recv += bToA
			vbw.Bandwidth += tpBW
		}

		// Edge B sent b→a, received a→b
		if metricsPK == "" || pkB == metricsPK {
			vbw, ok := byPK[pkB]
			if !ok {
				vbw = &visorBW{}
				byPK[pkB] = vbw
			}
			vbw.Transports++
			vbw.Sent += bToA
			vbw.Recv += aToB
			vbw.Bandwidth += tpBW
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
	fmt.Fprintln(w, "public_key\ttransports\tsent\trecv\tbandwidth") //nolint:errcheck
	for _, v := range sorted {
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", //nolint:errcheck
			v.PK, v.BW.Transports,
			formatBytes(v.BW.Sent), formatBytes(v.BW.Recv),
			formatBytes(v.BW.Bandwidth))
	}
	w.Flush() //nolint:errcheck,gosec

	totalTransports := len(metrics)

	date := time.Now().UTC().Format("2006-01-02")
	summary := fmt.Sprintf("\n%d visors, %d transports, %s network bandwidth (%d days) %s\n",
		len(sorted), totalTransports, formatBytes(networkTotal), metricsDays, date)

	internal.PrintOutput(cmd.Flags(), sorted, b.String()+summary)
}

func printTree(cmd *cobra.Command, metrics []store.TransportMetric) {
	type tpInfo struct {
		ID        string                  `json:"id"`
		Type      string                  `json:"type"`
		Remote    string                  `json:"remote"`
		Sent      uint64                  `json:"sent"`
		Recv      uint64                  `json:"recv"`
		Bandwidth uint64                  `json:"bandwidth"`
		Latency   *store.TransportLatency `json:"latency,omitempty"`
	}
	type visorTree struct {
		Sent       uint64   `json:"sent"`
		Recv       uint64   `json:"recv"`
		Bandwidth  uint64   `json:"bandwidth"`
		Transports []tpInfo `json:"transports"`
	}

	byPK := make(map[string]*visorTree)
	var networkTotal uint64

	for _, m := range metrics {
		if len(m.Edges) < 2 {
			continue
		}

		aToB, bToA := verifiedBandwidth(m)
		tpBW := aToB + bToA
		networkTotal += tpBW

		pkA := m.Edges[0]
		pkB := m.Edges[1]

		if metricsPK == "" || pkA == metricsPK {
			vt, ok := byPK[pkA]
			if !ok {
				vt = &visorTree{}
				byPK[pkA] = vt
			}
			vt.Sent += aToB
			vt.Recv += bToA
			vt.Bandwidth += tpBW
			if tpBW > 0 || m.Latency != nil {
				vt.Transports = append(vt.Transports, tpInfo{
					ID: m.ID, Type: m.Type, Remote: pkB,
					Sent: aToB, Recv: bToA, Bandwidth: tpBW,
					Latency: m.Latency,
				})
			}
		}

		if metricsPK == "" || pkB == metricsPK {
			vt, ok := byPK[pkB]
			if !ok {
				vt = &visorTree{}
				byPK[pkB] = vt
			}
			vt.Sent += bToA
			vt.Recv += aToB
			vt.Bandwidth += tpBW
			if tpBW > 0 || m.Latency != nil {
				vt.Transports = append(vt.Transports, tpInfo{
					ID: m.ID, Type: m.Type, Remote: pkA,
					Sent: bToA, Recv: aToB, Bandwidth: tpBW,
					Latency: m.Latency,
				})
			}
		}
	}

	type sortEntry struct {
		PK   string
		Tree *visorTree
	}
	var sorted []sortEntry
	for pk, vt := range byPK {
		sorted = append(sorted, sortEntry{PK: pk, Tree: vt})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Tree.Bandwidth > sorted[j].Tree.Bandwidth
	})

	if metricsTop > 0 && metricsTop < len(sorted) {
		sorted = sorted[:metricsTop]
	}

	if len(sorted) == 0 {
		fmt.Fprintln(os.Stderr, "No bandwidth data found")
		return
	}

	var b bytes.Buffer
	for _, v := range sorted {
		fmt.Fprintf(&b, "%s  sent %s  recv %s  total %s  (%d transports)\n",
			v.PK,
			formatBytes(v.Tree.Sent), formatBytes(v.Tree.Recv),
			formatBytes(v.Tree.Bandwidth), len(v.Tree.Transports))

		// Sort child transports by bandwidth desc
		sort.Slice(v.Tree.Transports, func(i, j int) bool {
			return v.Tree.Transports[i].Bandwidth > v.Tree.Transports[j].Bandwidth
		})

		for i, tp := range v.Tree.Transports {
			prefix := "├── "
			if i == len(v.Tree.Transports)-1 {
				prefix = "└── "
			}
			latStr := ""
			if tp.Latency != nil && tp.Latency.Avg > 0 {
				latStr = fmt.Sprintf("  latency %s", formatLatencyFull(tp.Latency))
			}
			fmt.Fprintf(&b, "%s%s  %s  → %s  sent %s  recv %s  total %s%s\n",
				prefix, tp.ID, tp.Type, tp.Remote,
				formatBytes(tp.Sent), formatBytes(tp.Recv),
				formatBytes(tp.Bandwidth), latStr)
		}
		b.WriteString("\n")
	}

	totalTransports := len(metrics)
	date := time.Now().UTC().Format("2006-01-02")
	summary := fmt.Sprintf("%d visors, %d transports, %s network bandwidth (%d days) %s\n",
		len(sorted), totalTransports, formatBytes(networkTotal), metricsDays, date)
	b.WriteString(summary)

	internal.PrintOutput(cmd.Flags(), sorted, b.String())
}
