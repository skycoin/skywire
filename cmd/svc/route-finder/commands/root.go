// Package commands cmd/svc/route-finder/commands/root.go c2-net-routing
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/rf"
)

var (
	flags          services.Flags
	dmsgDisc       string
	dmsgServerType string
	chartsAddr     string
)

func generateExamples() string {
	pk1 := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	pk2 := "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
	tpID := "e7a7f1b3c04047f89e12a0a1459b3456"

	return fmt.Sprintf(`
Request/Response Examples:

GET /health
  %s

POST /routes
  Request:  %s
  Response: %s`,
		cmdutil.ExampleJSON(map[string]interface{}{
			"build_info":   map[string]string{"version": "v1.3.29"},
			"started_at":   "2024-01-15T10:00:00Z",
			"dmsg_address": pk1 + ":80",
			"dmsg_servers": []string{pk2},
		}),
		cmdutil.ExampleJSON(map[string]interface{}{
			"edges": [][]string{{pk1, pk2}},
			"opts":  map[string]int{"min_hops": 0, "max_hops": 3},
		}),
		cmdutil.ExampleJSON(map[string]interface{}{
			pk1 + "-" + pk2: [][]map[string]interface{}{{
				{"t_id": tpID, "from": pk1, "to": pk2},
			}},
		}),
	)
}

func init() {
	flags.Bind(RootCmd.Flags(), services.FlagDefaults{Gen: "rf", Addr: ":9092", Tag: "route_finder", EntryTimeout: 10 * time.Minute})
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsg.DiscURL(false), "url of dmsg-discovery")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&chartsAddr, "charts-addr", "", "serve only the charts page over plain HTTP on this address")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Route Finder Server for skywire",
	Long: calvin.AsciiFont("route-finder") + `
Route Finder Server - finds routes between visors using transport data.

Depends: redis (shares Redis with TPD)

Production: ` + deployment.Prod.RouteFinder + `
            ` + dmsg.Prod.RouteFinder + `
Test:       ` + deployment.Test.RouteFinder + `
            ` + dmsg.Test.RouteFinder + `

HTTP Endpoints:
  GET  /health     Health check
  POST /routes     Find routes between visors
` + generateExamples() + `

Example:
  skywire cli config gen-keys | tee rf-keys.txt
  route-finder --sk $(tail -n1 rf-keys.txt)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}
		logger := logging.MustGetLogger(flags.LogTag("route_finder"))

		cfg, err := buildConfig()
		if err != nil {
			logger.WithError(err).Fatal("failed to build route-finder config")
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()
		if err := rf.New(cfg, logger).Run(ctx); err != nil {
			logger.WithError(err).Fatal("route-finder: run failed")
		}
	},
}

func buildConfig() (*rf.Config, error) {
	common, err := flags.Resolve()
	if err != nil {
		return nil, err
	}
	cfg := &rf.Config{
		Common:     common,
		ChartsAddr: chartsAddr,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  dmsgDisc,
			ServerType: dmsgServerType,
		},
	}
	if flags.ConfigPath != "" {
		fileCfg, err := rf.LoadFile(flags.ConfigPath)
		if err != nil {
			return nil, err
		}
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

func mergeFile(dst, src *rf.Config) {
	services.MergeCommon(&dst.Common, src.Common)
	dst.Dmsg.Merge(src.Dmsg)
	if src.ChartsAddr != "" {
		dst.ChartsAddr = src.ChartsAddr
	}
}

// Execute executes root CLI command.
func Execute() {
	cmdutil.RunRoot(RootCmd)
}
