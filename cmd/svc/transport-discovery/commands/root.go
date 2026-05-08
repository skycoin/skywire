// Package commands cmd/transport-discovery/root.go
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

	"github.com/google/uuid"
	"github.com/skycoin/skywire/deployment"
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
	"github.com/skycoin/skywire/pkg/serviceuptime"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/svcmode"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/api"
	"github.com/skycoin/skywire/pkg/transport-discovery/cxoaggregator"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/transport-discovery/metrics"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

const (
	redisPrefix = "transport-discovery"
	redisScheme = "redis://"
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
	enableCXO       bool
	mode            string
)

func init() {
	RootCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to JSON config file. When set, fields below come from the config file. Generate one with `skywire cli config gen --tpd -o /etc/skywire/transport-discovery.json`.\n\r")
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9091", "address to bind to\n\r")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\n\r")
	// 5 min is ~3.3× the 90s client refresh interval
	// (transportReRegisterInterval in pkg/transport/manager.go),
	// giving safe margin for one or two dropped refreshes without
	// expiring a live transport. Prior default of 2m allowed only
	// ~1.33 refreshes per TTL window — one missed refresh and the
	// transport would briefly drop from discovery.
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 5*time.Minute, "transport entry TTL (0 to disable)\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\n\r")
	RootCmd.Flags().StringVar(&tag, "tag", "transport_discovery", "logging tag\n\r")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsgDisc, "url of dmsg-discovery\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().StringVar(&keyFile, "keyfile", "", "path to file containing secret key (auto-generated if missing)\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&storeDataPath, "store-data-path", "/var/lib/skywire/tpd/bandwidth", "path for bandwidth backup files\n\r")
	RootCmd.Flags().BoolVar(&enableCXO, "cxo", false, "enable CXO feed for transport data distribution over DMSG")
	RootCmd.Flags().StringVar(&mode, "mode", "", "listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)")
	RootCmd.Flags().StringVar(&uptimeDB, "uptime-db", "/var/lib/skywire/tpd/uptime.db", "path for the service-self uptime bbolt store (empty disables)")
}

