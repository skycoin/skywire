// Package mgmt cmd/skywire-cli/commands/mgmt/connect.go
package mgmt

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	remotePK string
)

func init() {
	RootCmd.PersistentFlags().StringVarP(&remotePK, "pk", "k", "", "remote public key to connect to")

}
func init() {
	RootCmd.AddCommand(connCmd)
	RootCmd.PersistentFlags().StringVarP(&remotePK, "pk", "k", "", "remote public key to connect to")
}

// connCmd contains commands to connect to Manager
var connCmd = &cobra.Command{
	Use:   "connect <remote-pk>",
	Short: "Skywire visor manager",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		_, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		var remotePK cipher.PubKey

		internal.Catch(cmd.Flags(), remotePK.Set(args[0]))

		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")

	},
}
