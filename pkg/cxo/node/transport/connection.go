package transport

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
)

// Connection wraps a net.Conn with channel-based message I/O.
// Messages are length-prefixed (4 bytes, big-endian).
type Connection struct {
	conn  net.Conn
	isTCP bool

	mu     sync.RWMutex
	closed bool

	in   chan []byte
	out  chan []byte
	done chan struct{}
}

func newConnection(conn net.Conn, isTCP bool) *Connection {
	c := &Connection{
		conn:  conn,
		isTCP: isTCP,
		in:    make(chan []byte, 256),
		out:   make(chan []byte, 256),
		done:  make(chan struct{}),
	}
	go c.readLoop()
	go c.writeLoop()
	return c
}

// GetRemoteAddr returns the remote address of the connection.
func (c *Connection) GetRemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// IsTCP returns true if this is a TCP connection.
func (c *Connection) IsTCP() bool {
	return c.isTCP
}

// IsClosed returns true if the connection has been closed.
func (c *Connection) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// Close closes the connection and its channels.
func (c *Connection) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	c.conn.Close() //nolint:errcheck,gosec
	close(c.done)
}

// GetChanOut returns the send channel.
func (c *Connection) GetChanOut() chan<- []byte {
	return c.out
}

// GetChanIn returns the receive channel.
func (c *Connection) GetChanIn() <-chan []byte {
	return c.in
}

func (c *Connection) readLoop() {
	defer close(c.in)

	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(c.conn, header); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(header)
		if length == 0 || length > 64*1024*1024 { // 64MB max message size
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.conn, payload); err != nil {
			return
		}

		select {
		case c.in <- payload:
		case <-c.done:
			return
		}
	}
}

func (c *Connection) writeLoop() {
	header := make([]byte, 4)
	for {
		select {
		case data, ok := <-c.out:
			if !ok {
				return
			}
			binary.BigEndian.PutUint32(header, uint32(len(data))) //nolint:gosec
			if _, err := c.conn.Write(header); err != nil {
				return
			}
			if _, err := c.conn.Write(data); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
