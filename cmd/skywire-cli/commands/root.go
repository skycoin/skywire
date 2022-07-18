package commands

import (
	"log"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	clicompletion "github.com/skycoin/skywire/cmd/skywire-cli/commands/completion"
	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
	clidmsgpty "github.com/skycoin/skywire/cmd/skywire-cli/commands/dmsgpty"
	clihv "github.com/skycoin/skywire/cmd/skywire-cli/commands/hv"
	clivpn "github.com/skycoin/skywire/cmd/skywire-cli/commands/vpn"
	climdisc "github.com/skycoin/skywire/cmd/skywire-cli/commands/mdisc"
	clirtfind "github.com/skycoin/skywire/cmd/skywire-cli/commands/rtfind"
	clivisor "github.com/skycoin/skywire/cmd/skywire-cli/commands/visor"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var rootCmd = &cobra.Command{
	Use:   "skywire-cli",
	Short: "Command Line Interface for skywire",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┬  ┬
	└─┐├┴┐└┬┘││││├┬┘├┤───│  │  │
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘┴─┘┴`,
	SilenceErrors:      true,
	SilenceUsage:       true,
	DisableSuggestions: true,
}

func init() {
	rootCmd.AddCommand(
		cliconfig.RootCmd,
		clidmsgpty.RootCmd,
		clivisor.RootCmd,
		clihv.RootCmd,
		clivpn.RootCmd,
		clirtfind.RootCmd,
		climdisc.RootCmd,
		clicompletion.RootCmd,
	)
	var helpflag bool
	rootCmd.PersistentFlags().StringVarP(&clirpc.RpcAddr, "rpc", "", "localhost:3435", "RPC server address")
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
