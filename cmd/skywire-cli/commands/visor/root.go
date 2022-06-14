package visor

import (
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/commands/visor/vapps"
	"github.com/skycoin/skywire/cmd/skywire-cli/commands/visor/vroute"
	"github.com/skycoin/skywire/cmd/skywire-cli/commands/visor/vtp"
	"github.com/skycoin/skywire/cmd/skywire-cli/commands/visor/vvpn"
	"github.com/skycoin/skywire/pkg/visor"
)

var logger = logging.MustGetLogger("skywire-cli")

var rpcAddr string

func init() {
	RootCmd.AddCommand(vapps.RootCmd)
	RootCmd.AddCommand(vroute.RootCmd)
	RootCmd.AddCommand(vtp.RootCmd)
	RootCmd.AddCommand(vvpn.RootCmd)
	RootCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "visor",
	Short: "Query the Skywire Visor",
}

func rpcClient() visor.API {
	const rpcDialTimeout = time.Second * 5

	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed:", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0)
}
