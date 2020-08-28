package tplistener

import (
	"io"
	"net"
	"sync"

	"github.com/skycoin/dmsg"

	"github.com/skycoin/skywire/pkg/snet/directtp/tpconn"
)

// Listener implements net.Listener
type Listener struct {
	lAddr    dmsg.Addr
	mx       sync.Mutex
	once     sync.Once
	freePort func()
	accept   chan *tpconn.Conn
	done     chan struct{}
}

// NewListener returns a new Listener.
func NewListener(lAddr dmsg.Addr, freePort func()) *Listener {
	return &Listener{
		lAddr:    lAddr,
		freePort: freePort,
		accept:   make(chan *tpconn.Conn),
		done:     make(chan struct{}),
	}
}

// Introduce is used by Client to introduce Conn to Listener.
func (l *Listener) Introduce(conn *tpconn.Conn) error {
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

// Accept implements net.Listener
func (l *Listener) Accept() (net.Conn, error) {
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
