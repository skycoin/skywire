// Package dmsg pkg/dmsg/dmsg/listener.go c1-net-dmsg
package dmsg

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
)

var listenerLog = logging.MustGetLogger("dmsg_listener")

// Listener listens for remote-initiated streams.
type Listener struct {
	porter *netutil.Porter
	addr   Addr // local listening address

	accept chan *Stream
	// handed holds connections handed over in process (see Hand). Accept
	// returns them with the dmsg streams.
	handed chan net.Conn
	mx     sync.Mutex // protects 'accept' and 'handed'

	doneFunc atomic.Value // callback when done, type: func()
	done     chan struct{}
	once     sync.Once

	// onInbound, when non-nil, is called each time a remote-initiated stream is
	// successfully queued for Accept. It is the client's evidence that the dmsg
	// server is still forwarding inbound streams to us — see
	// EntityCommon.markInbound. Called with l.mx held, so it must not block.
	onInbound func()

	// traffic is the owning client's per-port counters (traffic.go).
	traffic *trafficRegistry
}

func newListener(porter *netutil.Porter, addr Addr, onInbound func()) *Listener {
	return &Listener{
		porter:    porter,
		addr:      addr,
		accept:    make(chan *Stream, AcceptBufferSize),
		handed:    make(chan net.Conn, AcceptBufferSize),
		done:      make(chan struct{}),
		onInbound: onInbound,
	}
}

// addCloseCallback adds a function that triggers when listener is closed.
// This should be called right after the listener is created and is not thread safe.
func (l *Listener) addCloseCallback(cb func()) { l.doneFunc.Store(cb) }

// introduceStream handles a stream after receiving a REQUEST frame.
func (l *Listener) introduceStream(tp *Stream) error {
	if tp.LocalAddr() != l.addr {
		return fmt.Errorf("local addresses do not match: expected %s but got %s", l.addr, tp.LocalAddr())
	}

	l.mx.Lock()
	defer l.mx.Unlock()

	if l.isClosed() {
		_ = tp.Close() //nolint:errcheck
		return ErrEntityClosed
	}

	if pt := l.traffic.port(l.addr.Port); pt != nil {
		pt.streams.Add(1)
		tp.traffic = pt
	}

	select {
	case l.accept <- tp:
		// A remote peer just reached us through the dmsg server, which is exactly
		// what a self-probe dial sets out to prove — with a real remote instead of
		// ourselves. Recording it lets the visor's self-probe stay quiet while
		// there is live evidence of reachability.
		if l.onInbound != nil {
			l.onInbound()
		}
		return nil

	case <-l.done:
		_ = tp.Close() //nolint:errcheck
		return ErrEntityClosed

	default:
		listenerLog.WithField("addr", l.addr).Warn("Accept buffer full, dropping stream.")
		_ = tp.Close() //nolint:errcheck
		return ErrAcceptChanMaxed
	}
}

// Accept accepts a connection: a dmsg stream, or one handed over in process.
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case tp, ok := <-l.accept:
		return l.accepted(tp, ok)
	case c, ok := <-l.handed:
		if !ok {
			return nil, ErrEntityClosed
		}
		return c, nil
	case <-l.done:
		return nil, ErrEntityClosed
	}
}

// AcceptStream accepts a stream connection.
func (l *Listener) AcceptStream() (*Stream, error) {
	select {
	case tp, ok := <-l.accept:
		return l.accepted(tp, ok)
	case <-l.done:
		return nil, ErrEntityClosed
	}
}

func (l *Listener) accepted(tp *Stream, ok bool) (*Stream, error) {
	if !ok {
		return nil, ErrEntityClosed
	}
	if ok, closeFn := l.porter.ReserveChild(tp.lAddr.Port, tp.rAddr.Port, tp); ok {
		tp.close = closeFn
	}
	tp.census.Load().set(0, "accepted")
	return tp, nil
}

// Hand queues conn for Accept as a connection from remote, for a host visor
// that took it in over a skywire transport for a service it runs.
func (l *Listener) Hand(conn net.Conn, remote Addr) error {
	l.mx.Lock()
	defer l.mx.Unlock()
	if l.isClosed() {
		return ErrEntityClosed
	}
	select {
	case l.handed <- &handedConn{Conn: conn, local: l.addr, remote: remote}:
		return nil
	default:
		return ErrAcceptChanMaxed
	}
}

// Close closes the listener.
func (l *Listener) Close() error {
	if l.close() {
		return nil
	}
	return ErrEntityClosed
}

func (l *Listener) close() (closed bool) {
	l.once.Do(func() {
		closed = true

		doneFunc, ok := l.doneFunc.Load().(func())
		if ok {
			doneFunc()
		}

		l.mx.Lock()
		defer l.mx.Unlock()

		close(l.done)
		for {
			select {
			case c := <-l.handed:
				c.Close() //nolint:errcheck,gosec
				continue
			default:
			}
			select {
			case stream := <-l.accept:
				stream.Close() //nolint:errcheck,gosec
			default:
				close(l.accept)
				close(l.handed)
				return
			}
		}
	})
	return closed
}

func (l *Listener) isClosed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

// Addr returns the listener's address.
func (l *Listener) Addr() net.Addr { return l.addr }

// DmsgAddr returns the listener's address in as `dmsg.Addr`.
func (l *Listener) DmsgAddr() Addr { return l.addr }

// Type returns the stream type.
func (l *Listener) Type() string { return Type }

// handedConn reports the listener's address and the caller's, as a dmsg
// stream would, so a service reads the caller's key the same way.
type handedConn struct {
	net.Conn
	local, remote Addr
}

func (c *handedConn) LocalAddr() net.Addr  { return c.local }
func (c *handedConn) RemoteAddr() net.Addr { return c.remote }
