// Package commands cmd/skywire-cli/commands/root.go
package commands

import (
	"log"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	clicompletion "github.com/skycoin/skywire/cmd/skywire-cli/commands/completion"
	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
	clidmsgpty "github.com/skycoin/skywire/cmd/skywire-cli/commands/dmsgpty"
	climdisc "github.com/skycoin/skywire/cmd/skywire-cli/commands/mdisc"
	clireward "github.com/skycoin/skywire/cmd/skywire-cli/commands/reward"
	clirtfind "github.com/skycoin/skywire/cmd/skywire-cli/commands/rtfind"
	clisurvey "github.com/skycoin/skywire/cmd/skywire-cli/commands/survey"
	clivisor "github.com/skycoin/skywire/cmd/skywire-cli/commands/visor"
	clivpn "github.com/skycoin/skywire/cmd/skywire-cli/commands/vpn"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var rootCmd = &cobra.Command{
	Use:   "skywire-cli",
	Short: "Command Line Interface for skywire",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┬  ┬
	└─┐├┴┐└┬┘││││├┬┘├┤───│  │  │
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘┴─┘┴`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

func init() {
	rootCmd.AddCommand(
		cliconfig.RootCmd,
		clidmsgpty.RootCmd,
		clivisor.RootCmd,
		clivpn.RootCmd,
		clireward.RootCmd,
		clisurvey.RootCmd,
		clirtfind.RootCmd,
		climdisc.RootCmd,
		clicompletion.RootCmd,
	)
	var jsonOutput bool
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, internal.JSONString, false, "print output in json")
	rootCmd.PersistentFlags().MarkHidden(internal.JSONString) //nolint
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
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
