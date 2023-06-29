// Package commands cmd/public-visor-monitor/commands/root.go
package commands

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/public-visor-monitor/api"
	"github.com/skycoin/skywire/pkg/tcpproxy"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	confPath            string
	addr                string
	tag                 string
	sleepDeregistration time.Duration
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9082", "address to bind to.\033[0m")
	RootCmd.Flags().DurationVarP(&sleepDeregistration, "sleep-deregistration", "s", 10, "Sleep time for derigstration process in minutes\033[0m")
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "public-visor-monitor.json", "config file location.\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "public_visor_monitor", "logging tag\033[0m")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var RootCmd = &cobra.Command{
	Use:   "pvm",
	Short: "Public Visor monitor.",
	Long: `
	┌─┐┬ ┬┌┐ ┬  ┬┌─┐ ┬  ┬┬┌─┐┌─┐┬─┐   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	├─┘│ │├┴┐│  ││───└┐┌┘│└─┐│ │├┬┘───││││ │││││ │ │ │├┬┘
	┴  └─┘└─┘┴─┘┴└─┘  └┘ ┴└─┘└─┘┴└─   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		visorBuildInfo := buildinfo.Get()
		if _, err := visorBuildInfo.WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		mLogger := logging.NewMasterLogger()
		conf := initConfig(confPath, visorBuildInfo, mLogger)

		srvURLs := api.ServicesURLs{
			SD: conf.Launcher.ServiceDisc,
			UT: conf.UptimeTracker.Addr,
		}

		logger := mLogger.PackageLogger("public_visor_monitor")

		logger.WithField("addr", addr).Info("Serving discovery API...")

		pvmSign, _ := cipher.SignPayload([]byte(conf.PK.Hex()), conf.SK) //nolint

		pvmConfig := api.Config{
			PK:   conf.PK,
			SK:   conf.SK,
			Sign: pvmSign,
		}

		pvmAPI := api.New(logger, srvURLs, pvmConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go pvmAPI.InitDeregistrationLoop(ctx, conf, sleepDeregistration)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, pvmAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := pvmAPI.Visor.Close(); err != nil {
			logger.WithError(err).Error("Visor closed with error.")
		}
	},
}

func initConfig(confPath string, visorBuildInfo *buildinfo.Info, mLog *logging.MasterLogger) *visorconfig.V1 {
	log := mLog.PackageLogger("public_visor_monitor:config")
	var r io.Reader

	if confPath != "" {
		log.WithField("filepath", confPath).Info()
		f, err := os.ReadFile(filepath.Clean(confPath))
		if err != nil {
			log.WithError(err).Fatal("Failed to read config file.")
		}
		r = bytes.NewReader(f)
	}

	conf, compat, err := visorconfig.Parse(log, r, confPath, visorBuildInfo)
	if err != nil {
		log.WithError(err).Fatal("Failed to read in config.")
	}
	if !compat {
		log.Fatalf("failed to start skywire - config version is incompatible")
	}

	return conf
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
