// Package commands cmd/address-resolver/commands/root.go
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
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/address-resolver/api"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services/ar"
)

var (
	configPath      string
	addr            string
	udpAddr         string
	publicUDPAddr   string
	metricsAddr     string
	redisURL        string
	redisPoolSize   int
	entryTimeout    time.Duration
	tag             string
	logLvl          string
	testing         bool
	dmsgDisc        string
	whitelistKeys   string
	testEnvironment bool
	sk              cipher.SecKey
	keyFile         string
	dmsgPort        uint16
	dmsgServerType  string
	pprofAddr       string
	mode            string
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

	return fmt.Sprintf(`
Request/Response Examples:

GET /health
  %s

POST /bind/stcpr (auth)
  Request:  %s
  Response: 200 OK

DEL /bind/stcpr (auth)
  Response: 200 OK

GET /resolve/stcpr/{pk}
  %s

GET /resolve/sudph/{pk}
  %s

GET /transports
  %s

DEL /deregister/{network} (NM auth headers: NM-PK, NM-Sign)
  Request:  %s
  Response: 200 OK

GET /security/nonces/{pk}
  %s`,
		exampleJSON(map[string]interface{}{
			"build_info":   map[string]string{"version": "v1.3.29"},
			"started_at":   "2024-01-15T10:00:00Z",
			"dmsg_address": pk1 + ":80",
			"dmsg_servers": []string{pk2},
		}),
		exampleJSON(map[string]interface{}{"port": 30178}),
		exampleJSON(map[string]string{"addr": "192.168.1.100:30178"}),
		exampleJSON(map[string]interface{}{
			"addr":      "192.168.1.100:30178",
			"handshake": "<base64_handshake_data>",
		}),
		exampleJSON(api.ArData{Sudph: []string{pk1}, Stcpr: []string{pk1, pk2}}),
		exampleJSON([]string{pk1, pk2}),
		exampleJSON(map[string]interface{}{"nonce": 12345}),
	)
}

func init() {
	RootCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to JSON config file. Generate with `skywire cli config gen --ar -o /etc/skywire/address-resolver.json`.\n\r")
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9093", "address to bind to\n\r")
	RootCmd.Flags().StringVar(&udpAddr, "udp-addr", ":30178", "UDP address to bind to for SUDPH\n\r")
	RootCmd.Flags().StringVar(&publicUDPAddr, "public-udp-address", "", "externally-reachable host:port advertised in /health for SUDPH\n\rrequired for visors that reach this AR over dmsghttp")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\n\r")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 5*time.Minute, "address binding TTL (0 to disable)\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\n\r")
	RootCmd.Flags().StringVar(&tag, "tag", "address_resolver", "logging tag\n\r")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsg.DiscURL(false), "url of dmsg discovery\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
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
	Short: "Address Resolver Server for skywire",
	Long: calvin.AsciiFont("address-resolver") + `
Address Resolver Server - resolves visor addresses for STCPR/SUDPH connections.

Depends: redis

Production: ` + deployment.Prod.AddressResolver + `
            ` + dmsg.Prod.AddressResolver + `
Test:       ` + deployment.Test.AddressResolver + `
            ` + dmsg.Test.AddressResolver + `

HTTP Endpoints:
  GET  /health                  Health check
  POST /bind/stcpr              Bind STCPR address (auth)
  DEL  /bind/stcpr              Unbind STCPR address (auth)
  GET  /resolve/{type}/{pk}     Resolve address by type and PK
  GET  /transports              List transports
  DEL  /deregister/{network}    Deregister from network
  GET  /security/nonces/{pk}    Get nonce for signing
` + generateExamples() + `

Note: the specified UDP port must be accessible from the internet for SUDPH.

Example:
  skywire cli config gen-keys > ar-config.json
  skywire svc ar --addr ":9093" --redis "redis://localhost:6379" --sk $(tail -n1 ar-config.json)`,
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
			logger.WithError(err).Fatal("failed to build address-resolver config")
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()
		if err := ar.New(cfg, logger).Run(ctx); err != nil {
			logger.WithError(err).Fatal("address-resolver: run failed")
		}
	},
}

func buildConfig() (*ar.Config, error) {
	if keyFile != "" {
		if err := cmdutil.LoadOrGenerateKey(keyFile, &sk); err != nil {
			return nil, err
		}
	}
	cfg := &ar.Config{
		SecKey:          sk,
		Addr:            addr,
		UDPAddr:         udpAddr,
		PublicUDPAddr:   publicUDPAddr,
		MetricsAddr:     metricsAddr,
		PprofAddr:       pprofAddr,
		Redis:           redisURL,
		RedisPoolSize:   redisPoolSize,
		EntryTimeout:    entryTimeout,
		Tag:             tag,
		LogLevel:        logLvl,
		Testing:         testing,
		Mode:            mode,
		Whitelist:       commaSplit(whitelistKeys),
		TestEnvironment: testEnvironment,
		DmsgPort:        dmsgPort,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  dmsgDisc,
			ServerType: dmsgServerType,
		},
	}
	if configPath != "" {
		fileCfg, err := ar.LoadFile(configPath)
		if err != nil {
			return nil, err
		}
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

func mergeFile(dst, src *ar.Config) {
	if src.SecKey != (cipher.SecKey{}) {
		dst.SecKey = src.SecKey
	}
	if src.Addr != "" {
		dst.Addr = src.Addr
	}
	if src.UDPAddr != "" {
		dst.UDPAddr = src.UDPAddr
	}
	if src.PublicUDPAddr != "" {
		dst.PublicUDPAddr = src.PublicUDPAddr
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
	if src.Tag != "" {
		dst.Tag = src.Tag
	}
	if src.LogLevel != "" {
		dst.LogLevel = src.LogLevel
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

// Execute executes root CLI command
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
