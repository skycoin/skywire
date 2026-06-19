// Package dmsg pkg/dmsg/dmsg/ws.go: dmsg-over-WebSocket.
//
// A WebSocket connection is a bidirectional, ordered, reliable byte pipe over
// HTTP(S) — exactly what dmsg's session layer wants. coder/websocket's NetConn
// adapts it to a net.Conn, so a WS session reuses the EXISTING Noise+yamux
// stack (makeServerSession/makeClientSession + yamux) with no new session
// semantics, no new mux, and no new crypto — the per-hop Noise handshake and
// the end-to-end per-stream noise are both unchanged. WS is purely a different
// way to carry the same bytes.
//
// Why it matters: a browser tab (JS or Go-js/wasm) cannot open a raw TCP or
// UDP socket, so neither the legacy TCP transport nor dmsg-over-QUIC can reach
// the mesh from a browser. WebSocket is the one transport the browser sandbox
// allows, which makes a WASM dmsg *client* — a real PK-authenticated leaf node
// running in a web page — possible. It is also the universal fallback for
// restrictive networks that only permit HTTP(S)/443 egress and for CDN
// fronting. coder/websocket is used precisely because it also compiles to
// js/wasm, so the same dial path serves the future browser client.
package dmsg

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
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
	s.advertisedWSAddr = advertisedWSURL

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

// dialSessionWS dials a dmsg server's WebSocket endpoint (Server.AddressWS) and
// builds a yamux+Noise client session over it — the WS analog of the TCP dial
// path in dialSession. The returned session is fully set up (Noise handshake
// done, yamux client started); dialSession's shared store/serve logic handles
// the rest. Used when the client prefers WS (Config.PreferWS) — always the case
// for the js/wasm build, which cannot dial TCP/QUIC.
func (ce *Client) dialSessionWS(ctx context.Context, entry *disc.Entry) (ClientSession, error) {
	c, _, err := websocket.Dial(ctx, entry.Server.AddressWS, nil)
	if err != nil {
		return ClientSession{}, fmt.Errorf("ws: dial %q: %w", entry.Server.AddressWS, err)
	}
	conn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)

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
