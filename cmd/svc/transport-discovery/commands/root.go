// Package commands cmd/transport-discovery/root.go
//
// Cobra entry point for the standalone `skywire svc tpd` binary.
// All run logic lives in pkg/services/tpd — this file is just the
// flag set, the optional --config file overlay, and the
// signal-context wiring.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/tpd"
	"github.com/skycoin/skywire/pkg/transport"
)

var (
	configPath      string
	addr            string
	metricsAddr     string
	redisURL        string
	redisPoolSize   int
	logLvl          string
	tag             string
	testing         bool
	whitelistKeys   string
	testEnvironment bool
	sk              cipher.SecKey
	keyFile         string
	dmsgPort        uint16
	dmsgServerType  string
	entryTimeout    time.Duration
	dmsgDisc        = deployment.Prod.DmsgDiscovery
	pprofAddr       string
	storeDataPath   string
	mode            string
	uptimeDB        string
)

func init() {
	RootCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to JSON config file. When set, fields below come from the config file. Generate one with: skywire cli config gen --tpd -o /etc/skywire/transport-discovery.json\n\r")
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9091", "address to bind to\n\r")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\n\r")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", tpd.DefaultEntryTimeout, "transport entry TTL (0 to disable)\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\n\r")
	RootCmd.Flags().StringVar(&tag, "tag", "transport_discovery", "logging tag\n\r")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsgDisc, "url of dmsg-discovery\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().StringVar(&keyFile, "keyfile", "", "path to file containing secret key (auto-generated if missing)\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsg-port", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().SetNormalizeFunc(cmdutil.LegacySvcFlagNormalizer)
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&storeDataPath, "store-data-path", tpd.DefaultStoreDataPath, "path for bandwidth backup files\n\r")
	RootCmd.Flags().StringVar(&mode, "mode", "", "listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)")
	RootCmd.Flags().StringVar(&uptimeDB, "uptime-db", tpd.DefaultUptimeDB, "path for the service-self uptime bbolt store (empty disables)")
}

// exampleJSON marshals v to indented JSON with color, returning empty string on error
func exampleJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// generateExamples creates example responses from actual struct types
func generateExamples() string {
	pk1 := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	pk2 := "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
	tpID := "e7a7f1b3c04047f89e12a0a1459b3456"
	sig := "00000000...00000000"

	return fmt.Sprintf(`
Request/Response Examples:

GET /health
  %s

GET /all-transports?selfTransports=hide
  %s

GET /all-transports/stats
  %s

GET /all-transports/per-key-stats
  %s

GET /transports/id:{id} (auth)
  %s

GET /transports/edge:{pk} (auth)
  [<signed_entry>, ...]

GET /transports/stats/{edge}
  %s

POST /transports/ (auth)
  Request:  %s
  Response: <same with registered timestamp>

DEL /transports/id:{id} (auth)
  Response: "transport deleted"

DEL /transports/deregister (NM auth headers: NM-PK, NM-Sign)
  Request:  %s
  Response: 200 OK

GET /bandwidth/transport/{id}?period=daily&limit=7
  %s

GET /bandwidth/visor/{pk}?period=daily&limit=7
  %s

GET /uptimes
  %s

GET /security/nonces/{pk}
  %s`,
		exampleJSON(map[string]interface{}{
			"build_info": map[string]string{"version": "v1.3.29"}, "started_at": "2024-01-15T10:00:00Z",
			"dmsg_address": pk1 + ":80", "dmsg_servers": []string{pk2},
		}),
		exampleJSON([]map[string]interface{}{{
			"entry":      map[string]interface{}{"t_id": tpID, "edges": []string{pk1, pk2}, "type": "stcpr"},
			"signatures": []string{sig, sig}, "registered": 1705312800, "latency_ms": 45.2,
		}}),
		exampleJSON(map[string]interface{}{
			"total_transports": 150, "by_type": map[string]int{"stcpr": 100, "sudph": 50}, "unique_visors": 75,
		}),
		exampleJSON(map[string]map[string]int{pk1: {"total": 5, "stcpr": 3, "sudph": 2}}),
		exampleJSON(map[string]interface{}{
			"entry":      map[string]interface{}{"t_id": tpID, "edges": []string{pk1, pk2}, "type": "stcpr"},
			"signatures": []string{sig, sig}, "registered": 1705312800,
		}),
		exampleJSON(map[string]interface{}{"total": 5, "by_type": map[string]int{"stcpr": 3, "sudph": 2}}),
		exampleJSON([]map[string]interface{}{{
			"entry":      map[string]interface{}{"t_id": tpID, "edges": []string{pk1, pk2}, "type": "stcpr"},
			"signatures": []string{sig, sig},
		}}),
		exampleJSON([]string{tpID}),
		exampleJSON([]transport.BandwidthData{{SentBytes: 1073741824, RecvBytes: 2147483648}}),
		exampleJSON(transport.BandwidthData{SentBytes: 5368709120, RecvBytes: 10737418240}),
		exampleJSON([]map[string]interface{}{{"pk": pk1, "on": true, "tp_count": 5}}),
		exampleJSON(map[string]interface{}{"nonce": 12345}),
	)
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Transport Discovery Server for skywire",
	Long: calvin.AsciiFont("transport-discovery") + `
Transport Discovery Server - registers and tracks transports between visors.

Depends: redis

Production: ` + deployment.Prod.TransportDiscovery + `
            ` + dmsg.Prod.TransportDiscovery + `
Test:       ` + deployment.Test.TransportDiscovery + `
            ` + dmsg.Test.TransportDiscovery + `

HTTP Endpoints:
  GET  /health                        Health check
  GET  /all-transports                All registered transports
  GET  /all-transports/stats          Transport statistics
  GET  /all-transports/per-key-stats  Transport counts per public key
  GET  /transports/id:{id}            Transport by ID (auth)
  GET  /transports/edge:{edge}        Transports by edge public key (auth)
  GET  /transports/stats/{edge}       Transport stats for edge
  POST /transports/                   Register transport (auth)
  DEL  /transports/id:{id}            Delete transport (auth)
  DEL  /transports/deregister         Deregister transport
  GET  /bandwidth/transport/{id}      Bandwidth for transport
  GET  /bandwidth/visor/{pk}          Bandwidth for visor
  GET  /uptimes                       Visor uptimes (proxied from UT)
  GET  /security/nonces/{pk}          Get nonce for signing
` + generateExamples() + `

Example:
  skywire cli config gen-keys | tee tpd-keys.txt
  transport-discovery --sk $(tail -n1 tpd-keys.txt)`,
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
			logger.WithError(err).Fatal("failed to build transport-discovery config")
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()
		if err := tpd.New(cfg, logger).Run(ctx); err != nil {
			logger.WithError(err).Fatal("transport-discovery: run failed")
		}
	},
}

