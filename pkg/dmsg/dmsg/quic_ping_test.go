//go:build !tinygo

// Package dmsg pkg/dmsg/dmsg/quic_ping_test.go
package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestQUICSessionPing asserts the liveness ping works on a dmsg-over-QUIC
// session. SessionCommon.Ping only branched on yamux and smux, so a QUIC
// session — whose native streams ARE the dmsg streams, with no separate mux —
// fell through to "no mux session available for ping" every single time.
// pingSessionsLoop closes a session after two consecutive failures, so every
// healthy QUIC session was declared dead and re-dialed roughly every two
// minutes. The server side always answered the ping marker (serveStream is
// mux-agnostic); only the client's dispatch was missing.
func TestQUICSessionPing(t *testing.T) {
	dc := disc.NewMock(0)
	const maxSessions = 10

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("quic_ping_server"))

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpAddr := udpConn.LocalAddr().String()

	// Serve ONLY over QUIC, so a session that exists is necessarily a QUIC one.
	go func() { _ = srv.ServeQUIC(udpConn, udpAddr) }() //nolint:errcheck

	srvEntry := disc.NewServerEntry(pkSrv, 0, "", maxSessions)
	srvEntry.Server.Address = "" // no TCP endpoint ⇒ no yamux/smux fallback
	srvEntry.Server.AddressUDP = udpAddr
	srvEntry.Protocol = "quic"
	require.NoError(t, srvEntry.Sign(skSrv))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	conf := DefaultConfig()
	conf.Protocol = "quic"
	pkC, skC := GenKeyPair(t, "client")
	client := NewClient(pkC, skC, dc, conf)
	client.SetLogger(logging.MustGetLogger("quic_ping_client"))
	go client.Serve(context.Background())

	require.Eventually(t, func() bool {
		return client.SessionCount() > 0
	}, 10*time.Second, 200*time.Millisecond, "client failed to connect to DMSG server over QUIC")

	ses, ok := client.Session(pkSrv)
	require.True(t, ok, "client has no session to the QUIC server")
	require.True(t, ses.SupportsDatagrams(), "session is not a QUIC session")

	// The ping must round-trip. Before the fix this returned
	// "no mux session available for ping".
	rtt, err := ses.Ping()
	require.NoError(t, err, "ping failed on a healthy QUIC session")
	require.Positive(t, rtt, "ping reported a non-positive round-trip time")

	// Repeated pings must keep working: pingSessionsLoop needs two consecutive
	// successes' worth of reliability to never trip pingDeadThreshold.
	for i := 0; i < 3; i++ {
		_, err := ses.Ping()
		require.NoErrorf(t, err, "ping %d failed on a healthy QUIC session", i+2)
	}

	require.NoError(t, client.Close())
	require.NoError(t, srv.Close())
}
