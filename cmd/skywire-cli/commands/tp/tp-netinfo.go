// Package clitp cmd/skywire-cli/commands/tp/tp-netinfo.go
package clitp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
)

func init() {
	tpCmd.AddCommand(tpdHealthCmd)
	tpCmd.AddCommand(tpdNetStatsCmd)
}

func tpdBaseURL() string {
	base := deployment.Prod.TransportDiscovery
	if base == "" {
		base = "http://tpd.skywire.skycoin.com"
	}
	return strings.TrimRight(base, "/")
}

func fetchJSON(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	return io.ReadAll(resp.Body)
}

var tpdHealthCmd = &cobra.Command{
	Use:   "tpd-health",
	Short: "Transport discovery health and version info",
	Run: func(cmd *cobra.Command, _ []string) {
		body, err := fetchJSON(tpdBaseURL() + "/health")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		var health struct {
			ServiceName string `json:"service_name"`
			BuildInfo   struct {
				Version string `json:"version"`
				Commit  string `json:"commit"`
				Date    string `json:"date"`
				Go      string `json:"go"`
			} `json:"build_info"`
			StartedAt   string   `json:"started_at"`
			DmsgAddress string   `json:"dmsg_address"`
			DmsgServers []string `json:"dmsg_servers"`
		}
		if err := json.Unmarshal(body, &health); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("parse error: %w", err))
		}

		msg := fmt.Sprintf(".:: Transport Discovery Health ::.\nService: %s\nVersion: %s (commit %s)\nBuilt: %s with %s\nStarted: %s\nDMSG Address: %s\nDMSG Servers: %d connected\n",
			health.ServiceName, health.BuildInfo.Version, health.BuildInfo.Commit,
			health.BuildInfo.Date, health.BuildInfo.Go, health.StartedAt,
			health.DmsgAddress, len(health.DmsgServers))

		internal.PrintOutput(cmd.Flags(), health, msg)
	},
}

var tpdNetStatsCmd = &cobra.Command{
	Use:   "net-stats",
	Short: "Network-wide transport statistics",
	Long: `Query the transport discovery for aggregate network statistics.
Shows total transport count by type and unique visor count.`,
	Run: func(cmd *cobra.Command, _ []string) {
		body, err := fetchJSON(tpdBaseURL() + "/all-transports/stats")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		var stats struct {
			TotalTransports int            `json:"total_transports"`
			ByType          map[string]int `json:"by_type"`
			UniqueVisors    int            `json:"unique_visors"`
		}
		if err := json.Unmarshal(body, &stats); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("parse error: %w", err))
		}

		msg := fmt.Sprintf(".:: Network Transport Statistics ::.\nTotal Transports: %d\nUnique Visors: %d\nBy Type:\n",
			stats.TotalTransports, stats.UniqueVisors)
		for tp, count := range stats.ByType {
			msg += fmt.Sprintf("  %s: %d\n", strings.ToUpper(tp), count)
		}

		internal.PrintOutput(cmd.Flags(), stats, msg)
	},
}