// buildConfig collects flag values + the optional --config file
// into one tpd.Config. File values override flag values where set.
func buildConfig() (*tpd.Config, error) {
	if keyFile != "" {
		if err := cmdutil.LoadOrGenerateKey(keyFile, &sk); err != nil {
			return nil, err
		}
	}
	cfg := &tpd.Config{
		SecKey:          sk,
		Addr:            addr,
		MetricsAddr:     metricsAddr,
		PprofAddr:       pprofAddr,
		Redis:           redisURL,
		RedisPoolSize:   redisPoolSize,
		EntryTimeout:    services.Duration(entryTimeout),
		LogLevel:        logLvl,
		Tag:             tag,
		Testing:         testing,
		Mode:            mode,
		Whitelist:       commaSplit(whitelistKeys),
		TestEnvironment: testEnvironment,
		StoreDataPath:   storeDataPath,
		UptimeDB:        uptimeDB,
		DmsgPort:        dmsgPort,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  dmsgDisc,
			ServerType: dmsgServerType,
		},
	}
	if configPath != "" {
		fileCfg, err := tpd.LoadFile(configPath)
		if err != nil {
			return nil, err
		}
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

func mergeFile(dst, src *tpd.Config) {
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
	if src.EntryTimeout != 0 {
		dst.EntryTimeout = src.EntryTimeout
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
	if src.TestEnvironment {
		dst.TestEnvironment = true
	}
	if len(src.Whitelist) > 0 {
		dst.Whitelist = src.Whitelist
	}
	if len(src.SurveyWhitelist) > 0 {
		dst.SurveyWhitelist = src.SurveyWhitelist
	}
	if src.StoreDataPath != "" {
		dst.StoreDataPath = src.StoreDataPath
	}
	if src.UptimeDB != "" {
		dst.UptimeDB = src.UptimeDB
	}
	if src.DmsgPort != 0 {
		dst.DmsgPort = src.DmsgPort
	}
	if src.Dmsg.Discovery != "" {
		dst.Dmsg.Discovery = src.Dmsg.Discovery
	}
	if src.Dmsg.DiscoveryDmsg != "" {
		dst.Dmsg.DiscoveryDmsg = src.Dmsg.DiscoveryDmsg
	}
	if src.Dmsg.ServerType != "" {
		dst.Dmsg.ServerType = src.Dmsg.ServerType
	}
	if len(src.Dmsg.Servers) > 0 {
		dst.Dmsg.Servers = src.Dmsg.Servers
	}
}

func commaSplit(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
