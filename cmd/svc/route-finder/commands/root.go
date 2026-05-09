// Package commands cmd/route-finder/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services/rf"
)

var (
	configPath     string
	addr           string
	metricsAddr    string
	redisURL       string
	redisPoolSize  int
	logLvl         string
	tag            string
	testing        bool
	dmsgDisc       string
	sk             cipher.SecKey
	keyFile        string
	dmsgPort       uint16
	dmsgServerType string
	pprofAddr      string
	mode           string
)

func exampleJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return ""
	}
	return string(pretty.Color(b, nil))
}

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
		exampleJSON(map[string]interface{}{
			"build_info":   map[string]string{"version": "v1.3.29"},
			"started_at":   "2024-01-15T10:00:00Z",
			"dmsg_address": pk1 + ":80",
			"dmsg_servers": []string{pk2},
		}),
		exampleJSON(map[string]interface{}{
			"edges": [][]string{{pk1, pk2}},
			"opts":  map[string]int{"min_hops": 0, "max_hops": 3},
		}),
		exampleJSON(map[string]interface{}{
			pk1 + "-" + pk2: [][]map[string]interface{}{{
				{"t_id": tpID, "from": pk1, "to": pk2},
			}},
		}),
	)
}

func init() {
	RootCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to JSON config file. Generate with `skywire cli config gen --rf -o /etc/skywire/route-finder.json`.\n\r")
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9092", "address to bind to\n\r")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\n\r")
	RootCmd.Flags().StringVar(&tag, "tag", "route_finder", "logging tag\n\r")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsg.DiscURL(false), "url of dmsg discovery\n\r")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().StringVar(&keyFile, "keyfile", "", "path to file containing secret key (auto-generated if missing)\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&mode, "mode", "", "listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)")
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
		logger := logging.MustGetLogger(tag)

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
	if keyFile != "" {
		if err := cmdutil.LoadOrGenerateKey(keyFile, &sk); err != nil {
			return nil, err
		}
	}
	cfg := &rf.Config{
		SecKey:        sk,
		Addr:          addr,
		MetricsAddr:   metricsAddr,
		PprofAddr:     pprofAddr,
		Redis:         redisURL,
		RedisPoolSize: redisPoolSize,
		LogLevel:      logLvl,
		Tag:           tag,
		Testing:       testing,
		Mode:          mode,
		DmsgPort:      dmsgPort,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  dmsgDisc,
			ServerType: dmsgServerType,
		},
	}
	if configPath != "" {
		fileCfg, err := rf.LoadFile(configPath)
		if err != nil {
			return nil, err
		}
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

func mergeFile(dst, src *rf.Config) {
	if src.SecKey != (cipher.SecKey{}) {
		dst.SecKey = src.SecKey
	}
	if src.Addr != "" {
		dst.Addr = src.Addr
	}
	if src.MetricsAddr != "" {
		dst.MetricsAddr = src.MetricsAddr
	}
	if src.PprofAddr != "" {
		dst.PprofAddr = src.PprofAddr
	}
	if src.Redis != "" {
		dst.Redis = src.Redis
	}
	if src.RedisPoolSize > 0 {
		dst.RedisPoolSize = src.RedisPoolSize
	}
	if src.LogLevel != "" {
		dst.LogLevel = src.LogLevel
	}
	if src.Tag != "" {
		dst.Tag = src.Tag
	}
	if src.Testing {
		dst.Testing = true
	}
	if src.Mode != "" {
		dst.Mode = src.Mode
	}
	if len(src.SurveyWhitelist) > 0 {
		dst.SurveyWhitelist = src.SurveyWhitelist
	}
	if src.DmsgPort != 0 {
		dst.DmsgPort = src.DmsgPort
	}
	if src.Dmsg.Discovery != "" {
		dst.Dmsg.Discovery = src.Dmsg.Discovery
	}
	if src.Dmsg.ServerType != "" {
		dst.Dmsg.ServerType = src.Dmsg.ServerType
	}
	if len(src.Dmsg.Servers) > 0 {
		dst.Dmsg.Servers = src.Dmsg.Servers
	}
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
