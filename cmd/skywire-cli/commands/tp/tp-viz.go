// Package clitp cmd/skywire-cli/commands/tp/tp-viz.go
package clitp

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/tpviz"
)

var (
	vizAddr          string
	vizPort          int
	vizCacheDirTPD   string
	vizCacheDirUT    string
	vizCacheDirSD    string
	vizCacheDirDMSG  string
	vizCacheMaxAge   int
	vizTPDURL        string
	vizUTURL         string
	vizSDURL         string
	vizDMSGURL       string
	vizNoCache       bool
	vizNoAutoRefresh bool
	vizSurveyDir     string
	vizVisor         bool // request visor to start its embedded UI server
	vizStop          bool // request visor to stop its embedded UI server
	vizStatus        bool // check visor UI server status
)

func init() {
	vizCmd.Flags().StringVarP(&vizAddr, "addr", "a", "127.0.0.1", "address to bind to (standalone mode)")
	vizCmd.Flags().IntVarP(&vizPort, "port", "p", 8080, "port to listen on (standalone mode)")
	vizCmd.Flags().StringVar(&vizCacheDirTPD, "cdt", tpviz.CacheDirFromURL(deployment.Prod.TransportDiscovery), "TPD cache dir (\"\" to disable)")
	vizCmd.Flags().StringVar(&vizCacheDirUT, "cdu", tpviz.CacheDirFromURL(deployment.Prod.UptimeTracker), "UT cache dir (\"\" to disable)")
	vizCmd.Flags().StringVar(&vizCacheDirSD, "cds", tpviz.CacheDirFromURL(deployment.Prod.ServiceDiscovery), "SD cache dir (\"\" to disable)")
	vizCmd.Flags().StringVar(&vizCacheDirDMSG, "cdd", tpviz.CacheDirFromURL(deployment.Prod.DmsgDiscovery), "DMSG cache dir (\"\" to disable)")
	vizCmd.Flags().IntVarP(&vizCacheMaxAge, "cfa", "m", 5, "update cache files if older than n minutes")
	vizCmd.Flags().StringVar(&vizTPDURL, "tpd-url", deployment.Prod.TransportDiscovery, "transport discovery URL")
	vizCmd.Flags().StringVarP(&vizUTURL, "ut-url", "w", deployment.Prod.UptimeTracker, "uptime tracker URL")
	vizCmd.Flags().StringVar(&vizSDURL, "sd-url", deployment.Prod.ServiceDiscovery, "service discovery URL")
	vizCmd.Flags().StringVar(&vizDMSGURL, "dmsg-url", deployment.Prod.DmsgDiscovery, "DMSG discovery URL")
	vizCmd.Flags().BoolVar(&vizNoCache, "no-cache", false, "disable caching, always fetch fresh data")
	vizCmd.Flags().BoolVar(&vizNoAutoRefresh, "no-auto-refresh", false, "disable auto-refresh of cache")
	vizCmd.Flags().StringVar(&vizSurveyDir, "survey-dir", "", "directory containing visor surveys for IP-based grouping (node-info.json files)")
	vizCmd.Flags().BoolVarP(&vizVisor, "visor", "v", false, "request the running visor to start its embedded UI server")
	vizCmd.Flags().BoolVar(&vizStop, "stop", false, "request the running visor to stop its embedded UI server")
	vizCmd.Flags().BoolVar(&vizStatus, "status", false, "check the status of the visor's embedded UI server")
	vizCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "visor RPC address (env: SKYWIRE_RPC)")
}

var vizCmd = &cobra.Command{
	Use:   "viz",
	Short: "Start transport discovery visualizer server",
	Long: `Start a web-based network graph visualizer for Skywire transport discovery data.

Modes:
  Standalone (default): Runs a local HTTP server showing network-wide transport data.
  Visor-embedded (--visor): Requests the running visor to start its embedded UI server,
    which has direct access to local transport/route data without RPC overhead.

Displays an interactive force-directed graph showing:
- Visors as nodes (sized by connection count)
- Transports as edges (colored by type: STCPR=green, SUDPH=blue, DMSG=yellow)
- Uptime status (green=online, red=offline, yellow=not in UT)
- Service discovery overlay (proxy, VPN, visor services)

Data is cached locally to reduce load on the discovery services.
Auto-refresh keeps the cache updated at the specified interval.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Handle visor-embedded mode via RPC
		if vizVisor || vizStop || vizStatus {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to connect to visor RPC: %v\n", err)
				os.Exit(1)
			}

			if vizStatus {
				status, err := rpcClient.UIServerStatus()
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to get UI server status: %v\n", err)
					os.Exit(1)
				}
				if status.Running {
					fmt.Printf("UI server is running at http://%s\n", status.LocalAddr)
				} else {
					fmt.Println("UI server is not running")
				}
				return
			}

			if vizStop {
				if err := rpcClient.StopUIServer(); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to stop UI server: %v\n", err)
					os.Exit(1)
				}
				fmt.Println("UI server stopped")
				return
			}

			if vizVisor {
				// Request visor to start its embedded UI server
				addr := fmt.Sprintf("%s:%d", vizAddr, vizPort)
				if err := rpcClient.StartUIServer(addr); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to start UI server: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("Visor UI server started at http://%s\n", addr)
				fmt.Println("The visor's embedded UI server has direct access to local transport/route data.")
				return
			}
		}

		// Standalone mode - run local HTTP server
		cfg := tpviz.Config{
			Addr:         vizAddr,
			Port:         vizPort,
			CacheDirTPD:  vizCacheDirTPD,
			CacheDirUT:   vizCacheDirUT,
			CacheDirSD:   vizCacheDirSD,
			CacheDirDMSG: vizCacheDirDMSG,
			CacheMaxAge:  vizCacheMaxAge,
			TPDURL:       vizTPDURL,
			UTURL:        vizUTURL,
			SDURL:        vizSDURL,
			DMSGURL:      vizDMSGURL,
			NoCache:      vizNoCache,
			AutoRefresh:  !vizNoAutoRefresh,
			SurveyDir:    vizSurveyDir,
		}

		// Standalone mode doesn't connect to visor RPC (removed due to brittleness)
		// Use --visor flag to use the visor's embedded UI server which has direct access

		server := tpviz.NewServer(cfg)
		if err := server.ListenAndServe(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	},
}
