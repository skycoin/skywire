// Package wisp pkg/wisp/frames.go c4-app-proxy
//
// The transport a Wisp session runs on.
//
// Wisp is defined over a WebSocket, but nothing in a session needs one: it
// reads a frame, writes a frame, and closes. Naming that as an interface lets
// the same server and client run over anything that preserves message
// boundaries — which is what makes Wisp usable inside the wasm visor, where a
// WebSocket is not available in either direction:
//
//   - websocket.Accept is //go:build !js, so a Wisp SERVER cannot exist on
//     js/wasm at all;
//   - a service worker cannot intercept ws:// or wss://, so a page-served
//     endpoint could not be reached even if it could exist.
//
// A virtual-loopback conn (bottle's vnet) has neither problem, so streamFrames
// below carries a session over any net.Conn by length-prefixing each frame.
//
// Endianness: the prefix is little-endian, matching every multi-byte field in
// Wisp itself. The one big-endian length in this package is the DNS-over-TCP
// framing in egress.go, which is network order per RFC 1035 and is called out
// there for exactly this reason.
package wisp

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Frames is a message transport: one Write is one frame, one Read is one
// frame. It is what a Wisp session actually requires of its socket.
type Frames interface {
	// ReadFrame returns the next frame. The slice is the caller's.
	ReadFrame(ctx context.Context) ([]byte, error)
	// WriteFrame sends one frame.
	WriteFrame(ctx context.Context, b []byte) error
	// Close releases the transport. It is safe to call more than once.
	Close() error
}

// frameLenBytes is the size of streamFrames' length prefix.
const frameLenBytes = 4

// wsFrames carries a session over a WebSocket.
type wsFrames struct {
	conn *websocket.Conn
}

// NewWebsocketFrames adapts a WebSocket to Frames. The conn's read limit
// should already be set by the caller.
func NewWebsocketFrames(conn *websocket.Conn) Frames { return &wsFrames{conn: conn} }

// ReadFrame implements Frames, skipping text frames: Wisp is binary, and a
// well-meaning proxy or debug tool injecting a text frame should not end a
// session.
func (w *wsFrames) ReadFrame(ctx context.Context) ([]byte, error) {
	for {
		typ, data, err := w.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if typ != websocket.MessageBinary {
			continue
		}
		return data, nil
	}
}

// WriteFrame implements Frames.
func (w *wsFrames) WriteFrame(ctx context.Context, b []byte) error {
	return w.conn.Write(ctx, websocket.MessageBinary, b)
}

// Close implements Frames.
func (w *wsFrames) Close() error { return w.conn.CloseNow() }

// streamFrames carries a session over a byte stream by length-prefixing each
// frame, so a net.Conn — a vnet loopback conn, a TCP conn, a skywire stream —
// keeps the message boundaries Wisp depends on.
type streamFrames struct {
	conn  net.Conn
	r     *bufio.Reader
	limit int64

	wmu sync.Mutex

	closeOnce sync.Once
}

// NewStreamFrames adapts a byte stream to Frames. readLimit bounds one frame;
// zero means DefaultReadLimit.
func NewStreamFrames(conn net.Conn, readLimit int64) Frames {
	if readLimit <= 0 {
		readLimit = DefaultReadLimit
	}
	return &streamFrames{
		conn:  conn,
		r:     bufio.NewReader(conn),
		limit: readLimit,
	}
}

// ReadFrame implements Frames.
//
// The context bounds the read through the conn's deadline rather than by
// abandoning it: a frame half-read off a byte stream cannot be left behind
// without desynchronizing everything after it.
func (s *streamFrames) ReadFrame(ctx context.Context) ([]byte, error) {
	if err := s.applyDeadline(ctx, s.conn.SetReadDeadline); err != nil {
		return nil, err
	}

	var hdr [frameLenBytes]byte
	if _, err := io.ReadFull(s.r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if int64(n) > s.limit {
		// Past this point the stream cannot be resynchronized, so the
		// session has to end rather than skip the frame.
		return nil, fmt.Errorf("wisp: frame of %d bytes exceeds the %d-byte read limit", n, s.limit)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(s.r, b); err != nil {
		return nil, err
	}
	return b, nil
}

// WriteFrame implements Frames. The prefix and the payload go out under one
// lock and in one write, so two goroutines cannot interleave halves of a
// frame — the session has a single writer, but nothing in the type should
// depend on that.
func (s *streamFrames) WriteFrame(ctx context.Context, b []byte) error {
	if int64(len(b)) > s.limit {
		return fmt.Errorf("wisp: frame of %d bytes exceeds the %d-byte limit", len(b), s.limit)
	}

	out := make([]byte, frameLenBytes+len(b))
	binary.LittleEndian.PutUint32(out[:frameLenBytes], uint32(len(b))) //nolint:gosec // bounded by the limit just above
	copy(out[frameLenBytes:], b)

	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := s.applyDeadline(ctx, s.conn.SetWriteDeadline); err != nil {
		return err
	}
	_, err := s.conn.Write(out)
	return err
}

// Close implements Frames.
func (s *streamFrames) Close() error {
	var err error
	s.closeOnce.Do(func() { err = s.conn.Close() })
	return err
}

// applyDeadline pushes a context deadline onto the conn, and clears the
// deadline when the context has none.
func (s *streamFrames) applyDeadline(ctx context.Context, set func(time.Time) error) error {
	if dl, ok := ctx.Deadline(); ok {
		return set(dl)
	}
	return set(time.Time{})
}
