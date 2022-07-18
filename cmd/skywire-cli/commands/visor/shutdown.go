package clivisor

import (
	"fmt"

	"github.com/spf13/cobra"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"

)

func init() {
	RootCmd.AddCommand(shutdownCmd)
}

var shutdownCmd = &cobra.Command{
	Use:   "halt",
	Short: "Stop a running visor",
	Run: func(_ *cobra.Command, args []string) {
		clirpc.RpcClient().Shutdown() //nolint
		fmt.Println("Visor was shut down")
	},
}
