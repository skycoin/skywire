// Package clisvc cmd/skywire-cli/commands/svc/health.go
package clisvc

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
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
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "(visor RPC ServiceHealth failed: %v)\n", err)
				} else if len(results) > 0 {
					printHealthResults(cmd, results)
					return
				}
			}
			// Fall through to direct query
			fmt.Println("(visor not available, querying services directly)")
		}

		// Direct query fallback
		results = queryServicesDirect(cmd.Flags())
		printHealthResults(cmd, results)
	},
}

// extractPK pulls the public key from a dmsg:// or http:// URL.
// For dmsg:// URLs the host is the PK (optionally with :port).
// For http:// URLs there's no PK to extract, so returns "".
func extractPK(rawURL string) string {
	if strings.HasPrefix(rawURL, "dmsg://") {
		host := strings.TrimPrefix(rawURL, "dmsg://")
		// Strip path
		if i := strings.Index(host, "/"); i >= 0 {
			host = host[:i]
		}
		// Strip port
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		if len(host) == 66 { // hex-encoded PK length
			return host
		}
	}
	return ""
}

func printHealthResults(cmd *cobra.Command, results []skyvisor.ServiceHealthEntry) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATUS\tLATENCY\tTRANSPORT\tVERSION\tPK") //nolint:errcheck,gosec
	for _, r := range results {
		transport := r.Transport
		if transport == "" {
			transport = "-"
		}
		ver := r.Version
		if ver == "" {
			ver = "-"
		}
		pk := extractPK(r.URL)
		if pk == "" {
			pk = "-"
		}
		status := r.Status
		latency := fmt.Sprintf("%dms", r.LatencyMs)
		if r.Status != "OK" {
			latency = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, status, latency, transport, ver, pk) //nolint:errcheck,gosec
	}
	tw.Flush() //nolint:errcheck,gosec
	internal.PrintOutput(cmd.Flags(), results, "")
}

func queryServicesDirect(cmdFlags *pflag.FlagSet) []skyvisor.ServiceHealthEntry {
	services := map[string]string{
		"Transport Discovery": deployment.Prod.TransportDiscovery,
		"DMSG Discovery":      deployment.Prod.DmsgDiscovery,
		"Address Resolver":    deployment.Prod.AddressResolver,
		"Route Finder":        deployment.Prod.RouteFinder,
		"Uptime Tracker":      deployment.Prod.UptimeTracker,
		"Service Discovery":   deployment.Prod.ServiceDiscovery,
	}

	var results []skyvisor.ServiceHealthEntry

	for name, baseURL := range services {
		if baseURL == "" {
			continue
		}
		url := strings.TrimSuffix(baseURL, "/") + "/health"
		entry := skyvisor.ServiceHealthEntry{Name: name, URL: baseURL}

		start := time.Now()
		body, err := clirpc.FetchServiceURL(cmdFlags, url)
		entry.LatencyMs = time.Since(start).Milliseconds()

		if err != nil {
			entry.Status = "DOWN"
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
