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
