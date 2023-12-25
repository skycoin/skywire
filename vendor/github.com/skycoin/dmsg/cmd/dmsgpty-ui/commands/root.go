// Package commands cmd/dmsgpty-ui/commands/root.go
package commands

import (
	"log"
	"net/http"
	"os"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/dmsgpty"
)

var (
	hostNet  = dmsgpty.DefaultCLINet
	hostAddr = dmsgpty.DefaultCLIAddr()
	addr     = ":8080"
	conf     = dmsgpty.DefaultUIConfig()
)

func init() {
	RootCmd.PersistentFlags().StringVar(&hostNet, "hnet", hostNet, "dmsgpty host network name")
	RootCmd.PersistentFlags().StringVar(&hostAddr, "haddr", hostAddr, "dmsgpty host network address")
	RootCmd.PersistentFlags().StringVar(&addr, "addr", addr, "network address to serve UI on")
	RootCmd.PersistentFlags().StringVar(&conf.CmdName, "cmd", conf.CmdName, "command to run when initiating pty")
	RootCmd.PersistentFlags().StringArrayVar(&conf.CmdArgs, "arg", conf.CmdArgs, "command arguments to include when initiating pty")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for dmsgpty-ui")
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

// RootCmd contains commands to start a dmsgpty-ui server for a dmsgpty-host
var RootCmd = &cobra.Command{
	Use:   "ui",
	Short: "hosts a UI server for a dmsgpty-host",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┬ ┬┬
	 │││││└─┐│ ┬├─┘ │ └┬┘───│ ││
	─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    └─┘┴`,
	Run: func(cmd *cobra.Command, args []string) {
		if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		ui := dmsgpty.NewUI(dmsgpty.NetUIDialer(hostNet, hostAddr), conf)
		logrus.
			WithField("addr", addr).
			Info("Serving.")

		srv := &http.Server{
			ReadTimeout:       3 * time.Second,
			WriteTimeout:      3 * time.Second,
			IdleTimeout:       30 * time.Second,
			ReadHeaderTimeout: 3 * time.Second,
			Addr:              addr,
			Handler:           ui.Handler(nil),
		}

		err := srv.ListenAndServe()
		logrus.
			WithError(err).
			Info("Stopped serving.")
	},
}

// Execute executes the root command.
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
		os.Exit(1)
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
