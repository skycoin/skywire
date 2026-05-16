// Package commands cmd/service-discovery/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/skycoin/skywire/pkg/services/sd"
)

var log = logging.MustGetLogger("service-discovery")

var (
	configPath     string
	addr           string
	metricsAddr    string
	redisURL       string
	testMode       bool
	dmsgDisc       string
	whitelistKeys  string
	sk             cipher.SecKey
	keyFile        string
	dmsgPort       uint16
	dmsgServerType string
	geoipURL       string
	pprofAddr      string
	entryTimeout   time.Duration
	mode           string
)

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

	serviceExample := map[string]interface{}{
		"address": pk1 + ":3", "type": "vpn", "version": "v1.3.29",
		"geo": map[string]interface{}{"country": "US", "region": "CA", "lat": 37.77, "lon": -122.41},
	}

	return fmt.Sprintf(`
Request/Response Examples:

GET /health
  %s

GET /api/services?type=vpn&version=v1.3&country=US&quantity=10
  %s

GET /api/services/{addr}?type=vpn
  %s

POST /api/services (auth)
  Request:  %s
  Response: (same with geo data added)

DEL /api/services/{addr}?type=vpn (auth)
  Response: true

DEL /api/services/deregister/{type} (NM auth headers: NM-PK, NM-Sign)
  Request:  %s
  Response: true

GET /security/nonces/{pk}
  %s`,
		exampleJSON(map[string]interface{}{
			"build_info":   map[string]string{"version": "v1.3.29"},
			"started_at":   "2024-01-15T10:00:00Z",
			"dmsg_address": pk1 + ":80",
			"dmsg_servers": []string{pk2},
		}),
		exampleJSON([]map[string]interface{}{serviceExample}),
		exampleJSON(serviceExample),
		exampleJSON(map[string]interface{}{
			"address": pk1 + ":3", "type": "vpn", "version": "v1.3.29",
		}),
		exampleJSON([]string{pk1, pk2}),
		exampleJSON(map[string]interface{}{"nonce": 12345}),
	)
}

func init() {
	RootCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to JSON config file. Generate with: skywire cli config gen --sd -o /etc/skywire/service-discovery.json\n\r")
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9098", "address to bind to\n\r")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVarP(&redisURL, "redis", "r", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().StringVarP(&whitelistKeys, "whitelist-keys", "w", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().BoolVarP(&testMode, "test", "t", false, "run in test mode and disable auth")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "d", dmsg.DiscURL(false), "url of dmsg-discovery\n\r")
	RootCmd.Flags().StringVar(&geoipURL, "geoip", deployment.Prod.GeoIP, "url of geoip service\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().VarP(&sk, "sk", "s", "dmsg secret key\n\r")
	RootCmd.Flags().StringVar(&keyFile, "keyfile", "", "path to file containing secret key (auto-generated if missing)\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsg-port", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().SetNormalizeFunc(cmdutil.LegacySvcFlagNormalizer)
	// 5 min is ~3.3× the 90s client refresh interval
	// (skyenv.ServiceDiscUpdateInterval), giving safe margin for one
	// or two dropped refreshes without expiring a live entry.
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 5*time.Minute, "client service entry TTL (0 to disable)\n\r")
	RootCmd.Flags().StringVar(&mode, "mode", "", "listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)")
}

// RootCmd contains the root service-discovery command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Service discovery server",
	Long: calvin.AsciiFont("service-discovery") + `
Service Discovery Server - registers and discovers services (VPN, proxy, visor).

Depends: redis

Production: ` + deployment.Prod.ServiceDiscovery + `
            ` + dmsg.Prod.ServiceDiscovery + `
Test:       ` + deployment.Test.ServiceDiscovery + `
            ` + dmsg.Test.ServiceDiscovery + `

HTTP Endpoints:
  GET  /health                           Health check
  GET  /api/services                     List services (?type=proxy|vpn|visor)
  GET  /api/services/{addr}              Get specific service
  POST /api/services                     Register service (auth)
  DEL  /api/services/{addr}              Delete service (auth)
  DEL  /api/services/deregister/{type}   Deregister by type
  GET  /security/nonces/{pk}             Get nonce for signing
` + generateExamples() + `

Example:
  skywire cli config gen-keys | tee sd-keys.txt
  service-discovery --sk $(tail -n1 sd-keys.txt)`,
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}
		cfg, err := buildConfig()
		if err != nil {
			log.WithError(err).Fatal("failed to build service-discovery config")
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		if err := sd.New(cfg, log).Run(ctx); err != nil {
			log.WithError(err).Fatal("service-discovery: run failed")
		}
	},
}

// buildConfig collects flag values + the optional --config file
// into one sd.Config. File values override flag values where set.
func buildConfig() (*sd.Config, error) {
	if keyFile != "" {
		if err := cmdutil.LoadOrGenerateKey(keyFile, &sk); err != nil {
			return nil, err
		}
	}
	cfg := &sd.Config{
		SecKey:       sk,
		Addr:         addr,
		MetricsAddr:  metricsAddr,
		PprofAddr:    pprofAddr,
		Redis:        redisURL,
		EntryTimeout: services.Duration(entryTimeout),
		TestMode:     testMode,
		Mode:         mode,
		Whitelist:    commaSplit(whitelistKeys),
		GeoIP:        geoipURL,
		DmsgPort:     dmsgPort,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  dmsgDisc,
			ServerType: dmsgServerType,
		},
	}
	if configPath != "" {
		fileCfg, err := sd.LoadFile(configPath)
		if err != nil {
			return nil, err
		}
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

func mergeFile(dst, src *sd.Config) {
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
	if src.EntryTimeout != 0 {
		dst.EntryTimeout = src.EntryTimeout
	}
	if src.TestMode {
		dst.TestMode = true
	}
	if src.Mode != "" {
		dst.Mode = src.Mode
	}
	if len(src.Whitelist) > 0 {
		dst.Whitelist = src.Whitelist
	}
	if len(src.SurveyWhitelist) > 0 {
		dst.SurveyWhitelist = src.SurveyWhitelist
	}
	if src.GeoIP != "" {
		dst.GeoIP = src.GeoIP
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

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
