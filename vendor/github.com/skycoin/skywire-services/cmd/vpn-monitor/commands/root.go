// Package commands cmd/vpn-monitor/commands/root.go
package commands

import (
	"context"
	"log"
	"os"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-services/pkg/vpn-monitor/api"
)

var (
	confPath            string
	addr                string
	tag                 string
	sleepDeregistration time.Duration
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9081", "address to bind to.\033[0m")
	RootCmd.Flags().DurationVarP(&sleepDeregistration, "sleep-deregistration", "s", 10, "Sleep time for derigstration process in minutes\033[0m")
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "vpn-monitor.json", "config file location.\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "vpn_monitor", "logging tag\033[0m")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for vpn-monitor")
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use:   "vpnmon",
	Short: "VPN monitor.",
	Long: `
	┬  ┬┌─┐┌┐┌   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	└┐┌┘├─┘│││───││││ │││││ │ │ │├┬┘
	 └┘ ┴  ┘└┘   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─`,
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
		conf := api.InitConfig(confPath, mLogger)

		srvURLs := api.ServicesURLs{
			SD: conf.Launcher.ServiceDisc,
			UT: conf.UptimeTracker.Addr,
		}

		logger := mLogger.PackageLogger("vpn_monitor")

		logger.WithField("addr", addr).Info("Serving discovery API...")

		vmSign, _ := cipher.SignPayload([]byte(conf.PK.Hex()), conf.SK) //nolint

		vmConfig := api.Config{
			PK:   conf.PK,
			SK:   conf.SK,
			Sign: vmSign,
		}

		vmAPI := api.New(logger, srvURLs, vmConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go vmAPI.InitDeregistrationLoop(ctx, conf, sleepDeregistration)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, vmAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := vmAPI.Visor.Close(); err != nil {
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
