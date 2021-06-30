package network

import (
	"io"
	"net"
	"sync"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
)

// Listener represents a skywire network listener. It wraps net.Listener
// with other skywire-specific data
// Listener implements net.Listener
type Listener interface {
	net.Listener
	PK() cipher.PubKey
	Port() uint16
	Network() Type
	AcceptConn() (Conn, error)
}

type listener struct {
	lAddr    dmsg.Addr
	mx       sync.Mutex
	once     sync.Once
	freePort func()
	accept   chan *conn
	done     chan struct{}
	network  Type
}

// NewListener returns a new Listener.
func NewListener(lAddr dmsg.Addr, freePort func(), network Type) Listener {
	return newListener(lAddr, freePort, network)
}

func newListener(lAddr dmsg.Addr, freePort func(), network Type) *listener {
	return &listener{
		lAddr:    lAddr,
		freePort: freePort,
		accept:   make(chan *conn),
		done:     make(chan struct{}),
		network:  network,
	}
}

// Accept implements net.Listener, returns generic net.Conn
func (l *listener) Accept() (net.Conn, error) {
	return l.AcceptConn()
}

// AcceptConn accepts a skywire connection and returns network.Conn
func (l *listener) AcceptConn() (Conn, error) {
	c, ok := <-l.accept
	if !ok {
		return nil, io.ErrClosedPipe
	}

	return c, nil
}

// Close implements net.Listener
func (l *listener) Close() error {
	l.once.Do(func() {
		close(l.done)
		// todo: consider if removing locks will change anything
		// todo: close all pending connections in l.accept
		l.mx.Lock()
		close(l.accept)
		l.mx.Unlock()

		l.freePort()
	})

	return nil
}

// Addr implements net.Listener
func (l *listener) Addr() net.Addr {
	return l.lAddr
}

// Addr implements net.Listener
func (l *listener) PK() cipher.PubKey {
	return l.lAddr.PK
}

// Addr implements net.Listener
func (l *listener) Port() uint16 {
	return l.lAddr.Port
}

// Network returns network type
// todo: consider switching to Type instead of string
func (l *listener) Network() Type {
	return l.network
}

// Introduce is used by Client to introduce a new connection to this Listener
func (l *listener) introduce(conn *conn) error {
	// todo: think if this is needed
	select {
	case <-l.done:
		return io.ErrClosedPipe
	default:
		l.mx.Lock()
		defer l.mx.Unlock()

		select {
		case l.accept <- conn:
			return nil
		case <-l.done:
			return io.ErrClosedPipe
		}
	}
}
