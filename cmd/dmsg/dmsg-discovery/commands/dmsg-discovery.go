// Package commands cmd/dmsg-discovery/commands/root.go
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dht"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/api"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/svcmode"
)

const redisPasswordEnvName = "REDIS_PASSWORD"

var (
	sf                cmdutil.ServiceFlags
	configPath        string
	addr              string
	redisURL          string
	whitelistKeys     string
	entryTimeout      time.Duration
	testMode          bool
	enableLoadTesting bool
	testEnvironment   bool
	pk                cipher.PubKey
	sk                cipher.SecKey
	keyFile           string
	dmsgPort          uint16
	authPassphrase    string
	officialServers   string
	dmsgServerType    string
	pprofMode         string
	pprofAddr         string
	mode              string
	enableCXO         bool
)

func init() {
	sf.Init(RootCmd, "dmsg_disc", "")

	RootCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to JSON config file. When set, every other CLI flag below is ignored — fields come from the config file. Generate one with `skywire cli config gen --dmsgdisc -o /etc/skywire/dmsg-discovery.json`.\n\r")
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9090", "address to bind to\n\r")
	RootCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	RootCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port")
	RootCmd.Flags().StringVar(&authPassphrase, "auth", "", "auth passphrase as simple auth for official dmsg servers registration")
	RootCmd.Flags().StringVar(&officialServers, "official-servers", "", "list of official dmsg servers keys separated by comma")
	RootCmd.Flags().StringVar(&redisURL, "redis", store.DefaultURL, "connections string for a redis store\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	// 60m is 12× the client refresh interval (DefaultUpdateInterval*5 = 5m),
	// giving ~2-3 missed refreshes of slack before Redis prunes an entry.
	// Set to 0 to disable expiration (legacy behavior, stale entries
	// will accumulate forever).
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 60*time.Minute, "client discovery entry TTL (0 to disable)\n\r")
	RootCmd.Flags().BoolVarP(&testMode, "test-mode", "t", false, "in testing mode")
	RootCmd.Flags().BoolVar(&enableLoadTesting, "enable-load-testing", false, "enable load testing")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().StringVar(&keyFile, "keyfile", "", "path to file containing secret key (auto-generated if missing)\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	// dmsg-discovery cannot run in dmsg-only mode because dmsg-servers
	// themselves register with it over plain HTTP (see
	// cmd/dmsg-commands/dmsg-server/commands/start/root.go). http and
	// dual are accepted; dmsg is rejected.
	RootCmd.Flags().StringVar(&mode, "mode", "", "listener mode: http|dual (dmsg-only is rejected — dmsg-servers reach this service over HTTP)")
	RootCmd.Flags().BoolVar(&enableCXO, "cxo", false, "enable CXO clients-by-server publisher feed over DMSG (requires --sk and --mode=dual)")
}

// RootCmd contains commands for dmsg-discovery
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG Discovery Server",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐  ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	 │││││└─┐│ ┬───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	─┴┘┴ ┴└─┘└─┘  ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
DMSG Discovery Server - registers and discovers DMSG clients and servers.

Depends: redis

HTTP Endpoints:
  GET  /health                                Health check
  GET  /dmsg-discovery/entry/{pk}             Get entry by public key
  POST /dmsg-discovery/entry/                 Register/update entry
  POST /dmsg-discovery/entry/{pk}             Register/update entry
  DEL  /dmsg-discovery/entry                  Delete entry
  GET  /dmsg-discovery/entries                All entries
  GET  /dmsg-discovery/visorEntries           All visor entries
  DEL  /dmsg-discovery/deregister             Deregister entry
  GET  /dmsg-discovery/available_servers      Available DMSG servers
  GET  /dmsg-discovery/all_servers            All DMSG servers
  GET  /dmsg-discovery/servers/clients        Clients by all servers
  GET  /dmsg-discovery/server/{pk}/clients    Clients by specific server
` + generateExamples() + `

Example:
  skywire cli config gen-keys > dmsgd-config.json
  skywire dmsg disc --sk $(tail -n1 dmsgd-config.json)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		log := sf.Logger()

		// configServers, when non-nil, overrides the embedded keyring
		// preload below. Populated only when --config is set.
		var configServers []*disc.Entry
		if configPath != "" {
			c, cErr := LoadConfig(configPath)
			if cErr != nil {
				log.WithError(cErr).Fatal("failed to load config")
			}
			applyConfig(c)
			configServers = c.DmsgServers
			log.WithField("path", configPath).Info("loaded config")
		}

		var err error
		if keyFile != "" {
			if err = dmsgcmdutil.LoadOrGenerateKey(keyFile, &sk); err != nil {
				log.Fatal("Failed to load keyfile: ", err)
			}
		}
		if pk, err = sk.PubKey(); err != nil {
			log.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		stopPProf := dmsgcmdutil.InitPProf(log, pprofMode, pprofAddr)
		defer stopPProf()

		metricsutil.ServeHTTPMetrics(log, sf.MetricsAddr)

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		db := prepareDB(ctx, log)

		var m metrics.Metrics
		if sf.MetricsAddr == "" {
			m = metrics.NewEmpty()
		} else {
			m = metrics.NewVictoriaMetrics()
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		// we enable metrics middleware if address is passed
		enableMetrics := sf.MetricsAddr != ""
		a := api.New(log, db, m, testMode, enableLoadTesting, enableMetrics, dmsgAddr, authPassphrase, entryTimeout)

		var whitelistPKs []string
		if whitelistKeys != "" {
			whitelistPKs = strings.Split(whitelistKeys, ",")
		}

		for _, v := range whitelistPKs {
			api.WhitelistPKs.Set(v)
		}

		a.OfficialServers, err = fetchOfficialDmsgServers(officialServers)
		if err != nil {
			log.Info(err)
		}

		// Resolve --mode. dmsg-discovery intentionally does NOT
		// accept ModeDmsg because dmsg-servers themselves register
		// with it over plain HTTP (see dmsg-server's disc.NewHTTP
		// call). Allow ModeHTTP and ModeDual; reject ModeDmsg.
		resolvedMode, err := svcmode.ResolveMode(mode, !sk.Null())
		if err != nil {
			log.WithError(err).Fatal("invalid --mode")
		}
		if resolvedMode == svcmode.ModeDmsg {
			log.Fatal("dmsg-discovery cannot run in --mode=dmsg: dmsg-servers must reach it over plain HTTP to register. Use --mode=http or --mode=dual.")
		}

		go a.RunBackgroundTasks(ctx, log)
		log.WithField("addr", addr).Info("Serving discovery API...")
		go func() {
			if listenErr := listenAndServe(addr, a); listenErr != nil {
				log.Errorf("ListenAndServe: %v", listenErr)
				cancel()
			}
		}()
		if !pk.Null() && resolvedMode.IncludesDmsg() {
			// Source priority for the dmsg-server transit set:
			//   1. config.dmsg_servers from --config (config-file mode)
			//   2. embedded deployment keyring (deployment.Prod, or
			//      deployment.Test with --test-environment)
			//   3. legacy API self-poll (the original bootstrap loop;
			//      kept as last-resort safety net for binaries built
			//      with an empty embedded keyring)
			var servers []*disc.Entry
			switch {
			case len(configServers) > 0:
				servers = configServers
				log.WithField("count", len(servers)).WithField("source", "config").Info("preloading dmsg-servers from config")
			default:
				src := deployment.Prod
				srcName := "deployment.Prod"
				if testEnvironment {
					src = deployment.Test
					srcName = "deployment.Test"
				}
				servers = src.ToDiscEntries()
				if len(servers) > 0 {
					log.WithField("count", len(servers)).WithField("source", srcName).Info("preloading dmsg-servers from embedded deployment keyring")
				} else {
					log.Warn("no dmsg-servers in embedded deployment keyring; falling back to API self-poll (legacy behavior; will block until at least one server registers over HTTP)")
					servers = getServers(ctx, a, dmsgServerType, log)
				}
			}
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
					a.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go updateServers(ctx, a, dClient, dmsgDC, dmsgServerType, log)

			go func() {
				if dmsgErr := dmsghttp.ListenAndServe(ctx, sk, a, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, log); dmsgErr != nil {
					log.Errorf("dmsghttp.ListenAndServe: %v", dmsgErr)
					cancel()
				}
			}()

			// Serve pprof debug interface over dmsg
			wl := deployment.Prod.SurveyWhitelist
			if testEnvironment {
				wl = deployment.Test.SurveyWhitelist
			}
			go func() {
				if debugErr := dmsghttp.ServeDebug(ctx, dmsgDC, log, wl); debugErr != nil {
					log.Errorf("dmsghttp.ServeDebug: %v", debugErr)
				}
			}()

			// CXO clients-by-server publisher: outbound feed mirroring
			// the AllClientsByServer / ClientsByServer view. Subscribers
			// (the hypervisor's network visualizer) connect to DMSG-D's
			// PK on skyenv.DmsgDMSGDClientsByServerCXOPort and read
			// JSON-encoded client entries grouped by server PK from
			// "clients-by-server/<server>/<client>/{entry,tombstone}"
			// instead of HTTP-polling /dmsg-discovery/servers/clients.
			if enableCXO {
				if pub, perr := api.StartClientsByServerCXOPublisher(dmsgDC, sk, log); perr != nil {
					log.WithError(perr).Error("Failed to start CXO clients-by-server publisher, continuing without it")
				} else {
					a.SetClientsByServerCXOPublisher(pub)
					defer pub.Close() //nolint:errcheck
				}
			}

			// Mirror DMSG entries to Redis in DHT format. The DMSG servers'
			// DHT nodes read from the same Redis and serve the data via
			// Kademlia. This replaces the previous EntryMirror which ran
			// a local DHT node and did expensive Kademlia PutSigned on
			// every SetEntry — at ~30 req/sec that consumed 60%+ CPU on
			// secp256k1 noise handshakes for each DHT dial.
			redisHost := strings.TrimPrefix(redisURL, "redis://")
			redisPassword := os.Getenv(redisPasswordEnvName)
			redisMirror, mirrorErr := dht.NewRedisMirror(redisHost, redisPassword, 0, "dmsg", pk, sk, logging.MustGetLogger("dht:redis-mirror"))
			if mirrorErr != nil {
				log.WithError(mirrorErr).Warn("DHT Redis mirror failed — entries won't be in DHT")
			} else {
				a.SetDHTMirror(redisMirror)
				log.Info("DHT entry mirroring enabled (via Redis)")
				// Backfill: mirror all existing entries so the DHT has the
				// full dataset, not just entries updated since this restart.
				go a.BackfillDHTMirror(ctx, log)
			}
		}

		<-ctx.Done()
	},
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}

func prepareDB(ctx context.Context, log *logging.Logger) store.Storer {
	dbConf := &store.Config{
		URL:      redisURL,
		Password: os.Getenv(redisPasswordEnvName),
		Timeout:  entryTimeout,
	}

	db, err := store.NewStore(ctx, "redis", dbConf, log)
	if err != nil {
		log.Fatal("Failed to initialize redis store: ", err)
	}

	return db
}

func getServers(ctx context.Context, a *api.API, dmsgServerType string, log logrus.FieldLogger) (servers []*disc.Entry) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		servers, err := a.AllServers(ctx, log)
		if err != nil {
			log.WithError(err).Fatal("Error getting dmsg-servers.")
		}
		// filtered dmsg servers by their type
		if dmsgServerType != "" {
			var filteredServers []*disc.Entry
			for _, server := range servers {
				if server.Server.ServerType == dmsgServerType {
					filteredServers = append(filteredServers, server)
				}
			}
			servers = filteredServers
		}
		if len(servers) > 0 {
			return servers
		}
		log.Warn("No dmsg-servers found, trying again in 1 minute.")
		select {
		case <-ctx.Done():
			return []*disc.Entry{}
		case <-ticker.C:
			return getServers(ctx, a, dmsgServerType, log)
		}
	}
}

