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
	vizAddr        string
	vizPort        int
	vizCacheFile   string
	vizCacheMaxAge int
	vizTPDURL      string
	vizNoCache     bool
)

func init() {
	vizCmd.Flags().StringVarP(&vizAddr, "addr", "a", "127.0.0.1", "address to bind to")
	vizCmd.Flags().IntVarP(&vizPort, "port", "p", 8080, "port to listen on")
	vizCmd.Flags().StringVar(&vizCacheFile, "cache", filepath.Join(os.TempDir(), "tpviz-cache.json"), "cache file location")
	vizCmd.Flags().IntVarP(&vizCacheMaxAge, "max-age", "m", 5, "update cache file if older than n minutes")
	vizCmd.Flags().StringVar(&vizTPDURL, "tpd-url", deployment.Prod.TransportDiscovery, "transport discovery URL")
	vizCmd.Flags().BoolVar(&vizNoCache, "no-cache", false, "disable caching, always fetch fresh data")
}

var vizCmd = &cobra.Command{
	Use:   "viz",
	Short: "Start transport discovery visualizer server",
	Long: `Start a web-based network graph visualizer for Skywire transport discovery data.

Displays an interactive force-directed graph showing:
- Visors as nodes (sized by connection count)
- Transports as edges (colored by type: STCPR=green, SUDPH=blue, DMSG=yellow)

Data is cached locally to reduce load on the transport discovery service.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := tpviz.Config{
			Addr:        vizAddr,
			Port:        vizPort,
			CacheFile:   vizCacheFile,
			CacheMaxAge: vizCacheMaxAge,
			TPDURL:      vizTPDURL,
			NoCache:     vizNoCache,
		}

		server := tpviz.NewServer(cfg)
		if err := server.ListenAndServe(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	},
}
