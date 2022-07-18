package clirpc

import (
	"net"
	"time"


	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	RpcAddr string
	logger  = logging.MustGetLogger("skywire-cli")

)

func RpcClient() visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", RpcAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed; is skywire running?\n", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0)
}
