// Package commands cmd/transport-discovery/root.go
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

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cxo/publisher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/tcpproxy"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/api"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/transport-discovery/metrics"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

const (
	redisPrefix = "transport-discovery"
	redisScheme = "redis://"
)

var (
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
	dmsgPort        uint16
	dmsgServerType  string
	entryTimeout    time.Duration
	dmsgDisc        = deployment.Prod.DmsgDiscovery
	pprofAddr       string
	storeDataPath   string
	enableCXO       bool
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9091", "address to bind to\n\r")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\n\r")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 2*time.Minute, "timeout for transport entry expiration\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\n\r")
	RootCmd.Flags().StringVar(&tag, "tag", "transport_discovery", "logging tag\n\r")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsgDisc, "url of dmsg-discovery\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&storeDataPath, "store-data-path", "/var/lib/skywire/tpd/bandwidth", "path for bandwidth backup files\n\r")
	RootCmd.Flags().BoolVar(&enableCXO, "cxo", false, "enable CXO feed for transport data distribution over DMSG")
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

		var whitelistPKs []string
		if whitelistKeys != "" {
			whitelistPKs = strings.Split(whitelistKeys, ",")
		}
		for _, v := range whitelistPKs {
			api.WhitelistPKs.Set(v)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		s, err := store.New(ctx, storeConfig, entryTimeout, logger)
		if err != nil {
			logger.Fatalf("Failed to create store instance: %v", err)
		}
		defer s.Close()

		nonceStoreConfig := storeconfig.Config{
			Type:     storeconfig.Memory,
			URL:      redisURL,
			Password: storeconfig.RedisPassword(),
			PoolSize: redisPoolSize,
		}

		if !testing {
			nonceStoreConfig.Type = storeconfig.Redis
		}

		nonceStore, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, redisPrefix)
		if err != nil {
			log.Fatal("Failed to initialize redis nonce store: ", err)
		}

		pk, err := sk.PubKey()
		if err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		metricsutil.ServeHTTPMetrics(logger, metricsAddr)

		var m tpdiscmetrics.Metrics
		if metricsAddr == "" {
			m = tpdiscmetrics.NewEmpty()
		} else {
			m = tpdiscmetrics.NewVictoriaMetrics()
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		enableMetrics := metricsAddr != ""
		tpdAPI := api.New(logger, s, nonceStore, enableMetrics, m, dmsgAddr, storeDataPath)

		logger.Infof("Listening on %s", addr)
		logger.Infof("Transport entry timeout: %v", entryTimeout)

		go tpdAPI.RunBackgroundTasks(ctx, logger)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, tpdAPI); err != nil {
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
					tpdAPI.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, dmsgServerType, logger)

			// Initialize CXO publisher for transport data distribution
			if enableCXO {
				cxoConf := publisher.DefaultConfig()
				cxoConf.Logger = logging.MustGetLogger("cxo-tpd")
				cxoPub, err := publisher.New(dmsgDC, sk, cxoConf)
				if err != nil {
					logger.WithError(err).Error("Failed to start CXO publisher, continuing without it")
				} else {
					tpdAPI.SetCXOPublisher(cxoPub)
					logger.Infof("CXO transport feed enabled: %s", cxoPub.Feed())
					defer cxoPub.Close() //nolint:errcheck,gosec
				}
			}

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, tpdAPI, dClient, dmsgPort, dmsgDC, logger); err != nil {
					logger.Errorf("dmsghttp.ListenAndServe: %v", err)
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
		log.Fatal("Failed to execute command: ", err)
	}
}
