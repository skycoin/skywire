//go:build !js

// Package pty pkg/pty/ui_socket.go c2-app-pty
package pty

import (
	"context"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/sirupsen/logrus"
)

// uiSocket is the terminal UI's websocket. Natively it is coder/websocket;
// under js that library is the browser's client and cannot accept, so the
// browser build uses x/net/websocket (ui_socket_js.go).
type uiSocket struct {
	ws *websocket.Conn
	nc net.Conn
}

// acceptUISocket upgrades the request and runs serve on the socket.
func acceptUISocket(w http.ResponseWriter, r *http.Request, log logrus.FieldLogger, serve func(*uiSocket)) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.WithError(err).Warn("Failed to upgrade to websocket.")
		return
	}
	defer func() { log.WithError(ws.Close(websocket.StatusNormalClosure, "closed")).Debug("Closed ws.") }()
	serve(&uiSocket{ws: ws, nc: websocket.NetConn(r.Context(), ws, websocket.MessageBinary)})
}

// Conn writes binary messages.
func (s *uiSocket) Conn() net.Conn { return s.nc }

// Read returns the next message and whether it was text.
func (s *uiSocket) Read(ctx context.Context) (bool, []byte, error) {
	t, b, err := s.ws.Read(ctx)
	return t == websocket.MessageText, b, err
}

// WriteText sends one text message.
func (s *uiSocket) WriteText(ctx context.Context, b []byte) error {
	return s.ws.Write(ctx, websocket.MessageText, b)
}

// Ping checks the peer is alive.
func (s *uiSocket) Ping(ctx context.Context) error { return s.ws.Ping(ctx) }

// Close closes the socket as going away.
func (s *uiSocket) Close(reason string) error { return s.ws.Close(websocket.StatusGoingAway, reason) }
