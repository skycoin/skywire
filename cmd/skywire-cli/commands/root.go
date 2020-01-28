package commands

import (
	"log"

	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/cmd/skywire-cli/commands/mdisc"
	"github.com/SkycoinProject/skywire-mainnet/cmd/skywire-cli/commands/rtfind"
	"github.com/SkycoinProject/skywire-mainnet/cmd/skywire-cli/commands/visor"
)

var rootCmd = &cobra.Command{
	Use:   "skywire-cli",
	Short: "Command Line Interface for skywire",
}

func init() {
	rootCmd.AddCommand(
		visor.RootCmd,
		mdisc.RootCmd,
		rtfind.RootCmd,
	)
}

// Execute executes root CLI command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
