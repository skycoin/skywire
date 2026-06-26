//go:build tinygo && js && wasm

// Package dmsg pkg/dmsg/dmsg/ws_js_tinygo.go: dmsg-over-WebSocket CLIENT dial for
// the TinyGo browser target.
//
// coder/websocket (used by ws.go on every other target) pulls net/http through
// its Dial signature (*http.Response), and net/http is broken on the TinyGo js
// target. So here we dial the browser's native WebSocket directly via syscall/js
// and adapt it to a net.Conn — which then flows through the SAME
// makeClientSession + yamux path as coder/websocket's NetConn does elsewhere.
// No new session semantics, no new crypto; just a different way to obtain the
// byte pipe.
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall/js"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// dialSessionWS dials the dmsg server's WebSocket endpoint (Server.AddressWS)
// using the browser WebSocket API and builds a yamux+Noise client session over
// it — the TinyGo-browser analog of ws.go's coder/websocket dial path.
func (ce *Client) dialSessionWS(ctx context.Context, entry *disc.Entry) (ClientSession, error) {
	conn, err := dialWebSocketJS(ctx, entry.Server.AddressWS)
	if err != nil {
		return ClientSession{}, fmt.Errorf("ws(js): dial %q: %w", entry.Server.AddressWS, err)
	}

	dSes, err := makeClientSession(&ce.EntityCommon, ce.porter, conn, entry.Static)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	dSes.sm.yamux, err = yamux.Client(conn, YamuxConfig())
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	ce.log.Infof("ws stream session initial for %s", dSes.RemotePK().String())
	return dSes, nil
}

// wsAddr / wsTimeoutError moved to jsaddr_js.go (js && wasm) so the WT browser
// dial (wt_js_tinygo.go, all js/wasm) can share them on std-Go wasm too.

// wsConnJS adapts a browser WebSocket (binaryType=arraybuffer) to a net.Conn.
// Inbound frames are buffered by the message event handler; Read drains the
// buffer and blocks (respecting the read deadline) when it is empty. Write maps
// straight onto WebSocket.send. The connection is dialed already-open, so a send
// never races the CONNECTING state.
type wsConnJS struct {
	ws   js.Value
	addr wsAddr

	mu        sync.Mutex
	buf       []byte        // unread inbound bytes
	notify    chan struct{} // closed (and replaced) to wake blocked readers
	closed    bool
	closeErr  error
	rDeadline time.Time

	// handlers are retained for the conn's lifetime; releasing them while the
	// browser may still deliver a trailing close/error event would panic, so we
	// intentionally do not Release (bounded leak: a handful per session).
	handlers []js.Func
}

// dialWebSocketJS opens a browser WebSocket to url and returns once it is OPEN
// (or ctx is done / the socket errors first).
func dialWebSocketJS(ctx context.Context, url string) (*wsConnJS, error) {
	ctor := js.Global().Get("WebSocket")
	if !ctor.Truthy() {
		return nil, errors.New("no WebSocket constructor in this JS environment")
	}
	ws := ctor.New(url)
	ws.Set("binaryType", "arraybuffer")

	c := &wsConnJS{ws: ws, addr: wsAddr(url), notify: make(chan struct{})}

	openCh := make(chan struct{})
	var openOnce sync.Once
	var openErr error
	finishOpen := func(err error) {
		openOnce.Do(func() { openErr = err; close(openCh) })
	}

	onOpen := js.FuncOf(func(js.Value, []js.Value) interface{} {
		finishOpen(nil)
		return nil
	})
	onMsg := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		// data is an ArrayBuffer (binaryType=arraybuffer); wrap as Uint8Array.
		u8 := js.Global().Get("Uint8Array").New(args[0].Get("data"))
		b := make([]byte, u8.Length())
		js.CopyBytesToGo(b, u8)
		c.mu.Lock()
		c.buf = append(c.buf, b...)
		c.wake()
		c.mu.Unlock()
		return nil
	})
	onClose := js.FuncOf(func(js.Value, []js.Value) interface{} {
		c.fail(io.EOF)
		finishOpen(errors.New("ws closed before open"))
		return nil
	})
	onErr := js.FuncOf(func(js.Value, []js.Value) interface{} {
		c.fail(errors.New("ws: connection error"))
		finishOpen(errors.New("ws error before open"))
		return nil
	})
	c.handlers = []js.Func{onOpen, onMsg, onClose, onErr}
	ws.Call("addEventListener", "open", onOpen)
	ws.Call("addEventListener", "message", onMsg)
	ws.Call("addEventListener", "close", onClose)
	ws.Call("addEventListener", "error", onErr)

	select {
	case <-openCh:
		if openErr != nil {
			c.Close() //nolint:errcheck
			return nil, openErr
		}
		return c, nil
	case <-ctx.Done():
		c.Close() //nolint:errcheck
		return nil, ctx.Err()
	}
}

// wake broadcasts to blocked readers. Caller must hold c.mu.
func (c *wsConnJS) wake() {
	close(c.notify)
	c.notify = make(chan struct{})
}

// fail marks the conn closed with err and wakes readers.
func (c *wsConnJS) fail(err error) {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.closeErr = err
		c.wake()
	}
	c.mu.Unlock()
}

// Read drains buffered inbound bytes, blocking until data arrives, the read
// deadline elapses, or the conn closes.
func (c *wsConnJS) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.buf) > 0 {
			n := copy(p, c.buf)
			c.buf = c.buf[n:]
			c.mu.Unlock()
			return n, nil
		}
		if c.closed {
			err := c.closeErr
			c.mu.Unlock()
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
		notify := c.notify
		deadline := c.rDeadline
		c.mu.Unlock()

		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, wsTimeoutError{}
			}
			t := time.NewTimer(d)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case <-notify:
		case <-timeout:
			return 0, wsTimeoutError{}
		}
	}
}

// Write sends p as a single binary WebSocket frame.
func (c *wsConnJS) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		if err == nil {
			err = net.ErrClosed
		}
		return 0, err
	}
	c.mu.Unlock()

	u8 := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(u8, p)
	c.ws.Call("send", u8)
	return len(p), nil
}

// Close closes the WebSocket and unblocks any pending Read.
func (c *wsConnJS) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.closeErr == nil {
		c.closeErr = net.ErrClosed
	}
	c.wake()
	c.mu.Unlock()
	c.ws.Call("close")
	return nil
}

func (c *wsConnJS) LocalAddr() net.Addr  { return c.addr }
func (c *wsConnJS) RemoteAddr() net.Addr { return c.addr }

// SetReadDeadline sets the deadline honored by Read; a blocked Read re-evaluates
// it immediately.
func (c *wsConnJS) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	c.wake()
	c.mu.Unlock()
	return nil
}

// SetWriteDeadline is a no-op: WebSocket.send does not block, so there is no
// write to time out.
func (c *wsConnJS) SetWriteDeadline(time.Time) error { return nil }

// SetDeadline sets the read deadline (write has none).
func (c *wsConnJS) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

// DialWebSocketJS dials a browser-native WebSocket to url and returns it as a
// net.Conn. It is the TinyGo/js WebSocket dialer, exported so the WS transport
// carrier (pkg/transport/network) can reuse the same tested net.Conn adapter a
// browser visor uses to reach a dmsg server — here to reach a peer visor's WS
// transport endpoint.
func DialWebSocketJS(ctx context.Context, url string) (net.Conn, error) {
	return dialWebSocketJS(ctx, url)
}
