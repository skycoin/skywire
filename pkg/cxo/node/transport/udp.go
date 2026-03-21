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

	mu       sync.Mutex
	listener net.Listener
	closed   bool
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
		f.listener.Close()
	}
}

func (f *UDPFactory) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		c := newConnection(conn, false)
		if cb := f.AcceptedCallback; cb != nil {
			go cb(c)
		}
	}
}
