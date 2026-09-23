//go:build !js

// Package wisp pkg/wisp/server_http.go c4-app-proxy
//
// Serving Wisp over a WebSocket, which is the transport the protocol is
// defined on and the one every browser Wisp client speaks.
//
// It is //go:build !js because websocket.Accept is: coder/websocket's js/wasm
// build wraps the browser's own WebSocket object, which can dial but cannot
// listen. A visor in a tab therefore serves Wisp with Server.ServeConn over a
// virtual-loopback conn instead — see frames.go.
package wisp

import (
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// ServeHTTP upgrades the request and runs one Wisp session on it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A v2 client announces itself with Sec-WebSocket-Protocol. Echo back
	// whatever it offered, since the spec keys the version off the header
	// being present rather than off any particular subprotocol name.
	offered := subprotocols(r)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       offered,
		InsecureSkipVerify: true, // a guest NIC dials from whatever origin the page has
	})
	if err != nil {
		s.cfg.Log.WithError(err).Debug("websocket upgrade failed")
		return
	}
	c.SetReadLimit(s.cfg.ReadLimit)

	s.ServeFrames(r.Context(), NewWebsocketFrames(c), len(offered) > 0)
}

func subprotocols(r *http.Request) []string {
	raw := r.Header.Get("Sec-WebSocket-Protocol")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
