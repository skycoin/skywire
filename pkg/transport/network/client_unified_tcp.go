//go:build !tinygo

// Package network pkg/transport/network/client_unified_tcp.go
//
// The unified transport port (TCP side): bind ONE master TCP listener and route
// the TCP transport types (stcpr raw + WS HTTP-upgrade) over it via a tcpDemux
// (cmux), so they share a single listening port instead of binding one each. See
// docs/design/transport-port-unification.md. Opt-in: only active when the visor
// configures transport_port (EnableUnifiedTCP is a no-op for port 0).
package network

import (
	"fmt"
	"net"
)

// EnableUnifiedTCP binds the master TCP listener on port and prepares the cmux
// demux. Call once, before MakeClient. port 0 is a no-op (per-type binding
// stays). stcpr accepts over the raw virtual listener and the WS HTTP server runs
// over the HTTP virtual listener (see makeResolvedClient / MakeClient). The master
// listener lifecycle is owned here — close it with CloseUnifiedTCP.
func (f *ClientFactory) EnableUnifiedTCP(port int) error {
	if port == 0 {
		return nil
	}
	return f.bindTCPDemux(port)
}

// EnableDefaultTCPDemux binds the DEFAULT TCP cmux (stcpr + WS) on the stcpr port
// — the phase-2 behavior so EVERY visor accepts WebSocket on its stcpr TCP socket
// without any config, with no port change (the cmux peeks: HTTP/WS-upgrade → WS,
// else the raw skywire handshake → stcpr; existing stcpr peers are unaffected).
// stcprPort 0 binds a random port (exactly as a per-type stcpr listener would).
// Unlike EnableUnifiedTCP this is NOT gated on transport_port, and stcpr always
// rides it (the cmux IS the stcpr listener — no break-out).
func (f *ClientFactory) EnableDefaultTCPDemux(stcprPort int) error {
	if err := f.bindTCPDemux(stcprPort); err != nil {
		return err
	}
	f.tcpDefaultDemux = true
	return nil
}

// bindTCPDemux binds a TCP listener on :port (port 0 → random) and installs the
// stcpr+WS cmux over it.
func (f *ClientFactory) bindTCPDemux(port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("tcp cmux (stcpr+ws) port %d: %w", port, err)
	}
	d := newTCPDemux(lis)
	f.tcpDemux = d
	f.stcprSharedListener = d.STCPR()
	f.wsSharedListener = d.WS()
	return nil
}

// CloseUnifiedTCP closes the master TCP listener + cmux demux, if enabled.
func (f *ClientFactory) CloseUnifiedTCP() error {
	if d, ok := f.tcpDemux.(*tcpDemux); ok && d != nil {
		return d.Close()
	}
	return nil
}

// stcprSharedListenerFor returns the shared stcpr virtual listener only when
// stcpr rides the master port — i.e. its per-type port (stcpr_port) is 0. A
// non-zero stcpr_port breaks stcpr out onto its own port even when transport_port
// is set. Returns nil → per-type binding.
func (f *ClientFactory) stcprSharedListenerFor(perTypePort int) net.Listener {
	// Default demux (phase 2): the cmux IS bound on the stcpr port, so stcpr
	// always rides it — there's no master port to break out of.
	if f.tcpDefaultDemux {
		return f.stcprSharedListener
	}
	// Explicit transport_port master: a non-zero stcpr_port breaks stcpr out onto
	// its own dedicated socket.
	if perTypePort != 0 {
		return nil
	}
	return f.stcprSharedListener
}
