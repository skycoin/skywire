//go:build !tinygo

// Package network pkg/transport/network/ws_native.go c2-net-transport
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

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// resolveWSURLViaAR resolves a peer's WS endpoint from the address resolver when
// there is no explicit table entry (a native `tp add -t ws`). WS shares the
// stcpr cmux TCP port, so the peer's stcpr AR record (host:port) IS its WS
// endpoint — wrap it as ws://host:port/. ok=false when there is no AR client or
// no stcpr record for the peer.
func (c *wsClient) resolveWSURLViaAR(ctx context.Context, rPK cipher.PubKey) (string, bool) {
	ar, _ := c.ar.(addrresolver.APIClient)
	if ar == nil {
		return "", false
	}
	vd, err := ar.Resolve(ctx, string(types.STCPR), rPK)
	if err != nil {
		return "", false
	}
	// RemoteAddr may be a bare host with the port carried separately in
	// VisorData.Port (same shape the stcpr dialer handles via canonicalAddr) —
	// "ws://"+RemoteAddr would otherwise drop the port and dial 80/443.
	addr := canonicalAddr(vd.RemoteAddr, vd.Port)
	if addr == "" {
		return "", false
	}
	return "ws://" + addr + "/", true
}

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
	var lis *wsListener
	if c.sharedListener != nil {
		// Unified transport port: run the WS HTTP server over the shared listener's
		// HTTP virtual listener instead of binding our own.
		lis = newWSListenerOver(c.sharedListener)
	} else {
		var err error
		lis, err = newWSListener(c.listenAddr)
		if err != nil {
			c.log.Errorf("Failed to start WS listener on %q: %v", c.listenAddr, err)
			return
		}
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
	return newWSListenerOver(tcpLis), nil
}

// newWSListenerOver runs the WebSocket HTTP server over an already-bound TCP
// listener (e.g. the HTTP virtual listener of a unified transport_port demux),
// rather than binding its own.
func newWSListenerOver(tcpLis net.Listener) *wsListener {
	l := &wsListener{
		addr:  tcpLis.Addr(),
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", l.handle)
	l.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go l.srv.Serve(tcpLis) //nolint:errcheck
	return l
}

func (l *wsListener) handle(w http.ResponseWriter, r *http.Request) {
	// InsecureSkipVerify disables coder/websocket's same-origin (CSRF) check.
	// A browser (wasm) visor ALWAYS sends an Origin header (its page origin),
	// which never matches this visor's Host, so the default check 403s every
	// browser-originated WS dial — the whole point of the WS carrier. The check
	// is meaningless here anyway: the WS connection carries a skywire transport
	// authenticated end-to-end by the Noise handshake (remote-PK verification),
	// and there is no cookie / ambient session to protect against CSRF. A forged
	// Origin buys nothing without the peer's keys.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
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
