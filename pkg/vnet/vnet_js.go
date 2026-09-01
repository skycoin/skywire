//go:build js && wasm

// Package vnet pkg/vnet/vnet_js.go c0-com-net
//
// js/wasm implementation over the page's virtual loopback network
// (pkg/wasmhv/browseui/vnet.js). Loopback addresses go through the page port
// table so SEPARATE wasm instances — the visor running in one terminal, the
// CLI in another, the nested browser fetching the hypervisor UI — share one
// localhost, exactly as processes on a host do. Non-loopback addresses, and
// pages without vnet.js, fall back to net.* (Go's js runtime simulates a
// PER-INSTANCE loopback, which still serves single-instance callers).
package vnet

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"
)

func vnetJS() js.Value { return js.Global().Get("vnet") }

// loopbackPort extracts the port when address is loopback ("", "localhost",
// "127.0.0.1", "::1" hosts); ok=false means "not ours — use net".
func loopbackPort(address string) (int, bool) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return 0, false
	}
	switch host {
	case "", "localhost", "127.0.0.1", "::1", "0.0.0.0", "::":
	default:
		return 0, false
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

// Listen binds a virtual loopback port when the page provides vnet and the
// address is loopback; otherwise net.Listen.
func Listen(network, address string) (net.Listener, error) {
	v := vnetJS()
	port, ok := loopbackPort(address)
	if !v.Truthy() || !ok || !strings.HasPrefix(network, "tcp") {
		return net.Listen(network, address)
	}

	l := &listener{port: port, accept: make(chan int, 16), done: make(chan struct{})}
	l.onConn = js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		select {
		case l.accept <- args[0].Int():
		case <-l.done:
		}
		return nil
	})
	if !v.Call("listen", port, l.onConn).Bool() {
		l.onConn.Release()
		return nil, fmt.Errorf("vnet: listen tcp 127.0.0.1:%d: address already in use", port)
	}
	return l, nil
}

// DialTimeout connects to a virtual loopback port when the page provides
// vnet and the address is loopback; otherwise net.DialTimeout.
func DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	v := vnetJS()
	port, ok := loopbackPort(address)
	if !v.Truthy() || !ok || !strings.HasPrefix(network, "tcp") {
		return net.DialTimeout(network, address, timeout)
	}
	id := v.Call("dial", port).Int()
	if id < 0 {
		return nil, fmt.Errorf("vnet: dial tcp 127.0.0.1:%d: connection refused", port)
	}
	return newConn(id, "a", port), nil
}

type vnetAddr struct{ port int }

func (a vnetAddr) Network() string { return "tcp" }
func (a vnetAddr) String() string  { return fmt.Sprintf("127.0.0.1:%d", a.port) }

type listener struct {
	port      int
	accept    chan int
	done      chan struct{}
	closeOnce sync.Once
	onConn    js.Func
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case id := <-l.accept:
		return newConn(id, "b", l.port), nil
	case <-l.done:
		return nil, errors.New("vnet: use of closed listener")
	}
}

func (l *listener) Close() error {
	l.closeOnce.Do(func() {
		vnetJS().Call("unlisten", l.port)
		close(l.done)
		// onConn is retained: a stray late onconn callback into a released
		// js.Func would panic; a handful of leaked Funcs per listener
		// lifetime is the same bounded tradeoff ws_js takes.
	})
	return nil
}

func (l *listener) Addr() net.Addr { return vnetAddr{port: l.port} }

// conn adapts one side of a vnet pipe to net.Conn. The read path mirrors the
// wsConnJS pattern: pull buffered chunks, otherwise arm a one-shot readable
// callback and block (honoring the read deadline).
type conn struct {
	id   int
	side string
	port int

	mu        sync.Mutex
	leftover  []byte
	closed    bool
	rDeadline time.Time
	notify    chan struct{}
}

func newConn(id int, side string, port int) *conn {
	return &conn{id: id, side: side, port: port, notify: make(chan struct{}, 1)}
}

func (c *conn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.leftover) > 0 {
			n := copy(p, c.leftover)
			c.leftover = c.leftover[n:]
			c.mu.Unlock()
			return n, nil
		}
		if c.closed {
			c.mu.Unlock()
			return 0, net.ErrClosed
		}
		deadline := c.rDeadline
		c.mu.Unlock()

		v := vnetJS()
		chunk := v.Call("recv", c.id, c.side)
		if chunk.Truthy() {
			b := make([]byte, chunk.Get("length").Int())
			js.CopyBytesToGo(b, chunk)
			n := copy(p, b)
			if n < len(b) {
				c.mu.Lock()
				c.leftover = append(c.leftover, b[n:]...)
				c.mu.Unlock()
			}
			return n, nil
		}
		if v.Call("eof", c.id, c.side).Bool() {
			return 0, io.EOF
		}

		// Arm a one-shot wakeup, then wait (with deadline).
		armed := make(chan struct{}, 1)
		cb := js.FuncOf(func(js.Value, []js.Value) interface{} {
			select {
			case armed <- struct{}{}:
			default:
			}
			return nil
		})
		v.Call("onReadable", c.id, c.side, cb)

		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				cb.Release()
				return 0, timeoutError{}
			}
			t := time.NewTimer(d)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case <-armed:
		case <-timeout:
			cb.Release()
			return 0, timeoutError{}
		case <-c.notify:
			cb.Release()
			return 0, net.ErrClosed
		}
		cb.Release()
	}
}

func (c *conn) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	c.mu.Unlock()
	u8 := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(u8, p)
	if !vnetJS().Call("send", c.id, c.side, u8).Bool() {
		return 0, errors.New("vnet: broken pipe")
	}
	return len(p), nil
}

func (c *conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	select {
	case c.notify <- struct{}{}:
	default:
	}
	c.mu.Unlock()
	vnetJS().Call("close", c.id, c.side)
	return nil
}

func (c *conn) LocalAddr() net.Addr  { return vnetAddr{port: c.port} }
func (c *conn) RemoteAddr() net.Addr { return vnetAddr{port: c.port} }

func (c *conn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	c.mu.Unlock()
	return nil
}
func (c *conn) SetWriteDeadline(time.Time) error { return nil }
func (c *conn) SetDeadline(t time.Time) error    { return c.SetReadDeadline(t) }

type timeoutError struct{}

func (timeoutError) Error() string   { return "vnet: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
