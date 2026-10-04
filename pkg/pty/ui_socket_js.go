//go:build js

// Package pty pkg/pty/ui_socket_js.go c2-app-pty
package pty

import (
	"context"
	"net"
	"net/http"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/websocket"
)

// uiSocket is the terminal UI's websocket in a browser visor, served with
// x/net/websocket because coder/websocket under js cannot accept.
type uiSocket struct{ c *websocket.Conn }

// acceptUISocket upgrades the request and runs serve on the socket.
func acceptUISocket(w http.ResponseWriter, r *http.Request, log logrus.FieldLogger, serve func(*uiSocket)) {
	websocket.Server{
		// The page and this server share the tab, and the vnet bridge sends
		// the page's origin; the handshake is not a cross-site request.
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: func(c *websocket.Conn) {
			c.PayloadType = websocket.BinaryFrame
			defer func() { log.WithError(c.Close()).Debug("Closed ws.") }()
			serve(&uiSocket{c: c})
		},
	}.ServeHTTP(w, r)
}

// Conn writes binary messages.
func (s *uiSocket) Conn() net.Conn { return s.c }

// typedCodec reads one message and reports whether it was text.
var typedCodec = websocket.Codec{
	Marshal: func(v interface{}) ([]byte, byte, error) { return v.([]byte), websocket.TextFrame, nil },
	Unmarshal: func(data []byte, payloadType byte, v interface{}) error {
		m := v.(*typedMsg)
		m.text, m.data = payloadType == websocket.TextFrame, data
		return nil
	},
}

type typedMsg struct {
	text bool
	data []byte
}

// Read returns the next message and whether it was text.
func (s *uiSocket) Read(context.Context) (bool, []byte, error) {
	var m typedMsg
	err := typedCodec.Receive(s.c, &m)
	return m.text, m.data, err
}

// WriteText sends one text message.
func (s *uiSocket) WriteText(_ context.Context, b []byte) error { return typedCodec.Send(s.c, b) }

// Ping is a no-op: x/net/websocket cannot send a ping. The vnet bridge has
// no idle timeout to keep warm.
func (s *uiSocket) Ping(context.Context) error { return nil }

// Close closes the socket.
func (s *uiSocket) Close(string) error { return s.c.Close() }
