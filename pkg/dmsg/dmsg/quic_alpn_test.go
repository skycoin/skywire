package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyquic"
)

// TestQUICServerALPNs pins the ALPN contract of a dmsg-over-QUIC server: it
// accepts the dmsg id (what clients offer now) and the transport id (what
// clients built before the split still offer for dmsg), and refuses anything
// else at the handshake. The refusal is what lets a client dialing a socket
// with no dmsg server behind it fall back to TCP at once.
func TestQUICServerALPNs(t *testing.T) {
	dc := disc.NewMock(0)
	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: 4, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("quic_alpn_server"))
	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpAddr := udpConn.LocalAddr().String()
	go func() { _ = srv.ServeQUIC(udpConn, udpAddr) }()
	t.Cleanup(func() { _ = srv.Close() })

	pkC, skC := GenKeyPair(t, "client")
	cert, err := skyquic.NewCertificate(pkC, skC)
	require.NoError(t, err)
	dial := func(protos ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		require.NoError(t, err)
		defer sock.Close() //nolint:errcheck
		raddr, err := net.ResolveUDPAddr("udp", udpAddr)
		require.NoError(t, err)
		qc, err := quic.Dial(ctx, sock, raddr, skyquic.TLSConfigALPN(cert, &pkSrv, nil, protos...), &quic.Config{})
		if err != nil {
			return err
		}
		return qc.CloseWithError(0, "")
	}
	require.NoError(t, dial(skyquic.DmsgNextProto), "the dmsg ALPN is accepted")
	require.NoError(t, dial(skyquic.NextProto), "an older client's transport ALPN is still accepted")
	require.Error(t, dial("skywire-nothing-1"), "an unknown ALPN is refused at the handshake")
}
