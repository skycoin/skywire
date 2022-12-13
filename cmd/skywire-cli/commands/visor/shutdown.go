// Package clivisor cmd/skywire-cli/commands/visor/shutdown.go
package clivisor

import (
	"fmt"
	"os"

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
	Long:  "\n  Stop a running visor",
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		rpcClient.Shutdown() //nolint
		fmt.Println("Visor was shut down")
		internal.PrintOutput(cmd.Flags(), "Visor was shut down", fmt.Sprintln("Visor was shut down"))
	},
}
