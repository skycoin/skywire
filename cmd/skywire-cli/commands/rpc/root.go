package clirpc

import (
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	// RPCAddr is the address to reach the rpc server
	RPCAddr string
	logger  = logging.MustGetLogger("skywire-cli")
)

// RPCClient is used by the other cli commands to query the visor rpc
func RPCClient() visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", RPCAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed; is skywire running?\n", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0)
}
