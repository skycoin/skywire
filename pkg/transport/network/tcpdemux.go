//go:build !tinygo

// Package network pkg/transport/network/tcpdemux.go c2-net-transport
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
//
// A visor running the in-process dmsg server on its own key takes a third
// branch here, so the server shares the visor's transport port rather than
// binding one of its own. That branch is only installed when something will
// consume it: an unconsumed cmux listener queues its matched connections and
// then blocks their goroutines.
package network

import (
	"net"

	"github.com/soheilhy/cmux"

	"github.com/skycoin/skywire/pkg/transport/network/handshake"
)

// tcpDemux fans one TCP listener out to a WS (HTTP) and an stcpr (raw) virtual
// net.Listener, plus a dmsg one when the co-resident dmsg server shares the
// port. The protocols' accept loops consume the virtual listeners unchanged.
// Closing the demux closes the master listener, which stops cmux and the
// virtual listeners.
type tcpDemux struct {
	master net.Listener
	ws     net.Listener
	stcpr  net.Listener
	dmsg   net.Listener // nil unless the in-process dmsg server shares this port
}

func newTCPDemux(master net.Listener, withDMSG bool) *tcpDemux {
	m := cmux.New(master)
	// Order matters: match the HTTP/1 (WebSocket-upgrade) connections first.
	ws := m.Match(cmux.HTTP1Fast())
	d := &tcpDemux{master: master, ws: ws}
	if !withDMSG {
		// Nothing else consumes this port: everything non-HTTP is the raw
		// skywire stcpr handshake, exactly as before the dmsg branch existed.
		d.stcpr = m.Match(cmux.Any())
		go m.Serve() //nolint:errcheck // returns when the master listener closes
		return d
	}
	// With the dmsg server sharing the port, stcpr can no longer be the
	// catch-all — but it does not need to be. An stcpr initiator's very first
	// bytes are the literal handshake.Message ("get_nonce"), so it is matched
	// positively and the catch-all is free for dmsg, whose sessions open with a
	// length-prefixed noise frame that no fixed prefix can match. Anything that
	// is neither lands on the dmsg branch and fails its handshake there, which
	// is what it did on the stcpr branch before.
	d.stcpr = m.Match(cmux.PrefixMatcher(handshake.Message))
	d.dmsg = m.Match(cmux.Any())
	go m.Serve() //nolint:errcheck // returns when the master listener closes
	return d
}

// WS returns the virtual listener carrying HTTP/1 (WebSocket-upgrade) connections.
func (d *tcpDemux) WS() net.Listener { return d.ws }

// STCPR returns the virtual listener carrying the raw skywire handshake.
func (d *tcpDemux) STCPR() net.Listener { return d.stcpr }

// DMSG returns the virtual listener carrying dmsg sessions, or nil when the
// demux was built without that branch.
func (d *tcpDemux) DMSG() net.Listener { return d.dmsg }

// Close closes the master listener, stopping cmux and the virtual listeners.
func (d *tcpDemux) Close() error { return d.master.Close() }
