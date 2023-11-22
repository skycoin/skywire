// Package commands cmd/service-discovery/root.go
package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/skycoin-service-discovery/internal/pg"
	"github.com/skycoin/skycoin-service-discovery/internal/sdmetrics"
	"github.com/skycoin/skycoin-service-discovery/pkg/service-discovery/api"
	"github.com/skycoin/skycoin-service-discovery/pkg/service-discovery/store"
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
)

func init() {
	rootCmd.Flags().StringVarP(&addr, "addr", "a", ":9098", "address to bind to")
	rootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	rootCmd.Flags().StringVarP(&redisURL, "redis", "r", "redis://localhost:6379", "connections string for a redis store")
	rootCmd.Flags().StringVarP(&pgHost, "pg-host", "o", "localhost", "host of postgres")
	rootCmd.Flags().StringVarP(&pgPort, "pg-port", "p", "5432", "port of postgres")
	rootCmd.Flags().StringVarP(&whitelistKeys, "whitelist-keys", "w", "", "list of whitelisted keys of network monitor used for deregistration")
	rootCmd.Flags().BoolVarP(&testMode, "test", "t", false, "run in test mode and disable auth")
	rootCmd.Flags().StringVarP(&apiKey, "api-key", "g", "", "geo API key")
	rootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "d", "", "url of dmsg-discovery default:\n"+skyenv.DmsgDiscAddr)
	rootCmd.Flags().BoolVarP(&testEnvironment, "test-environment", "n", false, "distinguished between prod and test environment")
	rootCmd.Flags().VarP(&sk, "sk", "s", "dmsg secret key\n")
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var rootCmd = &cobra.Command{
	Use:   "service-discovery",
	Short: "Service discovery server",
	Long: `
	┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐ ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	└─┐├┤ ├┬┘└┐┌┘││  ├┤───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	└─┘└─┘┴└─ └┘ ┴└─┘└─┘ ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴ `,
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

		// we enable metrics middleware if address is passed
		enableMetrics := metricsAddr != ""
		sdAPI := api.New(log, db, nonceDB, apiKey, enableMetrics, m)

		var whitelistPKs []string
		if whitelistKeys != "" {
			whitelistPKs = strings.Split(whitelistKeys, ",")
		} else {
			if testEnvironment {
				whitelistPKs = strings.Split(skyenv.TestNetworkMonitorPK, ",")
			} else {
				whitelistPKs = strings.Split(skyenv.NetworkMonitorPK, ",")
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

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, log)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, pk, sk, sdAPI, dClient, dmsg.DefaultDmsgHTTPPort, config, dmsgDC, log); err != nil {
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
	cc.Init(&cc.Config{
		RootCmd:       rootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

const help = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}\r\n\r\n" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
