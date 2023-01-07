// Package dmsg pkg/dmsg/listener.go
package dmsg

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/skycoin/skywire-utilities/pkg/netutil"
)

// Listener listens for remote-initiated streams.
type Listener struct {
	porter *netutil.Porter
	addr   Addr // local listening address

	accept chan *Stream
	mx     sync.Mutex // protects 'accept'

	doneFunc atomic.Value // callback when done, type: func()
	done     chan struct{}
	once     sync.Once
}

func newListener(porter *netutil.Porter, addr Addr) *Listener {
	return &Listener{
		porter: porter,
		addr:   addr,
		accept: make(chan *Stream, AcceptBufferSize),
		done:   make(chan struct{}),
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
		return nil

	case <-l.done:
		_ = tp.Close() //nolint:errcheck
		return ErrEntityClosed

	default:
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
			case <-l.accept:
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
