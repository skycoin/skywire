// Package commands cmd/uptime-tracker/commands/root.go
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
	"gorm.io/gorm"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/internal/pg"
	"github.com/skycoin/skywire/internal/utmetrics"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/geo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/tcpproxy"
	"github.com/skycoin/skywire/pkg/uptime-tracker/api"
	"github.com/skycoin/skywire/pkg/uptime-tracker/store"
)

const (
	statusFailure = 1
	redisPrefix   = "uptime-tracker"
	redisScheme   = "redis://"
)

var (
	addr              string
	pAddr             string
	metricsAddr       string
	redisURL          string
	redisPoolSize     int
	pgHost            string
	pgPort            string
	pgMaxOpenConn     int
	logEnabled        bool
	tag               string
	geoipURL          string
	enableLoadTesting bool
	testing           bool
	dmsgDisc          string
	sk                cipher.SecKey
	dmsgPort          uint16
	storeDataCutoff   int
	storeDataPath     string
	pprofAddr         string
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9096", "address to bind to\n\r")
	RootCmd.Flags().StringVarP(&pAddr, "private-addr", "p", ":9086", "private address to bind to\n\r")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", ":2121", "address to bind metrics API to\n\r")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\n\r")
	RootCmd.Flags().StringVar(&pgHost, "pg-host", "localhost", "host of postgres\n\r")
	RootCmd.Flags().StringVar(&pgPort, "pg-port", "5432", "port of postgres\n\r")
	RootCmd.Flags().IntVar(&pgMaxOpenConn, "pg-max-open-conn", 60, "maximum open connection of db\n\r")
	RootCmd.Flags().IntVar(&storeDataCutoff, "store-data-cutoff", 7, "number of days data store in db\n\r")
	RootCmd.Flags().StringVar(&storeDataPath, "store-data-path", "/var/lib/skywire/ut/daily", "path of db daily data store\n\r")
	RootCmd.Flags().BoolVarP(&logEnabled, "log", "l", true, "enable request logging\n\r")
	RootCmd.Flags().StringVar(&tag, "tag", "uptime_tracker", "logging tag\n\r")
	RootCmd.Flags().StringVar(&geoipURL, "geoip", skyenv.GeoIP, "url of geoip service\n\r")
	RootCmd.Flags().BoolVar(&enableLoadTesting, "enable-load-testing", false, "enable load testing")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsg.DiscURL(false), "url of dmsg discovery\n\r")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
}

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
	exPK1 := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	exPK2 := "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"

	return fmt.Sprintf(`
Request/Response Examples:

GET /health
  %s

GET /v4/update (auth)
  Response: 200 OK

GET /visors
  %s

GET /uptimes
  %s

GET /uptimes?v=v2
  %s

GET /uptimes?status=on
  (same as /uptimes, filtered to online visors only)

GET /uptime/{pk}
  %s

GET /dashboard?length=6
  Response: HTML bar chart of monthly node counts

GET /visor-ips?month=all (private API)
  %s

GET /security/nonces/{pk}
  %s`,
		exampleJSON(map[string]interface{}{
			"build_info":   map[string]string{"version": "v1.3.29"},
			"started_at":   "2024-01-15T10:00:00Z",
			"dmsg_address": exPK1 + ":80",
			"dmsg_servers": []string{exPK2},
		}),
		exampleJSON([]map[string]interface{}{{
			"pk": exPK1, "online": true, "version": "v1.3.29",
			"ip": "192.168.1.1", "country": "US", "city": "New York",
		}}),
		exampleJSON(store.UptimeResponse{
			{Key: exPK1, Online: true, Version: "v1.3.29"},
			{Key: exPK2, Online: false},
		}),
		exampleJSON(store.UptimeResponseV2{{
			Key: exPK1, Online: true, Version: "v1.3.29",
			DailyOnlineHistory: map[string]string{"2024-01-15": "95.5", "2024-01-14": "100.0"},
		}}),
		exampleJSON(store.UptimeDef{Key: exPK1, Online: true, Version: "v1.3.29"}),
		exampleJSON(map[string]string{exPK1: "192.168.1.1", exPK2: "10.0.0.1"}),
		exampleJSON(map[string]interface{}{"nonce": 12345}),
	)
}

