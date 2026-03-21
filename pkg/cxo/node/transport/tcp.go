package transport

import (
	"net"
	"sync"
)

// TCPFactory creates and manages TCP connections.
type TCPFactory struct {
	AcceptedCallback func(conn *Connection)

	mu       sync.Mutex
	listener net.Listener
	closed   bool
}

// NewTCPFactory creates a new TCPFactory.
func NewTCPFactory() *TCPFactory {
	return &TCPFactory{}
}

// Listen starts listening on the given address for incoming TCP connections.
func (f *TCPFactory) Listen(address string) error {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.listener = ln
	f.mu.Unlock()

	go f.acceptLoop(ln)
	return nil
}

// ListenerAddress returns the actual address the listener is bound to.
// Useful when listening on ":0" to get the OS-assigned port.
func (f *TCPFactory) ListenerAddress() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listener != nil {
		return f.listener.Addr().String()
	}
	return ""
}

// Connect dials a TCP address and returns a Connection.
func (f *TCPFactory) Connect(address string) (*Connection, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	return newConnection(conn, true), nil
}

// Close stops the factory and closes the listener.
func (f *TCPFactory) Close() {
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

func (f *TCPFactory) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		c := newConnection(conn, true)
		if cb := f.AcceptedCallback; cb != nil {
			go cb(c)
		}
	}
}
