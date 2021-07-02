package routerclient

import (
	"context"
	"net"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// WrapDmsgClient wraps a dmsg client to implement snet.Dialer
func WrapDmsgClient(dmsgC *dmsg.Client) network.Dialer {
	return &dmsgClientDialer{Client: dmsgC}
}

type dmsgClientDialer struct {
	*dmsg.Client
}

func (w *dmsgClientDialer) Dial(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error) {
	return w.Client.Dial(ctx, dmsg.Addr{PK: remote, Port: port})
}

func (w *dmsgClientDialer) Type() string {
	return string(network.DMSG)
}
