package clivisor

import (
	"fmt"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

func init() {
	RootCmd.AddCommand(shutdownCmd)
}

var shutdownCmd = &cobra.Command{
	Use:   "halt",
	Short: "Stop a running visor",
	Run: func(cmd *cobra.Command, args []string) {
		clirpc.Client().Shutdown() //nolint
		fmt.Println("Visor was shut down")
		internal.PrintOutput(cmd.Flags(), "Visor was shut down", fmt.Sprintln("Visor was shut down"))
	},
}
