// Package network pkg/transport/network/connection.go
package network

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/noise"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const encryptHSTimout = 5 * time.Second

// Transport represents a network connection between two visors in skywire network
// This transport wraps raw network connection and is ready to use for sending data.
// It also provides skywire-specific methods on top of net.Conn
type Transport interface {
	net.Conn
	// LocalPK returns local public key of transport
	LocalPK() cipher.PubKey

	// RemotePK returns remote public key of transport
	RemotePK() cipher.PubKey

	// LocalPort returns local skywire port of transport
	// This is not underlying OS port, but port within skywire network
	LocalPort() uint16

	// RemotePort returns remote skywire port of transport
	// This is not underlying OS port, but port within skywire network
	RemotePort() uint16

	// LocalRawAddr returns local raw network address (not skywire address)
	LocalRawAddr() net.Addr

	// RemoteRawAddr returns remote raw network address (not skywire address)
	RemoteRawAddr() net.Addr

	// Network returns network of transport
	Network() types.Type
}

type transport struct {
	net.Conn
	lAddr, rAddr  dmsg.Addr
	freePort      func()
	transportType types.Type
}

// DoHandshake performs given handshake over given raw connection and wraps
// connection in network.Transport
func DoHandshake(rawConn net.Conn, hs handshake.Handshake, netType types.Type, log *logging.Logger) (Transport, error) {
	return doHandshake(rawConn, hs, netType, log)
}

// handshake performs given handshake over given raw connection and wraps
// connection in network.transport
func doHandshake(rawConn net.Conn, hs handshake.Handshake, netType types.Type, log *logging.Logger) (*transport, error) {
	lAddr, rAddr, err := hs(rawConn, time.Now().Add(handshake.Timeout))
	if err != nil {
		if err := rawConn.Close(); err != nil {
			log.WithError(err).Warnf("Failed to close connection")
		}
		return nil, err
	}
	handshakedConn := &transport{Conn: rawConn, lAddr: lAddr, rAddr: rAddr, transportType: netType}
	return handshakedConn, nil
}

func (c *transport) encrypt(lPK cipher.PubKey, lSK cipher.SecKey, initator bool) error {
	config := noise.Config{
		LocalPK:   lPK,
		LocalSK:   lSK,
		RemotePK:  c.rAddr.PK,
		Initiator: initator,
	}

	wrappedConn, err := EncryptConn(config, c.Conn)
	if err != nil {
		return fmt.Errorf("encrypt connection to %v@%v: %w", c.rAddr, c.Conn.RemoteAddr(), err)
	}

	c.Conn = wrappedConn
	return nil
}

// debugConn wraps a net.Conn to capture initial bytes for debugging noise handshake failures
type debugConn struct {
	net.Conn
	reader      *bufio.Reader
	capturedBuf []byte
}

func newDebugConn(conn net.Conn) *debugConn {
	return &debugConn{
		Conn:   conn,
		reader: bufio.NewReader(conn),
	}
}

func (d *debugConn) Read(p []byte) (int, error) {
	n, err := d.reader.Read(p)
	// Capture first 64 bytes for debugging
	if len(d.capturedBuf) < 64 && n > 0 {
		remaining := 64 - len(d.capturedBuf)
		if n < remaining {
			remaining = n
		}
		d.capturedBuf = append(d.capturedBuf, p[:remaining]...)
	}
	return n, err
}

func (d *debugConn) capturedData() string {
	if len(d.capturedBuf) == 0 {
		return "(no data captured)"
	}
	hexStr := hex.EncodeToString(d.capturedBuf)
	// Try to show as ASCII if printable
	var ascii strings.Builder
	for _, b := range d.capturedBuf {
		if b >= 32 && b < 127 {
			ascii.WriteByte(b)
		} else {
			ascii.WriteByte('.')
		}
	}
	return fmt.Sprintf("hex=%s ascii=%s", hexStr, ascii.String())
}

// Ensure debugConn implements io.Reader for bufio
var _ io.Reader = (*debugConn)(nil)

// EncryptConn encrypts given connection
func EncryptConn(config noise.Config, conn net.Conn) (net.Conn, error) {
	ns, err := noise.New(noise.HandshakeKK, config)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare stream noise object: %w", err)
	}

	// Wrap connection with debug reader to capture bytes on failure
	dc := newDebugConn(conn)

	wrappedConn, err := noise.WrapConn(dc, ns, encryptHSTimout)
	if err != nil {
		return nil, fmt.Errorf("error performing noise handshake (first bytes: %s): %w", dc.capturedData(), err)
	}

	return wrappedConn, nil
}

// LocalAddr implements net.Conn
func (c *transport) LocalAddr() net.Addr {
	return c.lAddr
}

// RemoteAddr implements net.Conn
func (c *transport) RemoteAddr() net.Addr {
	return c.rAddr
}

// LocalAddr implements net.Conn
func (c *transport) LocalRawAddr() net.Addr {
	return c.Conn.LocalAddr()
}

// RemoteAddr implements net.Conn
func (c *transport) RemoteRawAddr() net.Addr {
	return c.Conn.RemoteAddr()
}

// Close implements net.Conn
func (c *transport) Close() error {
	if c.freePort != nil {
		c.freePort()
	}

	return c.Conn.Close()
}

// LocalPK returns local public key of transport
func (c *transport) LocalPK() cipher.PubKey { return c.lAddr.PK }

// RemotePK returns remote public key of transport
func (c *transport) RemotePK() cipher.PubKey { return c.rAddr.PK }

// LocalPort returns local skywire port of transport
// This is not underlying OS port, but port within skywire network
func (c *transport) LocalPort() uint16 { return c.lAddr.Port }

// RemotePort returns remote skywire port of transport
// This is not underlying OS port, but port within skywire network
func (c *transport) RemotePort() uint16 { return c.rAddr.Port }

// Network returns network of transport
func (c *transport) Network() types.Type { return c.transportType }
