package stcpr

import (
	"fmt"
	"net"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/noise"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/noisewrapper"
)

// Conn wraps an underlying net.Conn and modifies various methods to integrate better with the 'network' package.
type Conn struct {
	net.Conn
	lAddr    dmsg.Addr
	rAddr    dmsg.Addr
	freePort func()
}

// TODO: too many args
func (c *Client) newConn(conn net.Conn, deadline time.Time, hs Handshake, freePort func(), encrypt, initiator bool) (*Conn, error) {
	lAddr, rAddr, err := hs(conn, deadline)
	if err != nil {
		_ = conn.Close() //nolint:errcheck

		if freePort != nil {
			freePort()
		}

		return nil, err
	}

	// TODO: extract from handshake whether encryption needed
	if encrypt {
		config := noise.Config{
			LocalPK:   c.lPK,
			LocalSK:   c.lSK,
			RemotePK:  rAddr.PK,
			Initiator: initiator,
		}

		wrappedConn, err := noisewrapper.WrapConn(config, conn)
		if err != nil {
			return nil, fmt.Errorf("encrypt connection to %v@%v: %w", rAddr, conn.RemoteAddr(), err)
		}

		conn = wrappedConn

		c.log.Infof("Connection with %v@%v is encrypted", rAddr, conn.RemoteAddr())
	} else {
		c.log.Infof("Connection with %v@%v is NOT encrypted", rAddr, conn.RemoteAddr())
	}

	return &Conn{Conn: conn, lAddr: lAddr, rAddr: rAddr, freePort: freePort}, nil
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
