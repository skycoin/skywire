// Package commands cmd/service-discovery/root.go
package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"github.com/skycoin/skycoin-service-discovery/internal/pg"
	"github.com/skycoin/skycoin-service-discovery/internal/sdmetrics"
	"github.com/skycoin/skycoin-service-discovery/pkg/service-discovery/api"
	"github.com/skycoin/skycoin-service-discovery/pkg/service-discovery/store"
)

var log = logging.MustGetLogger("service-discovery")

const redisPrefix = "service-discovery"

var (
	addr            string
	metricsAddr     string
	redisURL        string
	pgHost          string
	pgPort          string
	testMode        bool
	apiKey          string
	dmsgDisc        string
	whitelistKeys   string
	testEnvironment bool
	sk              cipher.SecKey
	dmsgPort        uint16
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9098", "address to bind to")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVarP(&redisURL, "redis", "r", "redis://localhost:6379", "connections string for a redis store")
	RootCmd.Flags().StringVarP(&pgHost, "pg-host", "o", "localhost", "host of postgres")
	RootCmd.Flags().StringVarP(&pgPort, "pg-port", "p", "5432", "port of postgres")
	RootCmd.Flags().StringVarP(&whitelistKeys, "whitelist-keys", "w", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().BoolVarP(&testMode, "test", "t", false, "run in test mode and disable auth")
	RootCmd.Flags().StringVarP(&apiKey, "api-key", "g", "", "geo API key")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "d", skyenv.DmsgDiscAddr, "url of dmsg-discovery")
	RootCmd.Flags().BoolVarP(&testEnvironment, "test-environment", "n", false, "distinguished between prod and test environment")
	RootCmd.Flags().VarP(&sk, "sk", "s", "dmsg secret key\n")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value")
}

// RootCmd contains the root service-discovery command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Service discovery server",
	Long: `
	┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐ ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	└─┐├┤ ├┬┘└┐┌┘││  ├┤───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	└─┘└─┘┴└─ └┘ ┴└─┘└─┘ ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
----- depends: redis, postgresql and initial DB setup -----
sudo -iu postgres createdb sd
keys-gen | tee sd-config.json
PG_USER="postgres" PG_DATABASE="sd" PG_PASSWORD="" service-discovery --sk $(tail -n1 sd-config.json)`,
	Run: func(_ *cobra.Command, _ []string) {
		if dmsgDisc == "" {
			dmsgDisc = skyenv.DmsgDiscAddr
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

		var gormDB *gorm.DB

		pgUser, pgPassword, pgDatabase := storeconfig.PostgresCredential()
		dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			pgHost,
			pgPort,
			pgUser,
			pgPassword,
			pgDatabase)

		gormDB, err = pg.Init(dsn)
		if err != nil {
			log.Fatalf("Failed to connect to database %v", err)
		}
		log.Printf("Database connected.")

		db, err := store.NewStore(gormDB, log)
		if err != nil {
			log.Fatal("Failed to initialize redis store: ", err)
		}

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
		sdAPI := api.New(log, db, nonceDB, apiKey, enableMetrics, m, dmsgAddr)

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

		go sdAPI.RunBackgroundTasks(ctx, log)

		log.WithField("addr", addr).Info("Serving discovery API...")
		go func() {
			if err := tcpproxy.ListenAndServe(addr, sdAPI); err != nil {
				log.Errorf("ListenAndServe: %v", err)
				cancel()
			}
		}()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, log)
			config := &dmsg.Config{
				MinSessions:    0, // listen on all available servers
				UpdateInterval: dmsg.DefaultUpdateInterval,
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

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, log)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, sdAPI, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, log); err != nil {
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
