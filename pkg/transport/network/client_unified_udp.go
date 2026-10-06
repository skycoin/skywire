//go:build !(tinygo && js)

// Package network pkg/transport/network/client_unified_udp.go c2-net-transport
//
// The unified transport port (UDP side): bind ONE master UDP socket and route the
// UDP transport types (QUIC + sudph) over it via a udpDemux, so they share a
// single listening port instead of binding one each. See
// docs/design/transport-port-unification.md. Opt-in: only active when the visor
// configures transport_port (EnableUnifiedUDP is a no-op for port 0).
package network

import (
	"crypto/tls"
	"fmt"
	"net"

	"github.com/quic-go/quic-go"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyquic"
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
	d := newUDPDemux(conn)
	f.udpDemux = d
	// Build the shared QUIC multiplexer over the demux's QUIC virtual conn so
	// squicr + WebTransport ride ONE quic.Transport on this master socket (by
	// ALPN) instead of binding a port each. The listener starts lazily when the
	// first QUIC-based client registers.
	var log *logging.Logger
	if f.MLogger != nil {
		log = f.MLogger.PackageLogger("shared_quic")
	} else {
		log = logging.MustGetLogger("shared_quic")
	}
	f.sharedQUIC = newSharedQUICMux(d.Conn(protoQUIC), log)
	return nil
}

// dmsgQUICServer is the part of a dmsg server (pkg/dmsg/dmsg.Server) that
// serves QUIC connections accepted here.
type dmsgQUICServer interface {
	QUICTLSConfig() (*tls.Config, error)
	ServeQUICConn(*quic.Conn)
}

// SetDmsgQUICServer registers the dmsg ALPN on the shared QUIC socket and hands
// its connections to srv, so a dmsg server folded into this visor serves
// dmsg-over-QUIC on the transport port. Without it that port belongs to squicr
// and WT alone, and a dmsg client's QUIC dial is refused at the handshake. srv
// is typed `any`, as SetDmsgWSHandler's handler is. Returns the socket's local
// address, or nil when unified UDP is not enabled.
func (f *ClientFactory) SetDmsgQUICServer(srv any) (net.Addr, error) {
	m, ok := f.sharedQUIC.(*sharedQUICMux)
	if !ok || m == nil {
		return nil, nil
	}
	s, ok := srv.(dmsgQUICServer)
	if !ok {
		return nil, fmt.Errorf("dmsg quic: %T does not serve QUIC connections", srv)
	}
	tlsConf, err := s.QUICTLSConfig()
	if err != nil {
		return nil, err
	}
	if err := m.register(skyquic.DmsgNextProto, tlsConf, s.ServeQUICConn); err != nil {
		return nil, err
	}
	return m.localAddr(), nil
}

// CloseUnifiedUDP closes the shared QUIC mux (its listener + transport), then the
// master UDP socket + demux, if enabled.
func (f *ClientFactory) CloseUnifiedUDP() error {
	if m, ok := f.sharedQUIC.(*sharedQUICMux); ok && m != nil {
		_ = m.Close() //nolint:errcheck
	}
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
