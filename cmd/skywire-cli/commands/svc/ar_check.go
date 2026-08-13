// Package clisvc cmd/skywire-cli/commands/svc/ar_check.go c4-vis-cli
package clisvc

import (
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/clisvc"
)

func init() {
	arCmd.AddCommand(arCheckCmd)
}

var arCheckCmd = &cobra.Command{
	Use:   "check <pk>",
	Short: "Check if a public key is registered in the address resolver",
	Long: `Check whether a visor's public key has an entry in the address
resolver, without revealing its IP address. Returns the transport
types the visor is registered for (stcpr, sudph) or "not found".`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		result, err := rpcClient.CheckAREntry(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, clisvc.ARRegistration{
			PK: args[0], Registered: len(result) > 0, Types: result,
		}))
	},
}
