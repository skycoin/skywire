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
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/spf13/cobra"

	dmsgcmdutil "github.com/skycoin/dmsg/pkg/cmdutil"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/disc/metrics"
	"github.com/skycoin/dmsg/pkg/discovery/api"
	"github.com/skycoin/dmsg/pkg/discovery/store"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgclient"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
)

const redisPasswordEnvName = "REDIS_PASSWORD"

var (
	sf                cmdutil.ServiceFlags
	addr              string
	redisURL          string
	whitelistKeys     string
	entryTimeout      time.Duration
	testMode          bool
	enableLoadTesting bool
	testEnvironment   bool
	pk                cipher.PubKey
	sk                cipher.SecKey
	dmsgPort          uint16
	authPassphrase    string
	officialServers   string
	dmsgServerType    string
	pprofMode         string
	pprofAddr         string
)

func init() {
	sf.Init(RootCmd, "dmsg_disc", "")

	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9090", "address to bind to\n\r")
	RootCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	RootCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port")
	RootCmd.Flags().StringVar(&authPassphrase, "auth", "", "auth passphrase as simple auth for official dmsg servers registration")
	RootCmd.Flags().StringVar(&officialServers, "official-servers", "", "list of official dmsg servers keys separated by comma")
	RootCmd.Flags().StringVar(&redisURL, "redis", store.DefaultURL, "connections string for a redis store\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", store.DefaultTimeout, "discovery entry timeout\n\r")
	RootCmd.Flags().BoolVarP(&testMode, "test-mode", "t", false, "in testing mode")
	RootCmd.Flags().BoolVar(&enableLoadTesting, "enable-load-testing", false, "enable load testing")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
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

		var err error
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
		a := api.New(log, db, m, testMode, enableLoadTesting, enableMetrics, dmsgAddr, authPassphrase)

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

		go a.RunBackgroundTasks(ctx, log)
		log.WithField("addr", addr).Info("Serving discovery API...")
		go func() {
			if listenErr := listenAndServe(addr, a); listenErr != nil {
				log.Errorf("ListenAndServe: %v", listenErr)
				cancel()
			}
		}()
		if !pk.Null() {
			servers := getServers(ctx, a, dmsgServerType, log)
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
