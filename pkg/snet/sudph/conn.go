package sudph

import (
	"fmt"
	"net"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/noise"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/noisewrapper"
)

// Conn wraps an underlying net.Conn and modifies various methods to integrate better with the 'network' package.
type Conn struct {
	net.Conn
	lAddr    dmsg.Addr
	rAddr    dmsg.Addr
	freePort func()
}

type ConnConfig struct {
	Log       *logging.Logger
	Conn      net.Conn
	LocalPK   cipher.PubKey
	LocalSK   cipher.SecKey
	Deadline  time.Time
	Handshake Handshake
	FreePort  func()
	Encrypt   bool
	Initiator bool
}

func NewConn(c ConnConfig) (*Conn, error) {
	log.Infof("Performing handshake with %v", c.Conn.RemoteAddr())
	lAddr, rAddr, err := c.Handshake(c.Conn, c.Deadline)
	log.Infof("c.hs result laddr=%v raddr=%v err=%v", lAddr, rAddr, err)

	if err != nil { // TODO: errors are not caught here
		if err := c.Conn.Close(); err != nil && c.Log != nil {
			c.Log.WithError(err).Warnf("Failed to close sudph connection")
		}

		if c.FreePort != nil {
			c.FreePort()
		}

		return nil, err
	}
	log.Infof("Sent handshake to %v, local addr %v, remote addr %v", c.Conn.RemoteAddr(), lAddr, rAddr)

	// TODO(nkryuchkov): extract from handshake whether encryption is needed
	if c.Encrypt {
		config := noise.Config{
			LocalPK:   c.LocalPK,
			LocalSK:   c.LocalSK,
			RemotePK:  rAddr.PK,
			Initiator: c.Initiator,
		}

		wrappedConn, err := noisewrapper.WrapConn(config, c.Conn)
		if err != nil {
			return nil, fmt.Errorf("encrypt connection to %v@%v: %w", rAddr, c.Conn.RemoteAddr(), err)
		}

		c.Conn = wrappedConn

		if c.Log != nil {
			c.Log.Infof("Connection with %v@%v is encrypted", rAddr, c.Conn.RemoteAddr())
		}
	} else if c.Log != nil {
		c.Log.Infof("Connection with %v@%v is NOT encrypted", rAddr, c.Conn.RemoteAddr())
	}

	return &Conn{Conn: c.Conn, lAddr: lAddr, rAddr: rAddr, freePort: c.FreePort}, nil
}

// LocalAddr implements net.Conn
func (c *Conn) LocalAddr() net.Addr {
	return c.lAddr
}

// RemoteAddr implements net.Conn
func (c *Conn) RemoteAddr() net.Addr {
	return c.rAddr
}

// Close implements net.Conn
func (c *Conn) Close() error {
	if c.freePort != nil {
		c.freePort()
	}

	return c.Conn.Close()
}
