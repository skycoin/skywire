// Package commands cmd/liveness-checker/commands/root.go
package commands

import (
	"context"
	"log"
	"log/syslog"
	"os"
	"strings"

	cc "github.com/ivanpirog/coloredcobra"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/liveness-checker/api"
	"github.com/skycoin/skywire/pkg/liveness-checker/store"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/tcpproxy"
)

const (
	redisScheme = "redis://"
)

var (
	confPath   string
	addr       string
	tag        string
	syslogAddr string
	redisURL   string
	testing    bool
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9081", "address to bind to.\033[0m")
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "liveness-checker.json", "config file location.\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "liveness_checker", "logging tag\033[0m")
	RootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "syslog server address. E.g. localhost:514\033[0m")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\033[0m")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis\033[0m")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var RootCmd = &cobra.Command{
	Use:   "lc",
	Short: "Liveness checker of the deployment.",
	Long: `
	┬  ┬┬  ┬┌─┐┌┐┌┌─┐┌─┐┌─┐   ┌─┐┬ ┬┌─┐┌─┐┬┌─┌─┐┬─┐
	│  │└┐┌┘├┤ │││├┤ └─┐└─┐───│  ├─┤├┤ │  ├┴┐├┤ ├┬┘
	┴─┘┴ └┘ └─┘┘└┘└─┘└─┘└─┘   └─┘┴ ┴└─┘└─┘┴ ┴└─┘┴└─`,
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
		}

		if testing {
			storeConfig.Type = storeconfig.Memory
		}

		mLogger := logging.NewMasterLogger()
		conf, confAPI := api.InitConfig(confPath, mLogger)

		logger := mLogger.PackageLogger(tag)
		if syslogAddr != "" {
			hook, err := logrussyslog.NewSyslogHook("udp", syslogAddr, syslog.LOG_INFO, tag)
			if err != nil {
				logger.Fatalf("Unable to connect to syslog daemon on %v", syslogAddr)
			}
			logging.AddHook(hook)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		s, err := store.New(ctx, storeConfig, logger)
		if err != nil {
			logger.Fatal("Failed to initialize redis store: ", err)
		}

		logger.WithField("addr", addr).Info("Serving discovery API...")

		lcAPI := api.New(conf.PK, conf.SK, s, logger, mLogger, confAPI)

		go lcAPI.RunBackgroundTasks(ctx, conf)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, lcAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := lcAPI.Visor.Close(); err != nil {
			logger.WithError(err).Error("Visor closed with error.")
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       RootCmd,
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
