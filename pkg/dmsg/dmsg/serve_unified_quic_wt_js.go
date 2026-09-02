//go:build js && wasm

// Package dmsg pkg/dmsg/dmsg/serve_unified_quic_wt_js.go c1-net-dmsg
//
// js/wasm: the WebTransport SERVER side (webtransport-go + http3) does not
// build for the browser target, so the unified QUIC+WT listener degrades to
// plain dmsg-over-QUIC. A browser can't accept raw UDP sockets anyway — this
// exists so packages that CALL ServeUnifiedQUIC (the server API plumbing)
// compile in the wasm build of the full binary; nothing reaches it at runtime.
package dmsg

import (
	"crypto/tls"
	"net"
)

// ServeUnifiedQUIC on js/wasm serves QUIC only; advertisedWTURL is ignored.
func (s *Server) ServeUnifiedQUIC(udpConn net.PacketConn, advertisedUDPAddr, advertisedWTURL string) error {
	if advertisedWTURL != "" {
		s.log.Warn("dmsg-wt: WebTransport serving is unavailable on js/wasm; serving QUIC only")
	}
	return s.ServeQUIC(udpConn, advertisedUDPAddr)
}

// ServeWebTransport is unavailable on js/wasm (no HTTP/3 server in the
// browser); it exists so callers compile and blocks until the server closes,
// like a listener that never accepts.
func (s *Server) ServeWebTransport(_ net.PacketConn, _ string, _ tls.Certificate, _ [32]byte) error {
	s.log.Warn("dmsg-wt: WebTransport serving is unavailable on js/wasm")
	<-s.done
	return nil
}
