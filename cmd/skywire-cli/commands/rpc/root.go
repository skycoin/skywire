package clirpc

import (
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	logger   = logging.MustGetLogger("skywire-cli")
	RPCAddr  string
	path     string
	pk       string
	url      string
	pkg      bool
	ipAddr   string
	localIPs []net.IP
	err      error
)

// Client is used by other skywire-cli commands to query the visor rpc
func Client() visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", RPCAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed; is skywire running?\n", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0)
}
