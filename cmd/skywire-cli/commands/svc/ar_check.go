// Package clisvc ar_check.go — check if a key exists in the address resolver
package clisvc

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
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
		if len(result) == 0 {
			fmt.Printf("%s: not registered in address resolver\n", args[0])
		} else {
			fmt.Printf("%s: registered for %v\n", args[0], result)
		}
	},
}
