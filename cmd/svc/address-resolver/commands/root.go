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
	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/address-resolver/api"
	armetrics "github.com/skycoin/skywire/pkg/address-resolver/metrics"
	"github.com/skycoin/skywire/pkg/address-resolver/store"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/svcmode"
)

const (
	redisPrefix = "address-resolver"
	redisScheme = "redis://"
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

// exampleJSON marshals v to indented JSON with color, returning empty string on error
func exampleJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return ""
	}
	return string(pretty.Color(b, nil))
}

// generateExamples creates example responses from actual struct types
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
	RootCmd.Flags().StringVar(&publicUDPAddr, "public-udp-address", "", "externally-reachable host:port advertised in /health for SUDPH (e.g. ar.example.com:30178)\n\rrequired for visors that reach this AR over dmsghttp; without it those visors cannot register SUDPH")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\n\r")
	// 5 min is ~3.3× the 90s client refresh interval
	// (sudphReRegisterInterval in pkg/transport/network/addrresolver/client.go),
	// giving safe margin for one or two dropped refreshes without
	// expiring a live binding.
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

		var configServers []*disc.Entry
		var configSurveyWL []cipher.PubKey
		if configPath != "" {
			c, cErr := LoadConfig(configPath)
			if cErr != nil {
				log.Fatal(cErr)
			}
			configServers, configSurveyWL = applyConfig(c)
		}

		if !strings.HasPrefix(redisURL, redisScheme) {
			redisURL = redisScheme + redisURL
		}
		storeConfig := storeconfig.Config{
			Type:     storeconfig.Redis,
			URL:      redisURL,
			Password: storeconfig.RedisPassword(),
			PoolSize: redisPoolSize,
		}

		if testing {
			storeConfig.Type = storeconfig.Memory
		}

		logger := logging.MustGetLogger(tag)
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			logger.Fatal("Invalid loglvl detected")
		}
		logging.SetLevel(lvl)

		metricsutil.ServePProf(logger, pprofAddr, "address-resolver")

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		transportStore, err := store.New(ctx, storeConfig, entryTimeout, logger)
		if err != nil {
			logger.Fatal("Failed to initialize redis store: ", err)
		}

		var whitelistPKs []string
		if whitelistKeys != "" {
			whitelistPKs = strings.Split(whitelistKeys, ",")
		}
		for _, v := range whitelistPKs {
			api.WhitelistPKs.Set(v)
		}

		nonceStore, err := httpauth.NewNonceStore(ctx, storeConfig, redisPrefix)
		if err != nil {
			logger.Fatal("Failed to initialize redis nonce store: ", err)
		}

		if keyFile != "" {
			if err := cmdutil.LoadOrGenerateKey(keyFile, &sk); err != nil {
				logger.Fatal("Failed to load keyfile: ", err)
			}
		}
		pk, err := sk.PubKey()
		if err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		metricsutil.ServeHTTPMetrics(logger, metricsAddr)

		var m armetrics.Metrics
		if metricsAddr == "" {
			m = armetrics.NewEmpty()
		} else {
			m = armetrics.NewVictoriaMetrics()
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		enableMetrics := metricsAddr != ""
		arAPI := api.New(logger, transportStore, nonceStore, enableMetrics, m, dmsgAddr, publicUDPAddr)

		// Mirror AR bind state to the DHT under salts addr:stcpr / addr:sudph.
		// Only enabled when this AR is backed by Redis — the dmsg-server DHT
		// nodes read from the same Redis and serve the entries to visors via
		// Kademlia. Memory-store deployments skip the mirror entirely.
		if redisURL != "" {
			redisHost := strings.TrimPrefix(redisURL, redisScheme)
			redisPassword := storeconfig.RedisPassword()
			stcprMirror, errSt := dht.NewRedisMirror(redisHost, redisPassword, 0, "addr:stcpr", pk, sk, logging.MustGetLogger("dht:addr-stcpr-mirror"))
			sudphMirror, errSu := dht.NewRedisMirror(redisHost, redisPassword, 0, "addr:sudph", pk, sk, logging.MustGetLogger("dht:addr-sudph-mirror"))
			switch {
			case errSt != nil:
				logger.WithError(errSt).Warn("DHT addr:stcpr mirror init failed — STCPR data won't be in DHT")
			case errSu != nil:
				logger.WithError(errSu).Warn("DHT addr:sudph mirror init failed — SUDPH data won't be in DHT")
			default:
				arAPI.SetDHTMirrors(stcprMirror, sudphMirror)
				logger.Info("DHT address mirroring enabled (via Redis)")
				go arAPI.BackfillDHTMirror(ctx, logger)
			}
		}

		udpListener, err := kcp.Listen(udpAddr)
		if err != nil {
			log.Fatal("Failed to open UDP listener: ", err)
		}

		go arAPI.ListenUDP(udpListener)
		logger.Infof("UDP listener (SUDPH) on %s", udpAddr)

		resolvedMode, err := svcmode.ResolveMode(mode, !sk.Null())
		if err != nil {
			logger.WithError(err).Fatal("invalid --mode")
		}

		embeddedServers := dmsgDiscEntries(configServers)
		surveyWL := deployment.Prod.SurveyWhitelist
		if len(configSurveyWL) > 0 {
			surveyWL = configSurveyWL
		}

		h, err := svcmode.Start(ctx, svcmode.Config{
			Mode:                resolvedMode,
			HTTPAddr:            addr,
			Handler:             arAPI,
			PK:                  pk,
			SK:                  sk,
			DmsgPort:            dmsgPort,
			DmsgDiscovery:       dmsgDisc,
			DmsgServerType:      dmsgServerType,
			EmbeddedDmsgServers: embeddedServers,
			SurveyWhitelist:     surveyWL,
			Log:                 logger,
			DisableDHT:          true,
			OnDmsgServersUpdated: func(s []string) {
				arAPI.DmsgServers = s
			},
		})
		if err != nil {
			logger.WithError(err).Fatal("failed to start listeners")
		}
		defer h.Close()

		select {
		case <-ctx.Done():
		case err := <-h.Errors():
			logger.WithError(err).Error("listener failed")
			cancel()
		}

		arAPI.Close()
	},
}

// Execute executes root CLI command
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
