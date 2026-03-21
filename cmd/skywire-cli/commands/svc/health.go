// Package clisvc cmd/skywire-cli/commands/svc/health.go
package clisvc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
)

// RootCmd is the svc command
var RootCmd = &cobra.Command{
	Use:   "svc",
	Short: "Query skywire deployment services",
	Long:  "\n    Query skywire deployment services (health, stats)",
}

func init() {
	RootCmd.AddCommand(healthCmd)
}

type serviceHealth struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Latency string `json:"latency,omitempty"`
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check health of all deployment services",
	Long:  "\n    Check the /health endpoint of all skywire deployment services",
	Run: func(cmd *cobra.Command, _ []string) {
		services := map[string]string{
			"Transport Discovery": deployment.Prod.TransportDiscovery,
			"DMSG Discovery":      deployment.Prod.DmsgDiscovery,
			"Address Resolver":    deployment.Prod.AddressResolver,
			"Route Finder":        deployment.Prod.RouteFinder,
			"Uptime Tracker":      deployment.Prod.UptimeTracker,
			"Service Discovery":   deployment.Prod.ServiceDiscovery,
		}

		client := &http.Client{Timeout: 10 * time.Second}
		var results []serviceHealth

		for name, baseURL := range services {
			if baseURL == "" {
				continue
			}
			url := strings.TrimSuffix(baseURL, "/") + "/health"
			result := serviceHealth{Name: name, URL: baseURL}

			start := time.Now()
			resp, err := client.Get(url) //nolint:gosec
			latency := time.Since(start)
			result.Latency = fmt.Sprintf("%dms", latency.Milliseconds())

			if err != nil {
				result.Status = "DOWN"
				results = append(results, result)
				continue
			}
			defer resp.Body.Close() //nolint:errcheck

			if resp.StatusCode != http.StatusOK {
				result.Status = fmt.Sprintf("ERROR(%d)", resp.StatusCode)
				results = append(results, result)
				continue
			}

			body, _ := io.ReadAll(resp.Body) //nolint:errcheck
			var health map[string]interface{}
			if json.Unmarshal(body, &health) == nil {
				if bi, ok := health["build_info"].(map[string]interface{}); ok {
					if v, ok := bi["version"].(string); ok {
						result.Version = v
					}
				}
				// Some services put version at top level
				if result.Version == "" {
					if v, ok := health["version"].(string); ok {
						result.Version = v
					}
				}
			}
			result.Status = "OK"
			results = append(results, result)
		}

		// Print results
		for _, r := range results {
			status := r.Status
			ver := ""
			if r.Version != "" {
				ver = " (" + r.Version + ")"
			}
			fmt.Printf("%-22s %-6s %s%s\n", r.Name, status, r.Latency, ver)
		}

		internal.PrintOutput(cmd.Flags(), results, "")
	},
}
