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

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	skyvisor "github.com/skycoin/skywire/pkg/visor"
)

var directQuery bool

// RootCmd is the svc command
var RootCmd = &cobra.Command{
	Use:   "svc",
	Short: "Query skywire deployment services",
	Long:  "\n    Query skywire deployment services (health, stats)",
}

func init() {
	healthCmd.Flags().BoolVar(&directQuery, "direct", false, "query services directly instead of via visor RPC")
	healthCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	RootCmd.AddCommand(healthCmd)
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check health of all deployment services",
	Long: `    Check the /health endpoint of all skywire deployment services.

    By default queries via the local visor RPC (uses visor's configured URLs).
    Use --direct to query services directly from the CLI.`,
	Run: func(cmd *cobra.Command, _ []string) {
		var results []skyvisor.ServiceHealthEntry

		if !directQuery {
			// Try via visor RPC first
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				results, err = rpcClient.ServiceHealth()
				if err == nil && len(results) > 0 {
					printHealthResults(cmd, results)
					return
				}
			}
			// Fall through to direct query
			fmt.Println("(visor not available, querying services directly)")
		}

		// Direct query fallback
		results = queryServicesDirect()
		printHealthResults(cmd, results)
	},
}

func printHealthResults(cmd *cobra.Command, results []skyvisor.ServiceHealthEntry) {
	for _, r := range results {
		ver := ""
		if r.Version != "" {
			ver = " (" + r.Version + ")"
		}
		fmt.Printf("%-22s %-6s %dms%s\n", r.Name, r.Status, r.LatencyMs, ver)
	}
	internal.PrintOutput(cmd.Flags(), results, "")
}

func queryServicesDirect() []skyvisor.ServiceHealthEntry {
	services := map[string]string{
		"Transport Discovery": deployment.Prod.TransportDiscovery,
		"DMSG Discovery":      deployment.Prod.DmsgDiscovery,
		"Address Resolver":    deployment.Prod.AddressResolver,
		"Route Finder":        deployment.Prod.RouteFinder,
		"Uptime Tracker":      deployment.Prod.UptimeTracker,
		"Service Discovery":   deployment.Prod.ServiceDiscovery,
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var results []skyvisor.ServiceHealthEntry

	for name, baseURL := range services {
		if baseURL == "" {
			continue
		}
		url := strings.TrimSuffix(baseURL, "/") + "/health"
		entry := skyvisor.ServiceHealthEntry{Name: name, URL: baseURL}

		start := time.Now()
		resp, err := client.Get(url) //nolint:gosec
		entry.LatencyMs = time.Since(start).Milliseconds()

		if err != nil {
			entry.Status = "DOWN"
			results = append(results, entry)
			continue
		}

		body, _ := io.ReadAll(resp.Body) //nolint:errcheck
		resp.Body.Close()                //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			entry.Status = fmt.Sprintf("ERROR(%d)", resp.StatusCode)
			results = append(results, entry)
			continue
		}

		var health map[string]interface{}
		if json.Unmarshal(body, &health) == nil {
			if bi, ok := health["build_info"].(map[string]interface{}); ok {
				if v, ok := bi["version"].(string); ok {
					entry.Version = v
				}
			}
			if entry.Version == "" {
				if v, ok := health["version"].(string); ok {
					entry.Version = v
				}
			}
		}
		entry.Status = "OK"
		results = append(results, entry)
	}

	return results
}
