package vtp

import (
	"net"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var logger = logging.MustGetLogger("skywire-cli")

var rpcAddr string

func init() {
	RootCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "tp",
	Short: "view and set transports",
}

func rpcClient() visor.API {
	const rpcDialTimeout = time.Second * 5

	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed:", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0)
}
