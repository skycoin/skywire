// Package noise pkg/dmsg/noise/net.go c1-net-dmsg
package noise

import (
	"errors"
	"net"
	"time"

	"github.com/flynn/noise"

	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	// ErrAlreadyServing is returned when an operation fails due to an operation
	// that is currently running.
	ErrAlreadyServing = errors.New("already serving")

	// HandshakeXK is the XK handshake pattern.
	// 		legend: s(static) e(ephemeral)
	//	<- s
	//	...
	//	-> e, es
	//	<- e, ee
	//	-> s, se
	HandshakeXK = noise.HandshakeXK

	// HandshakeKK is the KK handshake pattern.
	// 		legend: s(static) e(ephemeral)
	//	-> s
	//	<- s
	//	...
	//	-> e, es, ss
	//	<- e, ee, se
	HandshakeKK = noise.HandshakeKK

	// AcceptHandshakeTimeout determines how long a noise hs should take.
	AcceptHandshakeTimeout = time.Second * 10
)

// Addr is the address of a either an AppNode or ManagerNode.
type Addr struct {
	PK   cipher.PubKey
	Addr net.Addr
}

// Network returns the network type.
func (a Addr) Network() string {
	return "noise"
}

// String implements fmt.Stringer
func (a Addr) String() string {
	return a.Addr.String() + "(" + a.PK.Hex() + ")"
}

// Conn wraps a net.Conn and encrypts the connection with noise.
type Conn struct {
	net.Conn
	ns *ReadWriter
}

// WrapConn wraps a provided net.Conn with noise.
func WrapConn(conn net.Conn, ns *Noise, hsTimeout time.Duration) (*Conn, error) {
	rw := NewReadWriter(conn, ns)
	if err := rw.Handshake(hsTimeout); err != nil {
		return nil, err
	}
	return &Conn{Conn: conn, ns: rw}, nil
}

// Read reads from the noise-encrypted connection.
func (c *Conn) Read(b []byte) (int, error) {
	return c.ns.Read(b)
}

// Write writes to the noise-encrypted connection.
func (c *Conn) Write(b []byte) (int, error) {
	return c.ns.Write(b)
}

// ChannelBinding returns the completed handshake's channel binding — see
// Noise.ChannelBinding. Lets a caller key a sibling channel (e.g. the
// faithful-UDP datagram route, #2607) off this encrypted route's session.
func (c *Conn) ChannelBinding() []byte {
	return c.ns.ChannelBinding()
}

// LocalAddr returns the local address of the connection.
func (c *Conn) LocalAddr() net.Addr {
	return &Addr{
		PK:   c.ns.LocalStatic(),
		Addr: c.Conn.LocalAddr(),
	}
}

// RemoteAddr returns the remote address of the connection.
func (c *Conn) RemoteAddr() net.Addr {
	return &Addr{
		PK:   c.ns.RemoteStatic(),
		Addr: c.Conn.RemoteAddr(),
	}
}

// Listener accepts incoming connections and encrypts with noise.
type Listener struct {
	net.Listener
	pk      cipher.PubKey
	sk      cipher.SecKey
	init    bool
	pattern noise.HandshakePattern
}

// WrapListener wraps a listener and encrypts incoming connections with noise.
func WrapListener(lis net.Listener, pk cipher.PubKey, sk cipher.SecKey, init bool, pattern noise.HandshakePattern) *Listener {
	return &Listener{Listener: lis, pk: pk, sk: sk, init: init, pattern: pattern}
}

// Accept calls Accept from the underlying net.Listener and encrypts the
// obtained net.Conn with noise.
func (ml *Listener) Accept() (net.Conn, error) {
	for {
		conn, err := ml.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ns, err := New(ml.pattern, Config{
			LocalPK:   ml.pk,
			LocalSK:   ml.sk,
			Initiator: ml.init,
		})
		if err != nil {
			return nil, err
		}
		rw := NewReadWriter(conn, ns)
		if err := rw.Handshake(AcceptHandshakeTimeout); err != nil {
			noiseLogger.WithError(err).Warn("accept: noise handshake failed.")
			conn.Close() //nolint:errcheck,gosec
			continue
		}
		noiseLogger.Infoln("accepted:", rw.RemoteStatic())
		return &Conn{Conn: conn, ns: rw}, nil
	}
}

// Addr returns the local address of the noise-encrypted Listener.
func (ml *Listener) Addr() net.Addr {
	return &Addr{
		PK:   ml.pk,
		Addr: ml.Listener.Addr(),
	}
}
