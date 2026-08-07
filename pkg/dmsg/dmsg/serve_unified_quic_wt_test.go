//go:build !tinygo && !(js && wasm)

// Package dmsg pkg/dmsg/dmsg/serve_unified_quic_wt_test.go
package dmsg

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestServeUnifiedQUIC_QUICThroughDemux proves that co-hosting WebTransport on the
// shared quic.Transport (ServeUnifiedQUIC with a non-empty WT URL) does NOT break
// native dmsg-over-QUIC. The rewrite replaced the broken two-listener design (a
// quic.Transport permits only ONE listener, so the WT ListenEarly always failed)
// with a SINGLE ListenEarly whose GetConfigForClient hands back the dmsg identity
// TLS config for the skywire ALPN and the WT config for "h3". This test drives two
// QUIC clients through that demux listener and bridges a stream A→B, so a
// regression in the skywire-ALPN (mutual-TLS) handshake selection would fail here.
func TestServeUnifiedQUIC_QUICThroughDemux(t *testing.T) {
	dc := disc.NewMock(0)
	const maxSessions = 10

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("uquic_server"))

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpAddr := udpConn.LocalAddr().String()

	// Serve QUIC + WebTransport on ONE socket. The non-empty WT URL exercises
	// buildWTServer + the GetConfigForClient ALPN demux (the path that was dead).
	chSrv := make(chan error, 1)
	go func() { chSrv <- srv.ServeUnifiedQUIC(udpConn, udpAddr, "https://"+udpAddr+"/dmsg") }()

	srvEntry := disc.NewServerEntry(pkSrv, 0, "", maxSessions)
	srvEntry.Server.Address = ""
	srvEntry.Server.AddressUDP = udpAddr
	srvEntry.Protocol = "quic"
	require.NoError(t, srvEntry.Sign(skSrv))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	quicConf := func() *Config {
		c := DefaultConfig()
		c.Protocol = "quic"
		return c
	}
	pkA, skA := GenKeyPair(t, "client A")
	clientA := NewClient(pkA, skA, dc, quicConf())
	clientA.SetLogger(logging.MustGetLogger("uquic_client_A"))
	go clientA.Serve(context.Background())

	pkB, skB := GenKeyPair(t, "client B")
	clientB := NewClient(pkB, skB, dc, quicConf())
	clientB.SetLogger(logging.MustGetLogger("uquic_client_B"))
	go clientB.Serve(context.Background())

	require.Eventually(t, func() bool {
		return clientA.SessionCount() > 0 && clientB.SessionCount() > 0
	}, 10*time.Second, 200*time.Millisecond, "QUIC clients failed to connect through the WT-demux listener")

	sesA, okA := clientA.Session(pkSrv)
	require.True(t, okA, "client A has no session to the unified server")
	require.True(t, sesA.SupportsDatagrams(), "session is not a genuine QUIC session")

	// Full data path: bridge a stream A→B through the server and round-trip a payload.
	const port = 8080
	lis, err := clientB.Listen(port)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	connA, err := clientA.DialStream(context.TODO(), Addr{PK: pkB, Port: port})
	require.NoError(t, err)
	defer connA.Close() //nolint:errcheck
	connB, err := lis.Accept()
	require.NoError(t, err)
	defer connB.Close() //nolint:errcheck

	payload := cipher.RandByte(4096)
	go func() { _, _ = connA.Write(payload) }() //nolint:errcheck
	got := make([]byte, len(payload))
	_, err = io.ReadFull(connB, got)
	require.NoError(t, err)
	require.Equal(t, payload, got, "payload mismatch over QUIC through the WT-demux listener")

	require.NoError(t, clientA.Close())
	require.NoError(t, clientB.Close())
	require.NoError(t, srv.Close())
}
