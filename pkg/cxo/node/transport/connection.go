// Package transport pkg/cxo/node/transport/connection.go c2-net-cxo
package transport

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
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

// maxFrameSize bounds an inbound frame.
const maxFrameSize = 64 * 1024 * 1024

// ErrInterrupted is returned by ReadFrame after Interrupt or Close.
var ErrInterrupted = errors.New("cxo transport: read interrupted")

// Connection wraps a net.Conn with length-prefixed (4 bytes, big-endian)
// message I/O. The owner reads frames with ReadFrame. Outbound frames go
// through a queue that a writer goroutine drains only while it is non-empty,
// so an idle connection holds no goroutine of its own.
type Connection struct {
	conn  net.Conn
	isTCP bool

	mu          sync.RWMutex
	closed      bool
	interrupted bool

	rmu     sync.Mutex // one ReadFrame at a time keeps frames whole
	out     chan Frame
	writing atomic.Bool
	done    chan struct{}
}

func newConnection(conn net.Conn, isTCP bool) *Connection {
	return &Connection{
		conn:  conn,
		isTCP: isTCP,
		out:   make(chan Frame, 256),
		done:  make(chan struct{}),
	}
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

// Close closes the connection.
func (c *Connection) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	// Force-unblock a read parked in io.ReadFull BEFORE closing. When the
	// remote peer vanishes without a FIN/RST (the common dmsg half-dead-stream
	// case), net.Conn.Close() does NOT reliably interrupt an in-flight Read,
	// only a read deadline does. Without this the owning CXO Conn.run never
	// returns, which once leaked tens of thousands of goroutines and multi-GB
	// RSS on the transport-discovery CXO node. Under mu so a ReadFrame timeout
	// cannot clear it.
	c.conn.SetReadDeadline(time.Now()) //nolint:errcheck,gosec
	c.mu.Unlock()
	c.conn.Close() //nolint:errcheck,gosec
	close(c.done)
}

// Interrupt makes a blocked or later ReadFrame return ErrInterrupted while
// leaving queued writes to drain.
func (c *Connection) Interrupt() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.interrupted {
		return
	}
	c.interrupted = true
	c.conn.SetReadDeadline(time.Now()) //nolint:errcheck,gosec
}

func (c *Connection) setReadDeadline(t time.Time) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.interrupted {
		return ErrInterrupted
	}
	return c.conn.SetReadDeadline(t)
}

// ReadFrame reads one frame. A timeout above zero bounds the read and returns
// an error satisfying os.IsTimeout when it passes. A failed read closes the
// connection, since a partly read frame leaves the stream unusable, except
// after Interrupt, which leaves writes to the owner.
func (c *Connection) ReadFrame(timeout time.Duration) ([]byte, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	b, err := c.readFrame(timeout)
	if err != nil {
		c.mu.RLock()
		stopped := c.closed || c.interrupted
		c.mu.RUnlock()
		if stopped {
			return nil, ErrInterrupted
		}
		c.Close()
	}
	return b, err
}

func (c *Connection) readFrame(timeout time.Duration) ([]byte, error) {
	if timeout > 0 {
		if err := c.setReadDeadline(time.Now().Add(timeout)); err != nil {
			return nil, err
		}
		defer c.setReadDeadline(time.Time{}) //nolint:errcheck
	}
	var header [4]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxFrameSize {
		return nil, errors.New("cxo transport: invalid frame length")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.conn, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// GetChanOut returns the send queue. Call Kick after each successful send.
func (c *Connection) GetChanOut() chan<- Frame {
	return c.out
}

// Kick starts the writer unless one is running.
func (c *Connection) Kick() {
	if c.writing.CompareAndSwap(false, true) {
		go c.writeLoop()
	}
}

func (c *Connection) writeLoop() {
	for {
		select {
		case f := <-c.out:
			if err := c.writeFrame(f); err != nil {
				// writing stays set, so no new writer starts on a dead conn.
				c.Close()
				return
			}
		case <-c.done:
			return
		default:
			c.writing.Store(false)
			// A frame queued after the empty check but before the Store saw
			// writing set and did not start a writer: take it over.
			if len(c.out) == 0 || !c.writing.CompareAndSwap(false, true) {
				return
			}
		}
	}
}

// writeFrame writes one small prefix (4-byte length + Head) then the Body,
// from its own (possibly shared) backing array with no copy. Wire bytes are
// identical to the old [length][Head||Body] form.
func (c *Connection) writeFrame(f Frame) error {
	prefix := make([]byte, 4+len(f.Head))
	binary.BigEndian.PutUint32(prefix, uint32(len(f.Head)+len(f.Body))) //nolint:gosec
	copy(prefix[4:], f.Head)
	if _, err := c.conn.Write(prefix); err != nil {
		return err
	}
	if len(f.Body) > 0 {
		if _, err := c.conn.Write(f.Body); err != nil {
			return err
		}
	}
	return nil
}
