// Package mgmt cmd/skywire-cli/commands/mgmt/conn.go
package mgmt

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

func init() {
	RootCmd.AddCommand(connCmd)
	RootCmd.AddCommand(disCmd)
	RootCmd.AddCommand(lsCmd)
}

// connCmd contains commands to connect to Manager Server
var connCmd = &cobra.Command{
	Use:   "connect <remote-pk>",
	Short: "Connect to a remote Manager Server of a skywire visor",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		var remotePK cipher.PubKey

		internal.Catch(cmd.Flags(), remotePK.Set(args[0]))

		err = rpcClient.ConnectMgmt(remotePK)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")

	},
}

// disCmd contains commands to disconnect from the Manager Server
var disCmd = &cobra.Command{
	Use:   "disconnect <remote-pk>",
	Short: "Disconnect from a remote Manager Server of a skywire visor",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		var remotePK cipher.PubKey

		internal.Catch(cmd.Flags(), remotePK.Set(args[0]))

		err = rpcClient.DisconnectMgmt(remotePK)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")

	},
}

// lsCmd contains commands to list the ongoing conns to Manager Servers
var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all ongoing connections to Manager Server of skywire visors",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		pks, err := rpcClient.ListMgmt()
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), pks, pks)

	},
}
