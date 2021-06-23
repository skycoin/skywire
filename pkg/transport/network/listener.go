package network

import (
	"io"
	"net"
	"sync"

	"github.com/skycoin/dmsg"
)

// Listener represents a skywire network listener. It wraps net.Listener
// with other skywire-specific data
// Listener implements net.Listener
type Listener struct {
	lAddr    dmsg.Addr
	mx       sync.Mutex
	once     sync.Once
	freePort func()
	accept   chan *Conn
	done     chan struct{}
	network  Type
}

// NewListener returns a new Listener.
func NewListener(lAddr dmsg.Addr, freePort func(), network Type) *Listener {
	return &Listener{
		lAddr:    lAddr,
		freePort: freePort,
		accept:   make(chan *Conn),
		done:     make(chan struct{}),
		network:  network,
	}
}

// Introduce is used by Client to introduce Conn to Listener.
func (l *Listener) Introduce(conn *Conn) error {
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

// Accept implements net.Listener, returns generic net.Conn
func (l *Listener) Accept() (net.Conn, error) {
	return l.AcceptConn()
}

// AcceptConn accepts a skywire connection and returns network.Conn
func (l *Listener) AcceptConn() (*Conn, error) {
	c, ok := <-l.accept
	if !ok {
		return nil, io.ErrClosedPipe
	}

	return c, nil
}

// Close implements net.Listener
func (l *Listener) Close() error {
	l.once.Do(func() {
		close(l.done)

		l.mx.Lock()
		close(l.accept)
		l.mx.Unlock()

		l.freePort()
	})

	return nil
}

// Addr implements net.Listener
func (l *Listener) Addr() net.Addr {
	return l.lAddr
}

// Network returns network type
func (l *Listener) Network() string {
	return string(l.network)
}
