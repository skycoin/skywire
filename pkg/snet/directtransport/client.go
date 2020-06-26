package directtransport

import (
	"context"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
)

// TODO(nkryuchkov): use
type Client interface {
	Serve() error
	Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error)
	SetLogger(log *logging.Logger) // TODO: remove
}
