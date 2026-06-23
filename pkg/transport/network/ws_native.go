//go:build !tinygo

// Package network pkg/transport/network/ws_native.go
//
// Native WS-transport carrier: dial via coder/websocket, and accept by running
// a WebSocket HTTP server fronted as a net.Listener so the shared
// acceptTransports loop can drain it. A browser visor has neither (it can't run
// a server, and coder/websocket pulls net/http) — see ws_tinygo.go.
package network

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// wsDial opens a direct WebSocket to url and adapts it to a net.Conn.
func wsDial(ctx context.Context, url string) (net.Conn, error) {
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	// Background context: the per-read/write deadlines are managed by the
	// transport layer over the returned net.Conn, not this lifetime ctx.
	return websocket.NetConn(context.Background(), c, websocket.MessageBinary), nil
}

// Start implements Client: serve incoming WS transports.
func (c *wsClient) Start() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	go c.serve()
	return nil
}

func (c *wsClient) serve() {
	lis, err := newWSListener(c.listenAddr)
	if err != nil {
		c.log.Errorf("Failed to start WS listener on %q: %v", c.listenAddr, err)
		return
	}
	c.acceptTransports(lis)
}

// wsListener fronts a WebSocket HTTP server as a net.Listener: each upgraded
// WebSocket connection is delivered as a net.Conn from Accept().
type wsListener struct {
	addr  net.Addr
	conns chan net.Conn
	done  chan struct{}
	srv   *http.Server
	once  sync.Once
}

func newWSListener(addr string) (*wsListener, error) {
	tcpLis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	l := &wsListener{
		addr:  tcpLis.Addr(),
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", l.handle)
	l.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go l.srv.Serve(tcpLis) //nolint:errcheck
	return l, nil
}

func (l *wsListener) handle(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	select {
	case l.conns <- conn:
	case <-l.done:
		_ = conn.Close() //nolint:errcheck
	}
}

// Accept implements net.Listener.
func (l *wsListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close implements net.Listener.
func (l *wsListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		_ = l.srv.Close() //nolint:errcheck
	})
	return nil
}

// Addr implements net.Listener.
func (l *wsListener) Addr() net.Addr { return l.addr }
