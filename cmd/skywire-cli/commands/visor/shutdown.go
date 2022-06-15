package visor

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
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
		err := rpcClient().Shutdown()
		if err.Error() != "unexpected EOF" {
			internal.Catch(err)
		}
		fmt.Print("visor was shut down")
	},
}
