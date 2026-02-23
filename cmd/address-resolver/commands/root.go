// Package commands cmd/address-resolver/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"
	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/internal/armetrics"
	"github.com/skycoin/skywire/pkg/address-resolver/api"
	"github.com/skycoin/skywire/pkg/address-resolver/store"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/tcpproxy"
)

const (
	redisPrefix = "address-resolver"
	redisScheme = "redis://"
)

var (
	addr            string
	udpAddr         string
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
	dmsgPort        uint16
	dmsgServerType  string
	pprofAddr       string
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
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9093", "address to bind to\n\r")
	RootCmd.Flags().StringVar(&udpAddr, "udp-addr", ":30178", "UDP address to bind to for SUDPH\n\r")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\n\r")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 2*time.Minute, "timeout for address entry expiration\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\n\r")
	RootCmd.Flags().StringVar(&tag, "tag", "address_resolver", "logging tag\n\r")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsg.DiscURL(false), "url of dmsg discovery\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
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

		if pprofAddr != "" {
			pprofMux := http.NewServeMux()

			// Register the index (which links to everything else)
			pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
			pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

			// Register profile handlers using pprof.Handler
			for _, profile := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
				pprofMux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
			}

			go func() {
				logger.Infof("Starting pprof server on %s", pprofAddr)
				server := &http.Server{
					Addr:              pprofAddr,
					Handler:           pprofMux,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       30 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
				}
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Errorf("pprof server failed: %v", err)
				}
			}()

			time.Sleep(100 * time.Millisecond)
		}

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
		arAPI := api.New(logger, transportStore, nonceStore, enableMetrics, m, dmsgAddr)

		udpListener, err := kcp.Listen(udpAddr)
		if err != nil {
			log.Fatal("Failed to open UDP listener: ", err)
		}

		go arAPI.ListenUDP(udpListener)

		if logger != nil {
			logger.Infof("Listening on %s (HTTP), %s (UDP)", addr, udpAddr)
		}

		go func() {
			if err := tcpproxy.ListenAndServe(addr, arAPI); err != nil {
				logger.Errorf("tcpproxy.ListenAndServe: %v", err)
				cancel()
			}
		}()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, dmsgServerType, logger)

			var keys cipher.PubKeys
			keys = append(keys, pk)
			dClient := direct.NewClient(direct.GetAllEntries(keys, servers), logger)
			config := &dmsg.Config{
				MinSessions:          0, // listen on all available servers
				UpdateInterval:       dmsg.DefaultUpdateInterval,
				ConnectedServersType: dmsgServerType,
			}

			dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, logger, pk, sk, dClient, config)
			if err != nil {
				logger.WithError(err).Fatal("failed to start direct dmsg client.")
			}

			defer closeDmsgDC()

			go func() {
				for {
					arAPI.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, dmsgServerType, logger)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, arAPI, dClient, dmsgPort, dmsgDC, logger); err != nil {
					logger.Errorf("dmsghttp.ListenAndServe: %v", err)
					cancel()
				}
			}()
		}

		<-ctx.Done()

		arAPI.Close()
	},
}

// Execute executes root CLI command
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
