// Package commands cmd/network-monitor/commands/root.go
package commands

import (
	"context"
	"log"
	"log/syslog"
	"os"
	"strings"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/internal/nmmetrics"
	"github.com/skycoin/skywire/pkg/network-monitor/api"
	"github.com/skycoin/skywire/pkg/network-monitor/store"
)

const (
	redisScheme = "redis://"
)

var (
	sleepDeregistration time.Duration
	confPath            string
	sdURL               string
	arURL               string
	utURL               string
	addr                string
	tag                 string
	syslogAddr          string
	metricsAddr         string
	redisURL            string
	testing             bool
	redisPoolSize       int
	batchSize           int
)

func init() {
	rootCmd.Flags().StringVarP(&addr, "addr", "a", ":9080", "address to bind to.\033[0m")
	rootCmd.Flags().DurationVar(&sleepDeregistration, "sleep-deregistration", 10, "Sleep time for derigstration process in minutes\033[0m")
	rootCmd.Flags().IntVarP(&batchSize, "batchsize", "b", 30, "Batch size of deregistration\033[0m")
	rootCmd.Flags().StringVarP(&confPath, "config", "c", "network-monitor.json", "config file location.\033[0m")
	rootCmd.Flags().StringVarP(&sdURL, "sd-url", "n", "", "url to service discovery.\033[0m")
	rootCmd.Flags().StringVarP(&arURL, "ar-url", "v", "", "url to address resolver.\033[0m")
	rootCmd.Flags().StringVarP(&utURL, "ut-url", "u", "", "url to uptime tracker visor data.\033[0m")
	rootCmd.Flags().StringVar(&tag, "tag", "network_monitor", "logging tag\033[0m")
	rootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "syslog server address. E.g. localhost:514\033[0m")
	rootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to\033[0m")
	rootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\033[0m")
	rootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis\033[0m")
	rootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\033[0m")
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var rootCmd = &cobra.Command{
	Use:   "network-monitor",
	Short: "Network monitor for skywire VPN and Visor.",
	Long: `
	┌┐┌┌─┐┌┬┐┬ ┬┌─┐┬─┐┬┌─   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	│││├┤  │ ││││ │├┬┘├┴┐───││││ │││││ │ │ │├┬┘
	┘└┘└─┘ ┴ └┴┘└─┘┴└─┴ ┴   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─`,
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

		s, err := store.New(storeConfig)
		if err != nil {
			log.Fatal("Failed to initialize redis store: ", err)
		}

		mLogger := logging.NewMasterLogger()
		conf := api.InitConfig(confPath, mLogger)

		if sdURL == "" {
			sdURL = conf.Launcher.ServiceDisc
		}
		if arURL == "" {
			arURL = conf.Transport.AddressResolver
		}
		if utURL == "" {
			utURL = conf.UptimeTracker.Addr + "/uptimes"
		}

		var srvURLs api.ServicesURLs
		srvURLs.SD = sdURL
		srvURLs.AR = arURL
		srvURLs.UT = utURL

		logger := mLogger.PackageLogger("network_monitor")
		if syslogAddr != "" {
			hook, err := logrussyslog.NewSyslogHook("udp", syslogAddr, syslog.LOG_INFO, tag)
			if err != nil {
				logger.Fatalf("Unable to connect to syslog daemon on %v", syslogAddr)
			}
			logging.AddHook(hook)
		}

		logger.WithField("addr", addr).Info("Serving discovery API...")

		metricsutil.ServeHTTPMetrics(logger, metricsAddr)

		var m nmmetrics.Metrics
		if metricsAddr == "" {
			m = nmmetrics.NewEmpty()
		} else {
			m = nmmetrics.NewVictoriaMetrics()
		}
		enableMetrics := metricsAddr != ""

		nmSign, _ := cipher.SignPayload([]byte(conf.PK.Hex()), conf.SK) //nolint

		var nmConfig api.NetworkMonitorConfig
		nmConfig.PK = conf.PK
		nmConfig.SK = conf.SK
		nmConfig.Sign = nmSign
		nmConfig.BatchSize = batchSize

		nmAPI := api.New(s, logger, srvURLs, enableMetrics, m, nmConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go nmAPI.InitDeregistrationLoop(ctx, conf, sleepDeregistration)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, nmAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := nmAPI.Visor.Close(); err != nil {
			logger.WithError(err).Error("Visor closed with error.")
		}
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
		log.Fatal("Failed to execute command: ", err)
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
