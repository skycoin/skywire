// Package network pkg/transport/network/listener.go
package network

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Listener represents a skywire network listener. It wraps net.Listener
// with other skywire-specific data
// Listener implements net.Listener
type Listener interface {
	net.Listener
	PK() cipher.PubKey
	Port() uint16
	Network() types.Type
	AcceptTransport() (Transport, error)
}

type listener struct {
	lAddr    dmsg.Addr
	mx       sync.Mutex
	once     sync.Once
	freePort func()
	accept   chan *transport
	done     chan struct{}
	network  types.Type
	log      *logging.Logger
}

// NewListener returns a new Listener.
func NewListener(lAddr dmsg.Addr, freePort func(), network types.Type) Listener {
	return newListener(lAddr, freePort, network)
}

func newListener(lAddr dmsg.Addr, freePort func(), network types.Type) *listener {
	return &listener{
		lAddr:    lAddr,
		freePort: freePort,
		accept:   make(chan *transport, 10), // Buffered to reduce blocking
		done:     make(chan struct{}),
		network:  network,
		log:      logging.MustGetLogger("listener"),
	}
}

// Accept implements net.Listener, returns generic net.Conn
func (l *listener) Accept() (net.Conn, error) {
	return l.AcceptTransport()
}

// AcceptTransport accepts a skywire transport and returns network.Transport
func (l *listener) AcceptTransport() (Transport, error) {
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
		l.mx.Lock()
		close(l.accept)
		l.mx.Unlock()
		for transport := range l.accept {
			transport.Close() //nolint: errcheck, gosec
		}
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
func (l *listener) Network() types.Type {
	return l.network
}

// Introduce is used by Client to introduce a new transport to this Listener
// If the transport cannot be delivered within 30 seconds, it is closed and an error is returned
func (l *listener) introduce(transport *transport) error {
	select {
	case <-l.done:
		return io.ErrClosedPipe
	default:
		l.mx.Lock()
		defer l.mx.Unlock()

		// Try to deliver transport with a timeout to prevent accumulation
		timeout := time.NewTimer(30 * time.Second)
		defer timeout.Stop()

		select {
		case l.accept <- transport:
			return nil
		case <-timeout.C:
			// No one is consuming from this listener, close the transport to prevent leak
			l.log.Warn("Transport not accepted within timeout, closing to prevent connection leak")
			transport.Close() //nolint:errcheck,gosec
			return io.ErrClosedPipe
		case <-l.done:
			return io.ErrClosedPipe
		}
	}
}
