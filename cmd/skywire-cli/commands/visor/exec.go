package clivisor

import (
	"strings"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
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
	Run: func(cmd *cobra.Command, args []string) {
		out, err := clirpc.Client().Exec(strings.Join(args, " "))
		internal.Catch(cmd.Flags(), err)
		// since the output of this command can be anything it is not formatted, so it's advisable to not use the `--json` flag for this one
		internal.PrintOutput(cmd.Flags(), string(out), string(out))
	},
}
