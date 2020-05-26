package stcpr

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

type connConfig struct {
	log       *logging.Logger
	conn      net.Conn
	localPK   cipher.PubKey
	localSK   cipher.SecKey
	deadline  time.Time
	hs        Handshake
	freePort  func()
	encrypt   bool
	initiator bool
}

func newConn(c connConfig) (*Conn, error) {
	lAddr, rAddr, err := c.hs(c.conn, c.deadline)
	if err != nil {
		_ = c.conn.Close() //nolint:errcheck

		if c.freePort != nil {
			c.freePort()
		}

		return nil, err
	}

	// TODO: extract from handshake whether encryption needed
	if c.encrypt {
		config := noise.Config{
			LocalPK:   c.localPK,
			LocalSK:   c.localSK,
			RemotePK:  rAddr.PK,
			Initiator: c.initiator,
		}

		wrappedConn, err := noisewrapper.WrapConn(config, c.conn)
		if err != nil {
			return nil, fmt.Errorf("encrypt connection to %v@%v: %w", rAddr, c.conn.RemoteAddr(), err)
		}

		c.conn = wrappedConn

		if c.log != nil {
			c.log.Infof("Connection with %v@%v is encrypted", rAddr, c.conn.RemoteAddr())
		}
	} else if c.log != nil {
		c.log.Infof("Connection with %v@%v is NOT encrypted", rAddr, c.conn.RemoteAddr())
	}

	return &Conn{Conn: c.conn, lAddr: lAddr, rAddr: rAddr, freePort: c.freePort}, nil
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
