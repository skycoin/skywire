// Package clitp cmd/skywire-cli/commands/tp/tp-rm.go
package clitp

import (
	"os"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	removeAll bool
)

func init() {
	rmTpCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all transports")
	rmTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "remove transport of given ID")
	rmTpCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

var rmTpCmd = &cobra.Command{
	Use:                   "rm",
	Short:                 "Remove transport(s) by id",
	Long:                  "\n    Remove transport(s) by id",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if removeAll {
			internal.Catch(cmd.Flags(), rpcClient.RemoveAllTransports())
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
		} else if tpID != "" {
			tID := internal.ParseUUID(cmd.Flags(), "transport-id", tpID)
			if err != nil {
				os.Exit(1)
			}
			internal.Catch(cmd.Flags(), rpcClient.RemoveTransport(tID))
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
		} else {
			internal.PrintOutput(cmd.Flags(), "", cmd.Help())
		}
	},
}
