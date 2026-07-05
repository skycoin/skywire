// Package dmsg pkg/dmsg/session_ping_test.go
package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestSessionPing_YamuxStreamRoundTrip verifies that Ping() on a healthy yamux
// session performs a real stream round-trip (yamuxPing) through the server's
// serveStream ping-marker echo and returns a positive RTT with no error.
//
// Regression guard for the zombie-session bug: the old yamux path used
// yamux.Session.Ping() (a control-frame keepalive) which reports a session
// alive even when the server can no longer relay streams to/from us, so
// pingSessionsLoop never marked half-open sessions dead. The fix routes yamux
// through a stream round-trip like smux already did. This test also guards the
// other direction — a HEALTHY yamux session must NOT be false-killed by the new
// stream-level probe (the server must echo the ping marker for yamux streams).
func TestSessionPing_YamuxStreamRoundTrip(t *testing.T) {
	dc := disc.NewMock(0)

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: 10, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("server"))
	lisSrv, err := net.Listen("tcp", "")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lisSrv, "") }() //nolint:errcheck

	pkA, skA := GenKeyPair(t, "client A")
	clientA := NewClient(pkA, skA, dc, DefaultConfig()) // default protocol => yamux
	clientA.SetLogger(logging.MustGetLogger("client_A"))
	go clientA.Serve(context.Background())
	t.Cleanup(func() {
		_ = clientA.Close() //nolint:errcheck
		_ = srv.Close()     //nolint:errcheck
	})

	require.Eventually(t, func() bool {
		return clientA.SessionCount() > 0
	}, 10*time.Second, 200*time.Millisecond, "client failed to connect to dmsg server")

	sessions := clientA.allClientSessions(clientA.porter)
	require.NotEmpty(t, sessions, "expected at least one client session")

	// A healthy yamux session must round-trip the stream-level ping. The
	// round-trip actually happening is proven by require.NoError (yamuxPing does a
	// real stream exchange that errors/times out if the server doesn't echo). The
	// RTT is only a sanity bound: non-negative. It must NOT assert strictly >0 —
	// on Windows a loopback round-trip can complete within the monotonic clock's
	// granularity, so time.Since() legitimately measures exactly 0.
	rtt, err := sessions[0].Ping()
	require.NoError(t, err, "yamux stream-level ping must succeed on a healthy session")
	assert.GreaterOrEqual(t, int64(rtt), int64(0), "ping round-trip time must be non-negative")
}
