package transport

import (
	"net"
	"sync"
)

// UDPFactory creates and manages UDP connections.
// Currently implemented over TCP for reliable delivery.
// A future version may use a true reliable UDP protocol.
type UDPFactory struct {
	AcceptedCallback func(conn *Connection)

	// MaxPendingAccepts caps in-flight AcceptedCallback invocations.
	// Zero (default) leaves accepts unbounded for backwards compatibility;
	// set it from the Node's MaxPendingConnections to apply backpressure
	// to the listener queue when handshake processing falls behind.
	MaxPendingAccepts int

	mu       sync.Mutex
	listener net.Listener
	closed   bool
	sem      chan struct{} // nil when MaxPendingAccepts == 0
}

// NewUDPFactory creates a new UDPFactory.
func NewUDPFactory() *UDPFactory {
	return &UDPFactory{}
}

// Listen starts listening on the given address.
func (f *UDPFactory) Listen(address string) error {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.listener = ln
	if f.MaxPendingAccepts > 0 {
		f.sem = make(chan struct{}, f.MaxPendingAccepts)
	}
	f.mu.Unlock()

	go f.acceptLoop(ln)
	return nil
}

// Connect dials the given address and returns a Connection.
func (f *UDPFactory) Connect(address string) (*Connection, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	return newConnection(conn, false), nil
}

// Close stops the factory and closes the listener.
func (f *UDPFactory) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return
	}
	f.closed = true
	if f.listener != nil {
		f.listener.Close() //nolint:errcheck,gosec
	}
}

func (f *UDPFactory) acceptLoop(ln net.Listener) {
	// f.sem is set once in Listen before this goroutine starts, so
	// reading it without the lock is safe.
	sem := f.sem
	for {
		// Block before Accept when the in-flight callback queue is full,
		// so the kernel listen backlog absorbs the burst rather than the
		// Node piling up handshake goroutines on a contended mutex.
		if sem != nil {
			sem <- struct{}{}
		}
		conn, err := ln.Accept()
		if err != nil {
			if sem != nil {
				<-sem
			}
			return
		}
		c := newConnection(conn, false)
		cb := f.AcceptedCallback
		if cb == nil {
			if sem != nil {
				<-sem
			}
			continue
		}
		go func() {
			defer func() {
				if sem != nil {
					<-sem
				}
			}()
			cb(c)
		}()
	}
}
