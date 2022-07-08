package clivisor

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

func init() {
	RootCmd.AddCommand(
		execCmd,
	)
}

var execCmd = &cobra.Command{
	Use:   "exec <command>",
	Short: "Execute a command",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		out, err := rpcClient().Exec(strings.Join(args, " "))
		internal.Catch(err)
		fmt.Print(string(out))
	},
}
