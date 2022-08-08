package clihv

import (
	"net"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	logger   = logging.MustGetLogger("skywire-cli")
	rpcAddr  string
	path     string
	pk       string
	url      string
	pkg      bool
	ipAddr   string
	localIPs []net.IP
	err      error
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "hv",
	Short: "Open HVUI in browser",
}

func rpcClient() visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed; is skywire running?\n", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0)
}
