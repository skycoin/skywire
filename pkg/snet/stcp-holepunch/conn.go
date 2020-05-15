package stcph

import (
	"net"
	"time"

	"github.com/SkycoinProject/dmsg"
)

// Conn wraps an underlying net.Conn and modifies various methods to integrate better with the 'network' package.
type Conn struct {
	net.Conn
	lAddr    dmsg.Addr
	rAddr    dmsg.Addr
	freePort func()
}

func newConn(conn net.Conn, deadline time.Time, hs Handshake, freePort func()) (*Conn, error) {
	lAddr, rAddr, err := hs(conn, deadline)
	if err != nil {
		_ = conn.Close() //nolint:errcheck

		if freePort != nil {
			freePort()
		}

		return nil, err
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