// uptimeDB is the path for the local self-uptime store. Open early
// in Run so a panic during subsystem init still leaves a session row.
var uptimeDB string

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

		// Service-self uptime recorder. Opened before subsystem init so
		// a panic in the redis or DMSG bring-up still leaves a session
		// row with the running binary's version. Failure is non-fatal
		// — TPD continues without the /uptime/* surface.
		var uptimeRec *serviceuptime.Recorder
		if uptimeDB != "" {
			rec, rErr := serviceuptime.New(uptimeDB, serviceuptime.Config{
				Service: "transport-discovery",
				Version: buildinfo.Version(),
				Commit:  buildinfo.Commit(),
			})
			if rErr != nil {
				logger.WithError(rErr).Warn("Service-self uptime recorder unavailable")
			} else {
				uptimeRec = rec
				defer func() { _ = uptimeRec.Close() }() //nolint:errcheck
				uptimeRec.Start()
			}
		}

		metricsutil.ServePProf(logger, pprofAddr, "transport-discovery")

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
		if uptimeRec != nil {
			tpdAPI.SetUptimeRecorder(uptimeRec)
		}

		logger.Infof("Listening on %s", addr)
		logger.Infof("Transport entry timeout: %v", entryTimeout)

		go tpdAPI.RunBackgroundTasks(ctx, logger)

		resolvedMode, err := svcmode.ResolveMode(mode, !sk.Null())
		if err != nil {
			logger.WithError(err).Fatal("invalid --mode")
		}

		// Source priority for embedded dmsg-server transit and the
		// survey whitelist:
		//   1. config (--config)
		//   2. embedded deployment keyring (deployment.Prod / dmsg.Prod)
		// Operators ship a config file generated from the keyring so
		// IP rotations don't require a binary rebuild.
		embeddedServers := dmsgDiscEntries(configServers)
		surveyWL := deployment.Prod.SurveyWhitelist
		if len(configSurveyWL) > 0 {
			surveyWL = configSurveyWL
		}

		h, err := svcmode.Start(ctx, svcmode.Config{
			Mode:                resolvedMode,
			HTTPAddr:            addr,
			Handler:             tpdAPI,
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
				tpdAPI.DmsgServers = s
			},
		})
		if err != nil {
			logger.WithError(err).Fatal("failed to start listeners")
		}
		defer h.Close()

		// CXO aggregator: visors dial in (using TPD's PK from
		// Transport.DiscoveryDmsg), and the aggregator subscribes
		// to each conn's remote PK as a feed. Reverse-dial means
		// visor-restart / DMSG reconnect is handled by the visor's
		// re-dial — TPD just accepts and re-subscribes on the next
		// reconcile tick. No visor enumeration needed on TPD's side.
		if enableCXO && h.DmsgClient != nil {
			// Sink wraps the redis store (telemetry methods) plus the
			// API (register/deregister methods, which need mirrorEdges
			// in addition to a store write).
			sink := &tpdAggregatorSink{Store: s, api: tpdAPI}
			agg, err := cxoaggregator.New(h.DmsgClient, sink, cxoaggregator.Config{
				Logger: logging.MustGetLogger("tpd-cxo-aggregator"),
			})
			if err != nil {
				logger.WithError(err).Error("Failed to start CXO aggregator, continuing without it")
			} else {
				agg.Run(ctx)
				defer agg.Close() //nolint:errcheck
				logger.WithField("feed_pk", agg.FeedPK()).Info("CXO aggregator running: accepting inbound visor stats feeds")
			}

			// CXO metrics publisher: outbound feed mirroring the
			// /metrics aggregate. Visors subscribe to TPD's PK on
			// skyenv.DmsgTPDMetricsCXOPort and read the JSON-encoded
			// []TransportMetric from "metrics/days/<n>" instead of
			// HTTP-polling the same query.
			if pub, perr := api.StartMetricsCXOPublisher(ctx, tpdAPI, h.DmsgClient, sk, logger); perr != nil {
				logger.WithError(perr).Error("Failed to start CXO metrics publisher, continuing without it")
			} else {
				defer pub.Close() //nolint:errcheck
			}

			// CXO uptime publisher: outbound feed mirroring the
			// /uptimes?v=v3 response. Visors subscribe to TPD's PK
			// on skyenv.DmsgTPDUptimeCXOPort and read the
			// JSON-encoded []VisorSummary from "uptimes/days/<n>".
			// Drives the hvui Network Uptime tab without per-visor
			// fan-out polling.
			if pub, perr := api.StartUptimeCXOPublisher(ctx, tpdAPI, h.DmsgClient, sk, logger); perr != nil {
				logger.WithError(perr).Error("Failed to start CXO uptime publisher, continuing without it")
			} else {
				defer pub.Close() //nolint:errcheck
			}
		} else if enableCXO {
			logger.Warn("CXO requested but dmsg is not enabled (--mode=http); aggregator/publisher disabled")
		}

		// Wire DHT entry mirroring: every transport registration is
		// also published to the DHT under each edge visor's PK.
		if h.DHTNode != nil {
			mirror := dht.NewEntryMirror(h.DHTNode, "tp", logging.MustGetLogger("dht:tp-mirror"))
			tpdAPI.SetDHTMirror(mirror)
			logger.Info("DHT transport mirroring enabled (via local DHT node)")
		} else if redisURL != "" {
			// No local DHT node — write directly to Redis so DMSG servers'
			// DHT nodes can serve the data to the Kademlia network.
			redisHost := strings.TrimPrefix(redisURL, redisScheme)
			redisPassword := os.Getenv("REDIS_PASSWORD")
			redisMirror, mErr := dht.NewRedisMirror(redisHost, redisPassword, 0, "tp", pk, sk, logging.MustGetLogger("dht:tp-redis-mirror"))
			if mErr != nil {
				logger.WithError(mErr).Warn("DHT Redis mirror failed — transport data won't be in DHT")
			} else {
				tpdAPI.SetDHTMirror(redisMirror)
				logger.Info("DHT transport mirroring enabled (via Redis)")
				go tpdAPI.BackfillDHTMirror(ctx, logger)
			}
		}

		select {
		case <-ctx.Done():
		case err := <-h.Errors():
			logger.WithError(err).Error("listener failed")
			cancel()
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// tpdAggregatorSink composes the cxoaggregator.Sink contract from the
// two collaborators that own the relevant pieces:
//
//   - store.Store satisfies the telemetry half (UpdateBandwidth,
//     UpdateLatency, RecordTransportHeartbeat, IngestTransportTimeline)
//     directly via the embedded interface.
//   - The API satisfies the metadata half (RegisterTransportFromCXO,
//     DeregisterTransportFromCXO) because those need mirrorEdges in
//     addition to a redis write, and mirrorEdges lives on the API.
//
// Defined here so the cxoaggregator package doesn't gain a dependency
// on the API package; the wiring stays at the deployment layer.
type tpdAggregatorSink struct {
	store.Store
	api *api.API
}

func (s *tpdAggregatorSink) RegisterTransportFromCXO(ctx context.Context, entry *transport.Entry, reporter cipher.PubKey, version string) error {
	return s.api.RegisterTransportFromCXO(ctx, entry, reporter, version)
}

func (s *tpdAggregatorSink) DeregisterTransportFromCXO(ctx context.Context, id uuid.UUID, reporter cipher.PubKey) error {
	return s.api.DeregisterTransportFromCXO(ctx, id, reporter)
}
