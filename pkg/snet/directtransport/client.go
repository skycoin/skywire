package directtransport

import (
	"context"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
)

// TODO(nkryuchkov): use
type Client interface {
	SetLogger(log *logging.Logger) // TODO(nkryuchkov): remove
	Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error)
	Listen(lPort uint16) (*Listener, error)
	Serve() error
	Close() error
	Type() string
}
