//go:build !tinygo

// Package network pkg/transport/network/client_unified_udp.go
//
// The unified transport port (UDP side): bind ONE master UDP socket and route the
// UDP transport types (QUIC + sudph) over it via a udpDemux, so they share a
// single listening port instead of binding one each. See
// docs/design/transport-port-unification.md. Opt-in: only active when the visor
// configures transport_port (EnableUnifiedUDP is a no-op for port 0).
package network

import (
	"fmt"
	"net"
)

// EnableUnifiedUDP binds the master UDP socket on port and prepares the demux.
// Call once, before MakeClient. port 0 is a no-op (per-type binding stays). The
// quic/sudph clients created afterwards listen over the demux's per-protocol
// virtual conn (see makeResolvedClient). The master socket lifecycle is owned
// here — close it with CloseUnifiedUDP.
func (f *ClientFactory) EnableUnifiedUDP(port int) error {
	if port == 0 {
		return nil
	}
	conn, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("unified transport port %d: %w", port, err)
	}
	// quic-go can't set the receive buffer on a demuxConn (not a *net.UDPConn), so
	// set it once on the master socket (best-effort; quic-go's recommended size).
	if uc, ok := conn.(*net.UDPConn); ok {
		_ = uc.SetReadBuffer(7 << 20) //nolint:errcheck
	}
	f.udpDemux = newUDPDemux(conn)
	return nil
}

// CloseUnifiedUDP closes the master UDP socket + demux, if enabled.
func (f *ClientFactory) CloseUnifiedUDP() error {
	if d, ok := f.udpDemux.(*udpDemux); ok && d != nil {
		return d.Close()
	}
	return nil
}

// sharedUDPConn returns the demux's virtual conn for proto when unified UDP is
// enabled, else nil (per-type binding).
func (f *ClientFactory) sharedUDPConn(proto udpProto) net.PacketConn {
	if d, ok := f.udpDemux.(*udpDemux); ok && d != nil {
		return d.Conn(proto)
	}
	return nil
}

// sharedUDPConnFor returns the shared demux conn for proto only when the type
// rides the master port — i.e. its per-type port is 0. A non-zero per-type port
// (e.g. quic_port / sudph_port) breaks that type out onto its own socket even
// when transport_port is set, so an operator can pin one type to a dedicated port
// while the rest share. Returns nil → per-type binding.
func (f *ClientFactory) sharedUDPConnFor(proto udpProto, perTypePort int) net.PacketConn {
	if perTypePort != 0 {
		return nil
	}
	return f.sharedUDPConn(proto)
}
