//go:build !tinygo

// Package dmsg pkg/dmsg/dmsg/ws_server.go c1-net-dmsg
//
// Split out of ws.go behind //go:build !tinygo: ServeWS runs an http.Server
// (net/http), which is broken on the TinyGo js target. The CLIENT dial path
// (dialSessionWS) stays in ws.go untagged — it uses only coder/websocket, which
// compiles to js/wasm, so the WASM dmsg client can still dial WS servers.
package dmsg

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// wsPath is the HTTP path the dmsg WebSocket endpoint is served on. The
// advertised Server.AddressWS URL includes it (e.g. "wss://host/dmsg") so the
// operator can front the endpoint with a reverse proxy / CDN that routes only
// this path to the dmsg server.
const wsPath = "/dmsg"

// ServeWS serves dmsg over WebSocket on lis and advertises advertisedWSURL in
// discovery (Server.AddressWS). It runs alongside the TCP Serve and the QUIC
// ServeQUIC: WS-capable clients (chiefly the js/wasm build) dial the WS URL,
// everyone else stays on TCP/QUIC. lis is a plaintext HTTP listener — TLS for
// wss:// is expected to be terminated by a front proxy, with the dmsg Noise
// layer providing the actual end-to-end PK authentication regardless. Blocks
// until the listener errors or the server closes.
func (s *Server) ServeWS(lis net.Listener, advertisedWSURL string) error {
	s.setAdvertisedWSAddr(advertisedWSURL)

	mux := http.NewServeMux()
	mux.HandleFunc(wsPath, s.handleWS)
	httpSrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Tear the HTTP server down when the dmsg server closes so ServeWS returns.
	go func() {
		<-s.done
		_ = httpSrv.Close() //nolint:errcheck
	}()

	s.log.WithField("addr_ws", advertisedWSURL).Info("Serving dmsg over WebSocket.")
	err := httpSrv.Serve(lis)
	if isClosed(s.done) {
		return nil
	}
	return err
}

// handleWS upgrades an inbound HTTP request to a WebSocket and serves it as a
// normal dmsg server session. websocket.NetConn yields a net.Conn, so the
// connection flows through the shared handleSession path (Noise handshake +
// yamux) exactly like an accepted TCP conn. handleSession blocks until the
// session closes, which keeps this handler — and thus the WS conn — alive for
// the session's lifetime.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// dmsg clients are not browsers making cross-origin fetches; the Noise
		// handshake authenticates the peer, so origin is not a trust boundary
		// here. Skipping the check lets a WASM client hosted on any origin
		// connect.
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.log.WithError(err).Debug("dmsg-ws: websocket upgrade failed")
		return
	}
	// MessageBinary: dmsg frames are raw bytes, not UTF-8 text. context.Background
	// bounds the conn to its own lifetime (not the HTTP request's), so a read/
	// write does not get canceled when net/http thinks the handler is "done".
	conn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
	defer conn.Close() //nolint:errcheck,gosec

	if s.SessionCount() >= s.maxSessions {
		s.log.WithField("max_sessions", s.maxSessions).
			Debug("dmsg-ws: max sessions reached, still accepting for delegated listeners.")
	}
	s.handleSession(conn)
}
