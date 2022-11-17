// Package connect root.go
package connect

import (
	"fmt"
	"os"

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
	RootCmd.PersistentFlags().IntVarP(&remotePort, "remoteport", "r", 0, "remote port on visor to read from")
	RootCmd.PersistentFlags().IntVarP(&localPort, "localport", "l", 0, "local port for server to run on")
}

// RootCmd is connectCmd
var RootCmd = connectCmd

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

		err = rpcClient.Connect(remotePK, remotePort, localPort)
		internal.Catch(cmd.Flags(), err)
	},
}
