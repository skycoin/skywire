//go:build !tinygo

// Package dmsg pkg/dmsg/dmsg/serve_unified.go: serve raw-dmsg AND dmsg-over-
// WebSocket on ONE listener / advertised ip:port.
package dmsg

import (
	"net"

	"github.com/soheilhy/cmux"
)

// ServeWithWS serves BOTH raw-dmsg and dmsg-over-WebSocket on a SINGLE listener,
// so one advertised ip:port carries both protocols — no separate WS port. It
// demultiplexes by first bytes: a WebSocket client opens with an HTTP/1 upgrade
// (cmux.HTTP1Fast → ServeWS), a native dmsg client opens with the Noise handshake
// (cmux.Any → Serve). They are trivially distinguishable because the WS handshake
// is ASCII HTTP and the dmsg handshake is binary Noise.
//
// The server's discovery entry then advertises Server.Address (TCP) AND
// Server.AddressWS (the SAME host:port, ws://host:port/dmsg) — no new port. So
// every server running this becomes reachable by a browser at ws://<address>/dmsg
// with no topology change: clients just dial the advertised address over whatever
// protocol they speak. (QUIC + WebTransport share the matching UDP port the same
// way — see the WT/QUIC listeners.)
//
// advertisedAddr is registered as Server.Address; advertisedWSURL (same host:port,
// ending in /dmsg) as Server.AddressWS. Blocks until a sub-server returns.
func (s *Server) ServeWithWS(lis net.Listener, advertisedAddr, advertisedWSURL string) error {
	// Set the WS address BEFORE Serve starts its self-registration loop, so the
	// first published entry already carries AddressWS alongside Address (both on
	// this one port). ServeWS sets it again to the same value — harmless.
	s.advertisedWSAddr = advertisedWSURL

	m := cmux.New(lis)
	httpL := m.Match(cmux.HTTP1Fast()) // the WS upgrade is an HTTP/1 request
	rawL := m.Match(cmux.Any())        // everything else is raw dmsg (Noise)

	errCh := make(chan error, 3)
	go func() { errCh <- s.Serve(rawL, advertisedAddr) }()
	go func() { errCh <- s.ServeWS(httpL, advertisedWSURL) }()
	go func() { errCh <- m.Serve() }()
	return <-errCh
}
