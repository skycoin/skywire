// Package clitp cmd/skywire-cli/commands/tp/tp-viz.go
package clitp

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/tpviz"
)

var (
	vizAddr          string
	vizPort          int
	vizCacheFile     string
	vizCacheFileUT   string
	vizCacheFileSD   string
	vizCacheMaxAge   int
	vizTPDURL        string
	vizUTURL         string
	vizSDURL         string
	vizNoCache       bool
	vizNoAutoRefresh bool
	vizSurveyDir     string
)

func init() {
	vizCmd.Flags().StringVarP(&vizAddr, "addr", "a", "127.0.0.1", "address to bind to")
	vizCmd.Flags().IntVarP(&vizPort, "port", "p", 8080, "port to listen on")
	vizCmd.Flags().StringVar(&vizCacheFile, "cache", filepath.Join(os.TempDir(), "tpd.json"), "TPD cache file location")
	vizCmd.Flags().StringVar(&vizCacheFileUT, "cache-ut", filepath.Join(os.TempDir(), "ut.json"), "uptime tracker cache file location")
	vizCmd.Flags().StringVar(&vizCacheFileSD, "cache-sd", filepath.Join(os.TempDir(), "sd.json"), "service discovery cache file location")
	vizCmd.Flags().IntVarP(&vizCacheMaxAge, "max-age", "m", 5, "update cache file if older than n minutes")
	vizCmd.Flags().StringVar(&vizTPDURL, "tpd-url", deployment.Prod.TransportDiscovery, "transport discovery URL")
	vizCmd.Flags().StringVarP(&vizUTURL, "ut-url", "w", deployment.Prod.UptimeTracker, "uptime tracker URL")
	vizCmd.Flags().StringVar(&vizSDURL, "sd-url", deployment.Prod.ServiceDiscovery, "service discovery URL")
	vizCmd.Flags().BoolVar(&vizNoCache, "no-cache", false, "disable caching, always fetch fresh data")
	vizCmd.Flags().BoolVar(&vizNoAutoRefresh, "no-auto-refresh", false, "disable auto-refresh of cache")
	vizCmd.Flags().StringVar(&vizSurveyDir, "survey-dir", "", "directory containing visor surveys for IP-based grouping (node-info.json files)")
}

var vizCmd = &cobra.Command{
	Use:   "viz",
	Short: "Start transport discovery visualizer server",
	Long: `Start a web-based network graph visualizer for Skywire transport discovery data.

Displays an interactive force-directed graph showing:
- Visors as nodes (sized by connection count)
- Transports as edges (colored by type: STCPR=green, SUDPH=blue, DMSG=yellow)
- Uptime status (green=online, red=offline, yellow=not in UT)
- Service discovery overlay (proxy, VPN, visor services)

Data is cached locally to reduce load on the discovery services.
Auto-refresh keeps the cache updated at the specified interval.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := tpviz.Config{
			Addr:        vizAddr,
			Port:        vizPort,
			CacheFile:   vizCacheFile,
			CacheFileUT: vizCacheFileUT,
			CacheFileSD: vizCacheFileSD,
			CacheMaxAge: vizCacheMaxAge,
			TPDURL:      vizTPDURL,
			UTURL:       vizUTURL,
			SDURL:       vizSDURL,
			NoCache:     vizNoCache,
			AutoRefresh: !vizNoAutoRefresh,
			SurveyDir:   vizSurveyDir,
		}

		server := tpviz.NewServer(cfg)
		if err := server.ListenAndServe(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	},
}
