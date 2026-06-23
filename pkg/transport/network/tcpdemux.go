//go:build !tinygo

// Package network pkg/transport/network/tcpdemux.go
//
// tcpDemux multiplexes ONE TCP listener across the TCP transport types — WS (an
// HTTP/1.1 WebSocket upgrade) and stcpr (a raw skywire handshake) — so a visor
// can listen for both on a single transport_port instead of binding a port each.
// See docs/design/transport-port-unification.md.
//
// It is a thin wrapper around the vendored soheilhy/cmux: cmux peeks each
// accepted connection's first bytes (replaying them to the matched protocol) and
// routes HTTP/1 to the WS listener, everything else to the stcpr listener. cmux
// is the established connection-mux library, so unlike the UDP side this needs no
// custom classifier. The TCP analog of udpDemux.
package network

import (
	"net"

	"github.com/soheilhy/cmux"
)

// tcpDemux fans one TCP listener out to a WS (HTTP) and an stcpr (raw) virtual
// net.Listener. The protocols' accept loops consume the virtual listeners
// unchanged. Closing the demux closes the master listener, which stops cmux and
// the virtual listeners.
type tcpDemux struct {
	master net.Listener
	ws     net.Listener
	stcpr  net.Listener
}

func newTCPDemux(master net.Listener) *tcpDemux {
	m := cmux.New(master)
	// Order matters: match the HTTP/1 (WebSocket-upgrade) connections first, then
	// everything else is the raw skywire stcpr handshake.
	ws := m.Match(cmux.HTTP1Fast())
	stcpr := m.Match(cmux.Any())
	d := &tcpDemux{master: master, ws: ws, stcpr: stcpr}
	go m.Serve() //nolint:errcheck // returns when the master listener closes
	return d
}

// WS returns the virtual listener carrying HTTP/1 (WebSocket-upgrade) connections.
func (d *tcpDemux) WS() net.Listener { return d.ws }

// STCPR returns the virtual listener carrying raw (non-HTTP) connections.
func (d *tcpDemux) STCPR() net.Listener { return d.stcpr }

// Close closes the master listener, stopping cmux and the virtual listeners.
func (d *tcpDemux) Close() error { return d.master.Close() }
