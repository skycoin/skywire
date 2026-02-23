// Package commands cmd/service-discovery/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/internal/sdmetrics"
	"github.com/skycoin/skywire/pkg/service-discovery/api"
	"github.com/skycoin/skywire/pkg/service-discovery/store"
	"github.com/skycoin/skywire/pkg/skyenv"
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

var log = logging.MustGetLogger("service-discovery")

const redisPrefix = "service-discovery"

var (
	addr           string
	metricsAddr    string
	redisURL       string
	testMode       bool
	dmsgDisc       string
	whitelistKeys  string
	sk             cipher.SecKey
	dmsgPort       uint16
	dmsgServerType string
	geoipURL       string
	pprofAddr      string
	entryTimeout   time.Duration
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
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9098", "address to bind to\n\r")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVarP(&redisURL, "redis", "r", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().StringVarP(&whitelistKeys, "whitelist-keys", "w", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().BoolVarP(&testMode, "test", "t", false, "run in test mode and disable auth")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "d", dmsg.DiscURL(false), "url of dmsg-discovery\n\r")
	RootCmd.Flags().StringVar(&geoipURL, "geoip", skyenv.GeoIP, "url of geoip service\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().VarP(&sk, "sk", "s", "dmsg secret key\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 2*time.Minute, "timeout for service entry expiration\n\r")
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
		if dmsgDisc == "" {
			dmsgDisc = dmsg.DiscURL(false)
		}
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		pk, err := sk.PubKey()
		if err != nil {
			log.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

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
				log.Infof("Starting pprof server on %s", pprofAddr)
				server := &http.Server{
					Addr:              pprofAddr,
					Handler:           pprofMux,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       30 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
				}
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Errorf("pprof server failed: %v", err)
				}
			}()

			time.Sleep(100 * time.Millisecond)
		}

		redisPassword := storeconfig.RedisPassword()
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("Failed to parse redis URL: %v", err)
		}
		opt.Password = redisPassword

		redisClient := redis.NewClient(opt)
		if _, err := redisClient.Ping(ctx).Result(); err != nil {
			log.Fatalf("Failed to connect to redis: %v", err)
		}
		log.Printf("Redis connected.")

		db, err := store.NewStore(ctx, redisClient, log, entryTimeout)
		if err != nil {
			log.Fatal("Failed to initialize redis store: ", err)
		}
		log.Printf("Service entry timeout: %v", entryTimeout)

		var nonceDB httpauth.NonceStore
		if !testMode {
			nonceStoreConfig := storeconfig.Config{
				URL:      redisURL,
				Type:     storeconfig.Redis,
				Password: storeconfig.RedisPassword(),
			}
			nonceDB, err = httpauth.NewNonceStore(ctx, nonceStoreConfig, redisPrefix)
			if err != nil {
				log.Fatal("Failed to initialize redis nonce store: ", err)
			}
		}

		metricsutil.ServeHTTPMetrics(log, metricsAddr)

		var m sdmetrics.Metrics
		if metricsAddr == "" {
			m = sdmetrics.NewEmpty()
		} else {
			m = sdmetrics.NewVictoriaMetrics()
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		// we enable metrics middleware if address is passed
		enableMetrics := metricsAddr != ""
		sdAPI := api.New(log, db, nonceDB, enableMetrics, m, dmsgAddr, geoipURL)

		var whitelistPKs []string
		if whitelistKeys != "" {
			whitelistPKs = strings.Split(whitelistKeys, ",")
		}
		for _, v := range whitelistPKs {
			api.WhitelistPKs.Set(v)
		}

		go sdAPI.RunBackgroundTasks(ctx, log)

		log.WithField("addr", addr).Info("Serving discovery API...")
		go func() {
			if err := tcpproxy.ListenAndServe(addr, sdAPI); err != nil {
				log.Errorf("ListenAndServe: %v", err)
				cancel()
			}
		}()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, dmsgServerType, log)
			config := &dmsg.Config{
				MinSessions:          0, // listen on all available servers
				UpdateInterval:       dmsg.DefaultUpdateInterval,
				ConnectedServersType: dmsgServerType,
			}
			var keys cipher.PubKeys
			keys = append(keys, pk)
			dClient := direct.NewClient(direct.GetAllEntries(keys, servers), log)

			dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, log, pk, sk, dClient, config)
			if err != nil {
				log.WithError(err).Fatal("failed to start direct dmsg client.")
			}

			defer closeDmsgDC()

			go func() {
				for {
					sdAPI.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, dmsgServerType, log)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, sdAPI, dClient, dmsgPort, dmsgDC, log); err != nil {
					log.Errorf("dmsghttp.ListenAndServe: %v", err)
					cancel()
				}
			}()
		}

		<-ctx.Done()
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
