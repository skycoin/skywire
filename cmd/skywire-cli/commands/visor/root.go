package visor

import (
	"net"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/pkg/visor"
)

var logger = logging.MustGetLogger("skywire-cli")

var rpcAddr string

func init() {
	RootCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "visor",
	Short: "Contains sub-commands that interact with the local Skywire Visor",
}

func rpcClient() visor.RPCClient {
	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed:", err)
	}
	if err := conn.SetDeadline(time.Now().Add(rpcConnDuration)); err != nil {
		logger.Fatal("RPC connection failed:", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, skyenv.DefaultRPCTimeout)
}

const (
	rpcDialTimeout  = time.Second * 5
	rpcConnDuration = time.Second * 60
)
