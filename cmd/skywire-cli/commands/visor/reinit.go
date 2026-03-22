// Package clivisor cmd/skywire-cli/commands/visor/reinit.go
package clivisor

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var module string

func init() {
	RootCmd.AddCommand(reinitCmd)
	reinitCmd.Flags().StringVarP(&module, "module", "m", "", "target module for reinitiating.")
}

var reinitCmd = &cobra.Command{
	Use:   "reinit",
	Short: "Reinitiate modules",
	Long:  "\n  Reinitiate modules",
	Run: func(cmd *cobra.Command, _ []string) {
		if module == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("No module choosed for reinitiate"))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		err = rpcClient.ReinitiateModule(module)
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Reinitiating of %s request sent. Check visor logs.\n", module))
	},
}
