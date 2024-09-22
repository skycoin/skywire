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
	"path/filepath"
	"strings"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/internal/discmetrics"
	"github.com/skycoin/dmsg/internal/dmsg-discovery/api"
	"github.com/skycoin/dmsg/internal/dmsg-discovery/store"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
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
)

func init() {
	sf.Init(RootCmd, "dmsg_disc", "")

	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9090", "address to bind to")
	RootCmd.Flags().StringVar(&authPassphrase, "auth", "", "auth passphrase as simple auth for official dmsg servers registration")
	RootCmd.Flags().StringVar(&officialServers, "official-servers", "", "list of official dmsg servers keys separated by comma")
	RootCmd.Flags().StringVar(&redisURL, "redis", store.DefaultURL, "connections string for a redis store")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", store.DefaultTimeout, "discovery entry timeout")
	RootCmd.Flags().BoolVarP(&testMode, "test-mode", "t", false, "in testing mode")
	RootCmd.Flags().BoolVar(&enableLoadTesting, "enable-load-testing", false, "enable load testing")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
}

// RootCmd contains commands for dmsg-discovery
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG Discovery Server",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐  ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	 │││││└─┐│ ┬───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	─┴┘┴ ┴└─┘└─┘  ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
DMSG Discovery Server
----- depends: redis -----
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

		metricsutil.ServeHTTPMetrics(log, sf.MetricsAddr)

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		db := prepareDB(ctx, log)

		var m discmetrics.Metrics
		if sf.MetricsAddr == "" {
			m = discmetrics.NewEmpty()
		} else {
			m = discmetrics.NewVictoriaMetrics()
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
		} else {
			if testEnvironment {
				whitelistPKs = strings.Split(skyenv.TestNetworkMonitorPKs, ",")
			} else {
				whitelistPKs = strings.Split(skyenv.NetworkMonitorPKs, ",")
			}
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
			if err = listenAndServe(addr, a); err != nil {
				log.Errorf("ListenAndServe: %v", err)
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
				if err = dmsghttp.ListenAndServe(ctx, sk, a, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, log); err != nil {
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
			getServers(ctx, a, dmsgServerType, log)
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
