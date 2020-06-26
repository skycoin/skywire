package directtransport

import (
	"context"

	"github.com/SkycoinProject/dmsg/cipher"
)

type Client interface {
	Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error)
	Listen(lPort uint16) (*Listener, error)
	Serve() error
	Close() error
	Type() string
}
