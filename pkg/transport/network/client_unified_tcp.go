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
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("unified transport port %d (tcp): %w", port, err)
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
