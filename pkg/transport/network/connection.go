package network

import (
	"fmt"
	"net"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/noise"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/snet/directtp/noisewrapper"
	"github.com/skycoin/skywire/pkg/snet/directtp/tphandshake"
)

// Conn wraps an underlying net.Conn and modifies various methods to integrate better with the 'network' package.
type Conn struct {
	net.Conn
	lAddr, rAddr dmsg.Addr
	freePort     func()
	connType     Type
}

// ConnConfig describes a config for Conn.
type ConnConfig struct {
	Log       *logging.Logger
	Conn      net.Conn
	LocalSK   cipher.SecKey
	LocalPK   cipher.PubKey
	Deadline  time.Time
	Handshake tphandshake.Handshake
	FreePort  func()
	Encrypt   bool
	Initiator bool
}

// NewConn creates a new Conn.
// todo: move out handshake
func NewConn(c ConnConfig, connType Type) (*Conn, error) {
	if c.Log != nil {
		c.Log.Infof("Performing handshake with %v", c.Conn.RemoteAddr())
	}

	lAddr, rAddr, err := c.Handshake(c.Conn, c.Deadline)
	if err != nil {
		if err := c.Conn.Close(); err != nil && c.Log != nil {
			c.Log.WithError(err).Warnf("Failed to close connection")
		}

		if c.FreePort != nil {
			c.FreePort()
		}

		return nil, err
	}

	if c.Log != nil {
		c.Log.Infof("Sent handshake to %v, local addr %v, remote addr %v", c.Conn.RemoteAddr(), lAddr, rAddr)
	}

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

	return &Conn{Conn: c.Conn, lAddr: lAddr, rAddr: rAddr, freePort: c.FreePort, connType: connType}, nil
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

// LocalPK returns local public key of connection.
func (c *Conn) LocalPK() cipher.PubKey { return c.lAddr.PK }

// RemotePK returns remote public key of connection.
func (c *Conn) RemotePK() cipher.PubKey { return c.rAddr.PK }

// LocalPort returns local port of connection.
func (c *Conn) LocalPort() uint16 { return c.lAddr.Port }

// RemotePort returns remote port of connection.
func (c *Conn) RemotePort() uint16 { return c.rAddr.Port }

// Network returns network of connection.
func (c *Conn) Network() string { return string(c.connType) }