// RootCmd contains the root cli commanmd
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Uptime Tracker Server for skywire",
	Long: calvin.AsciiFont("uptime-tracker") + `
Uptime Tracker Server - tracks visor online status and uptime statistics.

Depends: redis, postgres

Production: ` + deployment.Prod.UptimeTracker + `
            ` + dmsg.Prod.UptimeTracker + `
Test:       ` + deployment.Test.UptimeTracker + `
            ` + dmsg.Test.UptimeTracker + `

HTTP Endpoints:
  GET  /health                        Health check
  GET  /v4/update                     Visor heartbeat (auth)
  GET  /visors                        All registered visors
  GET  /uptimes                       Visor uptime data (?v=v2 for v2 format)
  GET  /uptime/{pk}                   Uptime for specific visor
  GET  /dashboard                     Dashboard chart data
  GET  /visor-ips                     Visor IP addresses (private API)
  GET  /security/nonces/{pk}          Get nonce for signing
` + generateExamples(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		if !strings.HasPrefix(redisURL, redisScheme) {
			redisURL = redisScheme + redisURL
		}

		const loggerTag = "uptime_tracker"
		logger := logging.MustGetLogger(loggerTag)

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

		var gormDB *gorm.DB

		pk, err := sk.PubKey()
		if err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		nonceStoreConfig := storeconfig.Config{
			Type:     storeconfig.Memory,
			URL:      redisURL,
			Password: storeconfig.RedisPassword(),
		}

		if !testing {
			pgUser, pgPassword, pgDatabase := storeconfig.PostgresCredential()
			dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
				pgHost,
				pgPort,
				pgUser,
				pgPassword,
				pgDatabase)

			gormDB, err = pg.Init(dsn, pgMaxOpenConn)
			if err != nil {
				logger.Fatalf("Failed to connect to database %v", err)
			}
			logger.Printf("Database connected.")

			nonceStoreConfig.Type = storeconfig.Redis
		}

		s, err := store.New(logger, gormDB, testing)
		if err != nil {
			logger.Fatalf("Failed to create store instance: %v", err)
		}
		defer s.Close()

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		nonceStore, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, redisPrefix)
		if err != nil {
			logger.Fatal("Failed to initialize redis nonce store: ", err)
		}

		locDetails := geo.MakeIPDetails(logging.MustGetLogger("uptime.geo"), geoipURL)

		metricsutil.ServeHTTPMetrics(logger, metricsAddr)

		var m utmetrics.Metrics
		if metricsAddr == "" {
			m = utmetrics.NewEmpty()
		} else {
			m = utmetrics.NewVictoriaMetrics()
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		enableMetrics := metricsAddr != ""
		utAPI := api.New(logger, s, nonceStore, locDetails, enableLoadTesting, enableMetrics, m, storeDataCutoff, storeDataPath, dmsgAddr)

		utPAPI := api.NewPrivate(logger, s)

		logger.Infof("Listening on %s", addr)

		go utAPI.RunBackgroundTasks(ctx, logger)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, utAPI); err != nil {
				logger.Errorf("tcpproxy.ListenAndServe utAPI: %v", err)
				cancel()
			}
		}()

		go func() {
			if err := tcpproxy.ListenAndServe(pAddr, utPAPI); err != nil {
				logger.Errorf("tcpproxy.ListenAndServe utPAPI: %v", err)
				cancel()
			}
		}()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, "", logger)

			var keys cipher.PubKeys
			keys = append(keys, pk)
			dClient := direct.NewClient(direct.GetAllEntries(keys, servers), logger)
			config := &dmsg.Config{
				MinSessions:    0, // listen on all available servers
				UpdateInterval: dmsg.DefaultUpdateInterval,
			}

			dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, logger, pk, sk, dClient, config)
			if err != nil {
				logger.WithError(err).Fatal("failed to start direct dmsg client.")
			}

			defer closeDmsgDC()

			go func() {
				for {
					utAPI.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, "", logger)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, utAPI, dClient, dmsgPort, dmsgDC, logger); err != nil {
					logger.Errorf("dmsghttp.ListenAndServe utAPI: %v", err)
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
		fmt.Println(err)

		os.Exit(statusFailure)
	}
}
