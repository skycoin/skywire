// Package clirpc root.go
package clirpc

import (
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	logger = logging.MustGetLogger("skywire-cli")
	//Addr is the address (ip:port) of the rpc server
	Addr string
)

// Client is used by other skywire-cli commands to query the visor rpc
func Client() visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", Addr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed; is skywire running?\n", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0)
}
