// Package clirpc root.go
package clirpc

import (
	"fmt"
	"net"
	"time"

	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	logger = logging.MustGetLogger("skywire-cli")
	//Addr is the address (ip:port) of the rpc server
	Addr string
)

// Client is used by other skywire-cli commands to query the visor rpc
func Client(cmdFlags *pflag.FlagSet) (visor.API, error) {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", Addr, rpcDialTimeout)
	if err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
		return nil, err
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0), nil
}
