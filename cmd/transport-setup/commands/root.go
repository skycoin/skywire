// Package commands cmd/transport-setup/commands/root.go
package commands

import (
	"fmt"
	"log"
	"net/http"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport-setup/api"
	"github.com/skycoin/skywire/pkg/transport-setup/config"
)

var configFile string

func init() {
	RootCmd.Flags().StringVarP(&configFile, "config", "c", "", "path to config file\033[0m")
	err := RootCmd.MarkFlagRequired("config")
	if err != nil {
		log.Fatal("config flag is not specified")
	}
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var RootCmd = &cobra.Command{
	Use:   "tps [config.json]",
	Short: "Transport setup node for skywire",
	Long: `
	┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐  ┌─┐┌─┐┌┬┐┬ ┬┌─┐
	 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │───└─┐├┤  │ │ │├─┘
	 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴   └─┘└─┘ ┴ └─┘┴  `,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		// local config of the client
		const loggerTag = "transport_setup"
		log := logging.MustGetLogger(loggerTag)
		conf := config.MustReadConfig(configFile, log)
		api := api.New(log, conf)
		srv := &http.Server{
			Addr:              fmt.Sprintf(":%d", conf.Port),
			ReadHeaderTimeout: 2 * time.Second,
			IdleTimeout:       30 * time.Second,
			Handler:           api,
		}
		if err := srv.ListenAndServe(); err != nil {
			log.Errorf("ListenAndServe: %v", err)
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
