package therealssh

import (
	"net"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
)

// dialer dials to a remote node.
type dialer interface {
	Dial(raddr appnet.Addr) (net.Conn, error)
}
