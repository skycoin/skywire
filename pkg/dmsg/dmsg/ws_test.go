// Package dmsg pkg/dmsg/dmsg/ws_test.go
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

// TestWebSocketSession exercises the full dmsg-over-WebSocket path end to end:
// a server that listens ONLY over WebSocket (no TCP/QUIC), two clients that
// dial it over WS (PreferWS), and a stream bridged between them through the
// server. This proves the Noise handshake + yamux mux + server bridge all run
// unchanged over a websocket.NetConn — i.e. WS is a drop-in transport.
//
// The server's discovery entry is posted with an empty TCP Address and only an
// AddressWS, so there is no TCP fallback: a client reaching a session here MUST
// have done so over WebSocket.
func TestWebSocketSession(t *testing.T) {
	dc := disc.NewMock(0)
	const maxSessions = 10

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("ws_server"))

	lisWS, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	wsURL := "ws://" + lisWS.Addr().String() + wsPath

	// Serve ONLY over WebSocket.
	chSrv := make(chan error, 1)
	go func() { chSrv <- srv.ServeWS(lisWS, wsURL) }()

	// Manually publish the server's discovery entry advertising only the WS
	// endpoint (empty Address ⇒ no TCP fallback). ServeWS does not run the
	// self-registration loop (that lives in Serve), so we post it here.
	srvEntry := disc.NewServerEntry(pkSrv, 0, "", maxSessions)
	srvEntry.Server.AddressWS = wsURL
	require.NoError(t, srvEntry.Sign(skSrv))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	// Two WS-preferring clients.
	wsConf := func() *Config {
		c := DefaultConfig()
		c.Carriers = []string{CarrierWS}
		return c
	}
	pkA, skA := GenKeyPair(t, "client A")
	clientA := NewClient(pkA, skA, dc, wsConf())
	clientA.SetLogger(logging.MustGetLogger("ws_client_A"))
	go clientA.Serve(context.Background())

	pkB, skB := GenKeyPair(t, "client B")
	clientB := NewClient(pkB, skB, dc, wsConf())
	clientB.SetLogger(logging.MustGetLogger("ws_client_B"))
	go clientB.Serve(context.Background())

	require.Eventually(t, func() bool {
		return clientA.SessionCount() > 0 && clientB.SessionCount() > 0
	}, 10*time.Second, 200*time.Millisecond, "clients failed to connect to DMSG server over WebSocket")

	// Both sessions must be to the WS server — confirms the WS dial succeeded
	// (there is no other server, and the entry carries no TCP Address).
	_, okA := clientA.Session(pkSrv)
	_, okB := clientB.Session(pkSrv)
	require.True(t, okA, "client A has no session to the WS server")
	require.True(t, okB, "client B has no session to the WS server")

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
	require.Equal(t, payload, got, "payload mismatch over WebSocket-bridged stream")

	require.NoError(t, clientA.Close())
	require.NoError(t, clientB.Close())
	require.NoError(t, srv.Close())
}
