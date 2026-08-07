// Package dmsg pkg/dmsg/dmsg/self_session_test.go
package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestSelfSession_SameKeyClientReachesOwnServer validates the load-bearing
// assumption behind the dmsg-server transit-client loopback fix: a dmsg CLIENT
// whose keypair is IDENTICAL to a co-located dmsg SERVER can still form a normal
// session with that server (the Noise-XK handshake with initiator==responder
// static key is valid, and the accept path has no self-PK guard), AND that
// server can relay an inbound stream from a THIRD client to the same-key client's
// listener — i.e. "reach the server → reach its co-located direct client".
func TestSelfSession_SameKeyClientReachesOwnServer(t *testing.T) {
	dc := disc.NewMock(0)

	// One keypair shared by the server and its co-located "transit" client.
	pk, sk := GenKeyPair(t, "self")

	srv := NewServer(pk, sk, dc, &ServerConfig{MaxSessions: 10, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("self_server"))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck

	// Server entry advertises the loopback listener (as the production fix does).
	srvEntry := disc.NewServerEntry(pk, 0, addr, 10)
	require.NoError(t, srvEntry.Sign(sk))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	// The transit client shares the server's keypair. It must NOT register (in
	// production it's a direct client) — MinSessions:0 = connect to all known
	// servers, and it resolves the server entry above from the shared mock.
	transit := NewClient(pk, sk, dc, &Config{MinSessions: 0})
	transit.SetLogger(logging.MustGetLogger("self_transit"))
	go transit.Serve(context.Background()) //nolint:errcheck

	// The same-key client forms a session with its OWN server.
	require.Eventually(t, func() bool {
		_, ok := transit.Session(pk)
		return ok
	}, 15*time.Second, 200*time.Millisecond, "same-key transit client failed to hold a session to its own server")

	// The transit client accepts inbound streams on port 80 (its /health analog).
	const port = 80
	lisT, err := transit.Listen(port)
	require.NoError(t, err)
	defer lisT.Close() //nolint:errcheck

	// A third client reaches the transit client THROUGH the shared server. It
	// seeds the transit PK as a client delegated to the server (mirrors how the
	// deployment services are reached: seeded, no discovery lookup).
	pkC, skC := GenKeyPair(t, "third")
	third := NewClient(pkC, skC, dc, DefaultConfig())
	third.SetLogger(logging.MustGetLogger("third"))
	third.SeedEntryCache(pk, &disc.Entry{
		Version: "0.0.1",
		Static:  pk,
		Client:  &disc.Client{DelegatedServers: []cipher.PubKey{pk}},
	})
	go third.Serve(context.Background()) //nolint:errcheck

	// Accept on the transit side.
	accepted := make(chan error, 1)
	go func() {
		s, aerr := lisT.AcceptStream()
		if aerr != nil {
			accepted <- aerr
			return
		}
		defer s.Close() //nolint:errcheck
		accepted <- nil
	}()

	var stream *Stream
	require.Eventually(t, func() bool {
		var derr error
		stream, derr = third.DialStream(context.TODO(), Addr{PK: pk, Port: port})
		return derr == nil
	}, 15*time.Second, 200*time.Millisecond, "third client could not dial the same-key transit client through its own server")
	defer stream.Close() //nolint:errcheck

	select {
	case aerr := <-accepted:
		require.NoError(t, aerr, "transit client failed to accept the relayed stream")
	case <-time.After(10 * time.Second):
		t.Fatal("transit client never accepted the relayed stream (server did not relay to its co-located same-key client)")
	}
}
