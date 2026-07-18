// Package transport pkg/cxo/node/transport/connection.go c2-net-cxo
package transport

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"
)

// Frame is one outbound message split into a small per-message Head (the
// 8-byte seq/rseq the CXO Conn prepends) and a Body (the encoded message).
// Splitting them lets a broadcast encode the (potentially large) Body ONCE and
// share that immutable []byte across every subscriber connection — each conn
// supplies only its own tiny Head. On the wire the frame is identical to the
// old single-buffer form: [4-byte length][Head][Body]. Sharing the Body is what
// turns a fan-out of an N-MB feed to M subscribers from O(N*M) memory + encode
// CPU (one encoded+buffered copy per conn) into O(N + M). See
// docs — the transport-discovery CXO node ballooned to multi-GB RSS re-encoding
// the all-transports feed once per subscriber.
type Frame struct {
	Head []byte
	Body []byte
}

// Connection wraps a net.Conn with channel-based message I/O.
// Messages are length-prefixed (4 bytes, big-endian).
type Connection struct {
	conn  net.Conn
	isTCP bool

	mu     sync.RWMutex
	closed bool

	in   chan []byte
	out  chan Frame
	done chan struct{}
}

func newConnection(conn net.Conn, isTCP bool) *Connection {
	c := &Connection{
		conn:  conn,
		isTCP: isTCP,
		in:    make(chan []byte, 256),
		out:   make(chan Frame, 256),
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

	// Force-unblock any read parked in readLoop's io.ReadFull BEFORE closing.
	// When the remote peer vanishes without a FIN/RST (the common dmsg
	// half-dead-stream case), the underlying Read never returns an error and
	// net.Conn.Close() does NOT reliably interrupt an in-flight Read — only a
	// read deadline does (dmsg honors SetReadDeadline even when Close cannot
	// wake the reader). Without this, readLoop never returns, its deferred
	// close(c.in) never runs, the owning CXO Conn.receiveMsg stays blocked
	// waiting on c.in, and Conn.run leaks forever on await.Wait(). That leak
	// accumulated tens of thousands of goroutines and multi-GB RSS on the
	// transport-discovery CXO node despite the idle watchdog correctly calling
	// Close() — the Close just never reached the stuck reader.
	c.conn.SetReadDeadline(time.Now()) //nolint:errcheck,gosec
	c.conn.Close()                     //nolint:errcheck,gosec
	close(c.done)
}

// GetChanOut returns the send channel.
func (c *Connection) GetChanOut() chan<- Frame {
	return c.out
}

// GetChanIn returns the receive channel.
func (c *Connection) GetChanIn() <-chan []byte {
	return c.in
}

func (c *Connection) readLoop() {
	// On any exit (read error, peer drop, size violation) tear down
	// the whole Connection so the paired writeLoop's `<-c.done`
	// select unblocks. Without this, a passive remote-side drop
	// leaves writeLoop parked in the select forever — Close is
	// only called from upper layers, and not every code path that
	// loses a peer reaches one. Close is idempotent so the paired
	// defer in writeLoop is safe.
	defer c.Close()
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
	// Mirrors readLoop's defer: tear down the Connection on any
	// exit so the paired readLoop's io.ReadFull unblocks (via the
	// underlying conn.Close inside Close). Close is idempotent.
	defer c.Close()

	for {
		select {
		case f, ok := <-c.out:
			if !ok {
				return
			}
			// One small prefix write (4-byte length + Head) then the Body.
			// The Body is written from its own (possibly shared) backing
			// array with no copy — the whole point of the Frame split. Wire
			// bytes are identical to the old [length][Head||Body] form.
			prefix := make([]byte, 4+len(f.Head))
			binary.BigEndian.PutUint32(prefix, uint32(len(f.Head)+len(f.Body))) //nolint:gosec
			copy(prefix[4:], f.Head)
			if _, err := c.conn.Write(prefix); err != nil {
				return
			}
			if len(f.Body) > 0 {
				if _, err := c.conn.Write(f.Body); err != nil {
					return
				}
			}
		case <-c.done:
			return
		}
	}
}
