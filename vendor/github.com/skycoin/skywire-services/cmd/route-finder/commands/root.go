// Package commands cmd/route-finder/commands/root.go
package commands

import (
	"context"
	"fmt"
	"log"
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
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"github.com/skycoin/skywire-services/internal/pg"
	"github.com/skycoin/skywire-services/pkg/route-finder/api"
	"github.com/skycoin/skywire-services/pkg/transport-discovery/store"
)

var (
	addr        string
	metricsAddr string
	pgHost      string
	pgPort      string
	logLvl      string
	tag         string
	testing     bool
	dmsgDisc    string
	sk          cipher.SecKey
	dmsgPort    uint16
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9092", "address to bind to\033[0m")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to\033[0m")
	RootCmd.Flags().StringVar(&pgHost, "pg-host", "localhost", "host of postgres\033[0m")
	RootCmd.Flags().StringVar(&pgPort, "pg-port", "5432", "port of postgres\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "set log level one of: info, error, warn, debug, trace, panic")
	RootCmd.Flags().StringVar(&tag, "tag", "route_finder", "logging tag\033[0m")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis\033[0m")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", "http://dmsgd.skywire.skycoin.com", "url of dmsg-discovery\033[0m")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\r")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Route Finder Server for skywire",
	Long: `
	┬─┐┌─┐┬ ┬┌┬┐┌─┐  ┌─┐┬┌┐┌┌┬┐┌─┐┬─┐
	├┬┘│ ││ │ │ ├┤───├┤ ││││ ││├┤ ├┬┘
	┴└─└─┘└─┘ ┴ └─┘  └  ┴┘└┘─┴┘└─┘┴└─
----- depends: postgres and initial db setup -----
sudo -iu postgres createdb rf
skywire cli config gen-keys | tee rf-config.json
PG_USER="postgres" PG_DATABASE="rf" PG_PASSWORD="" route-finder  --addr ":9092" --sk $(tail -n1 rf-config.json)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		memoryStore := true

		logger := logging.MustGetLogger(tag)
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			logger.Fatal("Invalid loglvl detected")
		}

		logging.SetLevel(lvl)

		var gormDB *gorm.DB

		if !testing {
			pgUser, pgPassword, pgDatabase := storeconfig.PostgresCredential()
			dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
				pgHost,
				pgPort,
				pgUser,
				pgPassword,
				pgDatabase)

			gormDB, err = pg.Init(dsn)
			if err != nil {
				logger.Fatalf("Failed to connect to database %v", err)
			}
			logger.Printf("Database connected.")
			memoryStore = false
		}

		transportStore, err := store.New(logger, gormDB, memoryStore)
		if err != nil {
			log.Fatal("Failed to initialize redis store: ", err)
		}

		pk, err := sk.PubKey()
		if err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		metricsutil.ServeHTTPMetrics(logger, metricsAddr)

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		enableMetrics := metricsAddr != ""
		rfAPI := api.New(transportStore, logger, enableMetrics, dmsgAddr)

		if logger != nil {
			logger.Infof("Listening on %s", addr)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go func() {
			if err := tcpproxy.ListenAndServe(addr, rfAPI); err != nil {
				logger.Errorf("tcpproxy.ListenAndServe: %v", err)
				cancel()
			}
		}()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, logger)

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
					rfAPI.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, logger)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, rfAPI, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, logger); err != nil {
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
