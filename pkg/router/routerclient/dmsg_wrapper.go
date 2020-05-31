package routerclient

import (
	"context"
	"net"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
)

// WrapDmsgClient wraps a dmsg client to implement snet.Dialer
func WrapDmsgClient(dmsgC *dmsg.Client) snet.Dialer {
	return &dmsgClientDialer{Client: dmsgC}
}

type dmsgClientDialer struct {
	*dmsg.Client
}

func (w *dmsgClientDialer) Dial(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error) {
	return w.Client.Dial(ctx, dmsg.Addr{PK: remote, Port: port})
}

func (w *dmsgClientDialer) Type() string {
	return snet.DmsgType
}
