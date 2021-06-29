package tpconn

import (
	"fmt"
	"net"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/noise"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"
)

// Conn wraps an underlying net.Conn and modifies various methods to integrate better with the 'network' package.
type Conn struct {
	net.Conn
	lAddr    dmsg.Addr
	rAddr    dmsg.Addr
	freePort func()
}

// Config describes a config for Conn.
type Config struct {
	Log       *logging.Logger
	Conn      net.Conn
	LocalPK   cipher.PubKey
	LocalSK   cipher.SecKey
	Deadline  time.Time
	Handshake handshake.Handshake
	FreePort  func()
	Encrypt   bool
	Initiator bool
}

// NewConn creates a new Conn.
func NewConn(c Config) (*Conn, error) {
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

		wrappedConn, err := encryptConn(config, c.Conn)
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

// EncryptConn encrypts given connection using noise config
func encryptConn(config noise.Config, conn net.Conn) (net.Conn, error) {
	ns, err := noise.New(noise.HandshakeKK, config)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare stream noise object: %w", err)
	}

	wrappedConn, err := noise.WrapConn(conn, ns, time.Second*3)
	if err != nil {
		return nil, fmt.Errorf("error performing noise handshake: %w", err)
	}

	return wrappedConn, nil
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
