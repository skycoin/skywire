//go:build !tinygo

package network

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyquic"
)

// TestSetDmsgQUICServer is the folded dmsg server's QUIC path: a dmsg client,
// offering only the dmsg ALPN as real clients do, dials the visor's shared
// transport socket — which squicr also listens on — and ends up with a dmsg
// session on the server, authenticated by its client certificate.
func TestSetDmsgQUICServer(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	mux := newSharedQUICMux(conn, logging.MustGetLogger("test_shared_quic"))
	t.Cleanup(func() { _ = mux.Close(); _ = conn.Close() }) //nolint:errcheck
	require.NoError(t, mux.register(quicALPN, serverTLSForALPN(t, quicALPN), func(*quic.Conn) {}))

	srvPK, srvSK := cipher.GenerateKeyPair()
	srv := dmsg.NewServer(srvPK, srvSK, disc.NewMock(0), dmsg.DefaultServerConfig(), metrics.NewEmpty())
	t.Cleanup(func() { _ = srv.Close() }) //nolint:errcheck

	f := &ClientFactory{sharedQUIC: mux}
	addr, err := f.SetDmsgQUICServer(srv)
	require.NoError(t, err)
	require.Equal(t, conn.LocalAddr().String(), addr.String())

	cliPK, cliSK := cipher.GenerateKeyPair()
	cert, err := skyquic.NewCertificate(cliPK, cliSK)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tr := &quic.Transport{Conn: mustPacketConn(t)}
	t.Cleanup(func() { _ = tr.Close() }) //nolint:errcheck
	qc, err := tr.Dial(ctx, conn.LocalAddr(), skyquic.TLSConfigALPN(cert, &srvPK, nil, skyquic.DmsgNextProto), &quic.Config{EnableDatagrams: true})
	require.NoError(t, err, "the dmsg ALPN must be accepted on the shared socket")
	t.Cleanup(func() { _ = qc.CloseWithError(0, "") }) //nolint:errcheck
	require.Equal(t, skyquic.DmsgNextProto, qc.ConnectionState().TLS.NegotiatedProtocol)

	require.Eventually(t, func() bool { return srv.SessionCount() == 1 }, 5*time.Second, 20*time.Millisecond,
		"the server must hold a session for the dialing client")
}

// TestSetDmsgQUICServerWithoutUnifiedUDP: with no shared socket there is
// nothing to register on, and that is not an error.
func TestSetDmsgQUICServerWithoutUnifiedUDP(t *testing.T) {
	addr, err := (&ClientFactory{}).SetDmsgQUICServer(struct{}{})
	require.NoError(t, err)
	require.Nil(t, addr)
}
