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
	mx     sync.Mutex // protects 'accept'

	doneFunc atomic.Value // callback when done, type: func()
	done     chan struct{}
	once     sync.Once

	// onInbound, when non-nil, is called each time a remote-initiated stream is
	// successfully queued for Accept. It is the client's evidence that the dmsg
	// server is still forwarding inbound streams to us — see
	// EntityCommon.markInbound. Called with l.mx held, so it must not block.
	onInbound func()
}

func newListener(porter *netutil.Porter, addr Addr, onInbound func()) *Listener {
	return &Listener{
		porter:    porter,
		addr:      addr,
		accept:    make(chan *Stream, AcceptBufferSize),
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

// Accept accepts a connection.
func (l *Listener) Accept() (net.Conn, error) {
	return l.AcceptStream()
}

// AcceptStream accepts a stream connection.
func (l *Listener) AcceptStream() (*Stream, error) {
	select {
	case tp, ok := <-l.accept:
		if !ok {
			return nil, ErrEntityClosed
		}

		if ok, closeFn := l.porter.ReserveChild(tp.lAddr.Port, tp.rAddr.Port, tp); ok {
			tp.close = closeFn
		}

		return tp, nil

	case <-l.done:
		return nil, ErrEntityClosed
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
			case stream := <-l.accept:
				stream.Close() //nolint:errcheck,gosec
			default:
				close(l.accept)
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
