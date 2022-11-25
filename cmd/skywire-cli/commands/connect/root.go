// Package connect root.go
package connect

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	remotePort int
	localPort  int
)

func init() {
	connectCmd.PersistentFlags().IntVarP(&remotePort, "remoteport", "r", 0, "remote port on visor to read from")
	connectCmd.PersistentFlags().IntVarP(&localPort, "localport", "l", 0, "local port for server to run on")
	RootCmd.AddCommand(connectCmd)
	RootCmd.AddCommand(disconnectCmd)
}

// RootCmd contains commands that interact with the skyproxy
var RootCmd = &cobra.Command{
	Use:   "skyproxy",
	Short: "Query the Skywire Visor",
}

var connectCmd = &cobra.Command{
	Use:   "connect <pubkey>",
	Short: "Skywire connect",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		var remotePK cipher.PubKey
		internal.Catch(cmd.Flags(), remotePK.Set(args[0]))

		if remotePort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag -remoteport not specified"))
		}

		if localPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag -localPort not specified"))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		id, err := rpcClient.Connect(remotePK, remotePort, localPort)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), id, fmt.Sprintln(id))
	},
}

var disconnectCmd = &cobra.Command{
	Use:   "disconnect <id>",
	Short: "Skywire connect",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id, err := uuid.Parse(args[0])
		internal.Catch(cmd.Flags(), err)

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		err = rpcClient.Disconnect(id)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}
