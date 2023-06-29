// Package commands cmd/config-bootstrapper/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/config-bootstrapper/api"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/tcpproxy"
)

var (
	addr     string
	tag      string
	stunPath string
	domain   string
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9082", "address to bind to\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "address_resolver", "logging tag\033[0m")
	RootCmd.Flags().StringVarP(&stunPath, "config", "c", "./config.json", "stun server list file location\033[0m")
	RootCmd.Flags().StringVarP(&domain, "domain", "d", "skywire.skycoin.com", "the domain of the endpoints\033[0m")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var RootCmd = &cobra.Command{
	Use:   "cb",
	Short: "Config Bootstrap Server for skywire",
	Long: `
	┌─┐┌─┐┌┐┌┌─┐┬┌─┐   ┌┐ ┌─┐┌─┐┌┬┐┌─┐┌┬┐┬─┐┌─┐┌─┐┌─┐┌─┐┬─┐
	│  │ ││││├┤ ││ ┬───├┴┐│ ││ │ │ └─┐ │ ├┬┘├─┤├─┘├─┘├┤ ├┬┘
	└─┘└─┘┘└┘└  ┴└─┘   └─┘└─┘└─┘ ┴ └─┘ ┴ ┴└─┴ ┴┴  ┴  └─┘┴└─`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		logger := logging.MustGetLogger(tag)
		config := readConfig(logger, stunPath)

		conAPI := api.New(logger, config, domain)
		if logger != nil {
			logger.Infof("Listening on %s", addr)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go func() {
			if err := tcpproxy.ListenAndServe(addr, conAPI); err != nil {
				logger.Errorf("conAPI.ListenAndServe: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()

		conAPI.Close()
	},
}

func readConfig(log *logging.Logger, confPath string) (config api.Config) {
	var r io.Reader

	f, err := os.Open(confPath) //nolint:gosec
	if err != nil {
		log.WithError(err).
			WithField("filepath", confPath).
			Fatal("Failed to read config file.")
	}
	defer func() { //nolint
		if err := f.Close(); err != nil {
			log.WithError(err).Fatal("Closing config file resulted in error.")
		}
	}()

	r = f

	raw, err := io.ReadAll(r)
	if err != nil {
		log.WithError(err).Fatal("Failed to read in config.")
	}
	conf := api.Config{}

	if err := json.Unmarshal(raw, &conf); err != nil {
		log.WithError(err).Fatal("failed to convert config into json.")
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
