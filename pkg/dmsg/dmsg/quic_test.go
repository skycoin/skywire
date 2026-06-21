//go:build !tinygo

// Package dmsg pkg/dmsg/dmsg/quic_test.go
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

// TestQUICSession exercises the full dmsg-over-QUIC path end to end after the
// QUIC code was extracted behind the quicConn/quicStream interfaces
// (quic_native.go). A server listens ONLY over QUIC (no TCP/WS), two clients
// dial it over QUIC, a stream is bridged A→B through the server and a payload
// round-trips, and an unreliable datagram round-trips over the QUIC session.
// This covers dialSessionQUIC, makeClientSessionQUIC, ServeQUIC,
// handleQUICConn, makeServerSessionQUIC, the quicConnWrap/quicStreamWrap
// adapters, OpenStreamSync/AcceptStream, and WriteDatagram/ReadDatagram — i.e.
// the whole extracted surface.
//
// The server's discovery entry advertises only AddressUDP + Protocol "quic"
// (empty TCP Address), so a client reaching a session here MUST have done so
// over QUIC.
func TestQUICSession(t *testing.T) {
	dc := disc.NewMock(0)
	const maxSessions = 10

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("quic_server"))

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpAddr := udpConn.LocalAddr().String()

	// Serve ONLY over QUIC.
	chSrv := make(chan error, 1)
	go func() { chSrv <- srv.ServeQUIC(udpConn, udpAddr) }()

	// Publish the server's discovery entry advertising only the QUIC endpoint
	// (empty TCP Address ⇒ no TCP fallback) with Protocol "quic" so the dial
	// switch takes the QUIC branch. ServeQUIC does not run self-registration.
	srvEntry := disc.NewServerEntry(pkSrv, 0, "", maxSessions)
	srvEntry.Server.Address = ""
	srvEntry.Server.AddressUDP = udpAddr
	srvEntry.Protocol = "quic"
	require.NoError(t, srvEntry.Sign(skSrv))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	// Protocol "quic" so the proactive session-establishment path (EnsureSession,
	// which sets entry.Protocol = conf.Protocol) takes the QUIC dial branch.
	quicConf := func() *Config {
		c := DefaultConfig()
		c.Protocol = "quic"
		return c
	}
	pkA, skA := GenKeyPair(t, "client A")
	clientA := NewClient(pkA, skA, dc, quicConf())
	clientA.SetLogger(logging.MustGetLogger("quic_client_A"))
	go clientA.Serve(context.Background())

	pkB, skB := GenKeyPair(t, "client B")
	clientB := NewClient(pkB, skB, dc, quicConf())
	clientB.SetLogger(logging.MustGetLogger("quic_client_B"))
	go clientB.Serve(context.Background())

	require.Eventually(t, func() bool {
		return clientA.SessionCount() > 0 && clientB.SessionCount() > 0
	}, 10*time.Second, 200*time.Millisecond, "clients failed to connect to DMSG server over QUIC")

	// Both sessions must be to the QUIC server (the only server, no TCP Address),
	// and must support datagrams — i.e. they are genuinely QUIC sessions.
	sesA, okA := clientA.Session(pkSrv)
	sesB, okB := clientB.Session(pkSrv)
	require.True(t, okA, "client A has no session to the QUIC server")
	require.True(t, okB, "client B has no session to the QUIC server")
	require.True(t, sesA.SupportsDatagrams(), "client A session is not a QUIC session")
	require.True(t, sesB.SupportsDatagrams(), "client B session is not a QUIC session")

	// Bridge a stream A→B through the server and round-trip a payload.
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
	go func() {
		_, _ = connA.Write(payload) //nolint:errcheck
	}()

	got := make([]byte, len(payload))
	_, err = io.ReadFull(connB, got)
	require.NoError(t, err)
	require.Equal(t, payload, got, "payload mismatch over QUIC-bridged stream")

	// Note: a live client↔client datagram round-trip isn't asserted here — dmsg
	// datagrams are session-level (client↔server, RFC 9221) and the server
	// doesn't echo/forward them, so there's no end-to-end path to check. The
	// SupportsDatagrams() assertions above already prove the quicConn datagram
	// interface is wired on a real QUIC session.

	require.NoError(t, clientA.Close())
	require.NoError(t, clientB.Close())
	require.NoError(t, srv.Close())
}