func updateServers(ctx context.Context, a *api.API, dClient disc.APIClient, dmsgC *dmsg.Client, dmsgServerType string, log logrus.FieldLogger) {
	ticker := time.NewTicker(time.Minute * 10)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			servers, err := a.AllServers(ctx, log)
			if err != nil {
				log.WithError(err).Error("Error getting dmsg-servers.")
				break
			}
			// filtered dmsg servers by their type
			if dmsgServerType != "" {
				var filteredServers []*disc.Entry
				for _, server := range servers {
					if server.Server.ServerType == dmsgServerType {
						filteredServers = append(filteredServers, server)
					}
				}
				servers = filteredServers
			}
			for _, server := range servers {
				dClient.PostEntry(ctx, server) //nolint
				err := dmsgC.EnsureSession(ctx, server)
				if err != nil {
					log.WithField("remote_pk", server.Static).WithError(err).Warn("Failed to establish session.")
				}
			}
		}
	}
}

func listenAndServe(addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second, ReadHeaderTimeout: 3 * time.Second}
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	proxyListener := &proxyproto.Listener{Listener: ln}
	defer proxyListener.Close() // nolint:errcheck
	return srv.Serve(proxyListener)
}

func fetchOfficialDmsgServers(officialServers string) (map[string]bool, error) {
	dmsgServers := make(map[string]bool)
	if officialServers != "" {
		dmsgServersList := strings.Split(officialServers, ",")
		for _, v := range dmsgServersList {
			dmsgServers[v] = true
		}
		return dmsgServers, nil
	}
	return dmsgServers, errors.New("no official dmsg server list passed by --official-server flag")
}
