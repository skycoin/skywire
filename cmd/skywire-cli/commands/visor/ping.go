// Package clivisor cmd/skywire-cli/commands/visor/ping.go
package clivisor

import (
	"fmt"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

func init() {
	RootCmd.AddCommand(testCmd)
}

var testCmd = &cobra.Command{
	Use:   "ping <pk>",
	Short: "Return routing rule by route ID key",
	Long:  "\n	Return routing rule by route ID key",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pk := internal.ParsePK(cmd.Flags(), "pk", args[0])

		latency, err := clirpc.Client(cmd.Flags()).TestRouting(pk)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), latency, fmt.Sprintf(latency+"\n"))

	},
}
