package dmsg

import (
	"fmt"
	"net"
	"sync"
)

// Listener listens for remote-initiated streams.
type Listener struct {
	addr Addr // local listening address

	accept chan *Stream
	mx     sync.Mutex // protects 'accept'

	doneFunc func() // callback when done
	done     chan struct{}
	once     sync.Once
}

func newListener(addr Addr) *Listener {
	return &Listener{
		addr:   addr,
		accept: make(chan *Stream, AcceptBufferSize),
		done:   make(chan struct{}),
	}
}

// AddCloseCallback adds a function that triggers when listener is closed.
// This should be called right after the listener is created and is not thread safe.
func (l *Listener) AddCloseCallback(cb func()) { l.doneFunc = cb }

// IntroduceStream handles a stream after receiving a REQUEST frame.
func (l *Listener) IntroduceStream(tp *Stream) error {
	if tp.LocalAddr() != l.addr {
		return fmt.Errorf("failed to accept stream as local addresses does not match: we expected %s but got %s",
			l.addr, tp.LocalAddr())
	}

	l.mx.Lock()
	defer l.mx.Unlock()

	if l.isClosed() {
		_ = tp.Close() //nolint:errcheck
		return ErrClientClosed
	}

	select {
	case l.accept <- tp:
		return nil
	case <-l.done:
		_ = tp.Close() //nolint:errcheck
		return ErrClientClosed
	default:
		_ = tp.Close() //nolint:errcheck
		return ErrClientAcceptMaxed
	}
}

// Accept accepts a connection.
func (l *Listener) Accept() (net.Conn, error) {
	return l.AcceptStream()
}

// AcceptStream accepts a stream connection.
func (l *Listener) AcceptStream() (*Stream, error) {
	select {
	case <-l.done:
		return nil, ErrClientClosed
	case tp, ok := <-l.accept:
		if !ok {
			return nil, ErrClientClosed
		}
		return tp, nil
	}
}

// Close closes the listener.
func (l *Listener) Close() error {
	if l.close() {
		return nil
	}
	return ErrClientClosed
}

func (l *Listener) close() (closed bool) {
	l.once.Do(func() {
		closed = true
		l.doneFunc()

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

// Type returns the stream type.
func (l *Listener) Type() string { return Type }
