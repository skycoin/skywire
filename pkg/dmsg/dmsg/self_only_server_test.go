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

// TestSelfOnlyServer_ClientBeforeServer models a visor whose folded dmsg server
// is the only server it knows. The visor's client starts first and fails to
// dial while the server is down, then becomes ready through it once it serves,
// and a second client reaches the visor's key through that one server.
func TestSelfOnlyServer_ClientBeforeServer(t *testing.T) {
	dc := disc.NewMock(0)
	pk, sk := GenKeyPair(t, "self-only")

	// Reserve an address, then free it so the first dials are refused.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	srvEntry := disc.NewServerEntry(pk, 0, addr, 10)
	require.NoError(t, srvEntry.Sign(sk))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	visor := NewClient(pk, sk, dc, &Config{MinSessions: 1})
	visor.SetLogger(logging.MustGetLogger("self_only_visor"))
	go visor.Serve(ctx)
	defer visor.Close() //nolint:errcheck

	select {
	case <-visor.Ready():
		t.Fatal("client ready before its only server serves")
	case <-time.After(time.Second):
	}

	srv := NewServer(pk, sk, dc, &ServerConfig{MaxSessions: 10, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("self_only_server"))
	lis, err = net.Listen("tcp", addr)
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	defer srv.Close()                        //nolint:errcheck

	select {
	case <-visor.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("client never became ready through its own server")
	}

	const port = 80
	lisV, err := visor.Listen(port)
	require.NoError(t, err)
	defer lisV.Close() //nolint:errcheck

	pkC, skC := GenKeyPair(t, "self-only-peer")
	peer := NewClient(pkC, skC, dc, DefaultConfig())
	peer.SetLogger(logging.MustGetLogger("self_only_peer"))
	go peer.Serve(ctx)
	defer peer.Close() //nolint:errcheck

	accepted := make(chan error, 1)
	go func() {
		s, aerr := lisV.AcceptStream()
		if aerr == nil {
			aerr = s.Close()
		}
		accepted <- aerr
	}()

	// The peer resolves the visor from discovery, where one entry carries both
	// the server and the client role.
	var stream *Stream
	require.Eventually(t, func() bool {
		var derr error
		stream, derr = peer.DialStream(ctx, Addr{PK: pk, Port: port})
		return derr == nil
	}, 30*time.Second, 200*time.Millisecond, "peer could not reach the visor's key through its own server")
	defer stream.Close() //nolint:errcheck

	select {
	case aerr := <-accepted:
		require.NoError(t, aerr)
	case <-time.After(10 * time.Second):
		t.Fatal("visor never accepted the peer's stream")
	}
}
