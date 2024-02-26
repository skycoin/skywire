// Package commands cmd/dmsg-monitor/commands/root.go
package commands

import (
	"context"
	"log"
	"log/syslog"
	"os"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-services/pkg/dmsg-monitor/api"
)

var (
	confPath            string
	dmsgURL             string
	utURL               string
	addr                string
	tag                 string
	syslogAddr          string
	logLvl              string
	sleepDeregistration time.Duration
	batchSize           int
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9080", "address to bind to.\033[0m")
	RootCmd.Flags().DurationVarP(&sleepDeregistration, "sleep-deregistration", "s", 10, "Sleep time for derigstration process in minutes\033[0m")
	RootCmd.Flags().IntVarP(&batchSize, "batchsize", "b", 20, "Batch size of deregistration\033[0m")
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "dmsg-monitor.json", "config file location.\033[0m")
	RootCmd.Flags().StringVarP(&dmsgURL, "dmsg-url", "d", "", "url to dmsg data.\033[0m")
	RootCmd.Flags().StringVarP(&utURL, "ut-url", "u", "", "url to uptime tracker visor data.\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "dmsg_monitor", "logging tag\033[0m")
	RootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "syslog server address. E.g. localhost:514\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "set log level one of: info, error, warn, debug, trace, panic")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for dmsg-monitor")
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use:   "mon",
	Short: "DMSG monitor of DMSG discoery.",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	 │││││└─┐│ ┬───││││ │││││ │ │ │├┬┘
	─┴┘┴ ┴└─┘└─┘   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		mLogger := logging.NewMasterLogger()
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			mLogger.Fatal("Invalid log level")
		}
		logging.SetLevel(lvl)

		conf := api.InitConfig(confPath, mLogger)

		if dmsgURL == "" {
			dmsgURL = conf.Dmsg.Discovery
		}
		if utURL == "" {
			utURL = conf.UptimeTracker.Addr + "/uptimes"
		}

		var srvURLs api.ServicesURLs
		srvURLs.DMSG = dmsgURL
		srvURLs.UT = utURL

		logger := mLogger.PackageLogger(tag)
		if syslogAddr != "" {
			hook, err := logrussyslog.NewSyslogHook("udp", syslogAddr, syslog.LOG_INFO, tag)
			if err != nil {
				logger.Fatalf("Unable to connect to syslog daemon on %v", syslogAddr)
			}
			logging.AddHook(hook)
		}

		logger.WithField("addr", addr).Info("Serving DMSG-Monitor API...")

		monitorSign, _ := cipher.SignPayload([]byte(conf.PK.Hex()), conf.SK) //nolint

		var monitorConfig api.DMSGMonitorConfig
		monitorConfig.PK = conf.PK
		monitorConfig.Sign = monitorSign
		monitorConfig.BatchSize = batchSize

		dmsgMonitorAPI := api.New(logger, srvURLs, monitorConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go dmsgMonitorAPI.InitDeregistrationLoop(ctx, conf, sleepDeregistration)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, dmsgMonitorAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := dmsgMonitorAPI.Visor.Close(); err != nil {
			logger.WithError(err).Error("Visor closed with error.")
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:         RootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := RootCmd.Execute(); err != nil {
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
