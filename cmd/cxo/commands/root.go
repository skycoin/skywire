package commands

import (
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
)

// RootCmd is the root cobra command for cxo
var RootCmd = &cobra.Command{
	Use:   "cxo",
	Short: "CXO object distribution system",
	Long:  calvin.AsciiFont("cxo") + "\nCXO is a P2P content-addressable object distribution system.",
}

func init() {
	RootCmd.AddCommand(
		daemonCmd,
		cliCmd,
	)
}

// Execute runs the root command
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
