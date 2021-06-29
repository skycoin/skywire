package network

import (
	"fmt"
	"net"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/noise"

	"github.com/skycoin/skywire/pkg/snet/directtp/noisewrapper"
)

// Conn represents a network connection between two visors in skywire network
// This connection wraps raw network connection and is ready to use for sending data.
// It also provides skywire-specific methods on top of net.Conn
type Conn struct {
	net.Conn
	lAddr, rAddr dmsg.Addr
	freePort     func()
	connType     Type
}

func (c *Conn) encrypt(lPK cipher.PubKey, lSK cipher.SecKey, initator bool) error {
	config := noise.Config{
		LocalPK:   lPK,
		LocalSK:   lSK,
		RemotePK:  c.rAddr.PK,
		Initiator: initator,
	}

	wrappedConn, err := noisewrapper.WrapConn(config, c.Conn)
	if err != nil {
		return fmt.Errorf("encrypt connection to %v@%v: %w", c.rAddr, c.Conn.RemoteAddr(), err)
	}

	c.Conn = wrappedConn
	return nil
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

// LocalPK returns local public key of connection
func (c *Conn) LocalPK() cipher.PubKey { return c.lAddr.PK }

// RemotePK returns remote public key of connection
func (c *Conn) RemotePK() cipher.PubKey { return c.rAddr.PK }

// LocalPort returns local skywire port of connection
// This is not underlying OS port, but port within skywire network
func (c *Conn) LocalPort() uint16 { return c.lAddr.Port }

// RemotePort returns remote skywire port of connection
// This is not underlying OS port, but port within skywire network
func (c *Conn) RemotePort() uint16 { return c.rAddr.Port }

// Network returns network of connection
// todo: consider switching to Type instead of string
func (c *Conn) Network() string { return string(c.connType) }
