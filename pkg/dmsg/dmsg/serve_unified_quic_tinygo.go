//go:build tinygo

// Package dmsg pkg/dmsg/dmsg/serve_unified_quic_tinygo.go c1-net-dmsg
//
// TinyGo builds carry no QUIC server (quic_native.go is !tinygo), so the
// server's QUIC and WebTransport entry points exist only so callers compile —
// a TinyGo wasm page that links the server API (the apt-repo config
// generator, via autoconfigcmd) never serves dmsg.
package dmsg

import (
	"crypto/tls"
	"errors"
	"net"
)

// errNoQUICServer is returned by the QUIC and WebTransport entry points in a
// TinyGo build.
var errNoQUICServer = errors.New("dmsg: no QUIC server in a TinyGo build")

// ServeUnifiedQUIC is unavailable in a TinyGo build.
func (s *Server) ServeUnifiedQUIC(_ net.PacketConn, _, _ string) error {
	return errNoQUICServer
}

// ServeWebTransport is unavailable in a TinyGo build.
func (s *Server) ServeWebTransport(_ net.PacketConn, _ string, _ tls.Certificate, _ [32]byte) error {
	return errNoQUICServer
}
