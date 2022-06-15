package visor

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(
		shutdownCmd,
	)
}

var shutdownCmd = &cobra.Command{
	Use:   "halt",
	Short: "stop a running visor",
	Run: func(_ *cobra.Command, args []string) {
		rpcClient().Shutdown() //nolint
		fmt.Println("Visor was shut down")
	},
}
