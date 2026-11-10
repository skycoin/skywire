// Package dmsg pkg/dmsg/dmsg/server_sig_count_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// A stream between two clients of one server costs two signature checks, the
// destination's on the request and the initiator's on the response. The server
// relies on each session's handshake instead of checking them a second time.
func TestServerSkipsSessionAuthenticatedSigChecks(t *testing.T) {
	var checks atomic.Int64
	orig := verifySig
	verifySig = func(pk cipher.PubKey, sig cipher.Sig, payload []byte) error {
		checks.Add(1)
		return orig(pk, sig, payload)
	}
	t.Cleanup(func() { verifySig = orig })

	dc := disc.NewMock(0)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	srvPK, srvSK := cipher.GenerateKeyPair()
	srv := NewServer(srvPK, srvSK, dc, &ServerConfig{MaxSessions: 20, UpdateInterval: 0}, nil)
	entry := disc.NewServerEntry(srvPK, 0, addr, 20)
	require.NoError(t, entry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck

	newClient := func(name string) (*Client, ClientSession) {
		pk, sk := cipher.GenerateKeyPair()
		c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
		c.SetLogger(logging.MustGetLogger(name))
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		ses, err := c.EnsureAndObtainSession(context.Background(), srvPK)
		require.NoError(t, err)
		return c, ses
	}
	_, aSes := newClient("sigcount-a")
	b, _ := newClient("sigcount-b")
	// A client has its session before the server lists it, and a dial to a
	// session the server does not list yet is refused.
	require.Eventually(t, func() bool { return srv.SessionCount() == 2 }, 5*time.Second, time.Millisecond)
	l, err := b.Listen(1001)
	require.NoError(t, err)
	go func() {
		for {
			s, err := l.Accept()
			if err != nil {
				return
			}
			_ = s.Close() //nolint:errcheck
		}
	}()

	const streams = 10
	checks.Store(0)
	for range streams {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s, err := aSes.DialStream(ctx, Addr{PK: b.LocalPK(), Port: 1001})
		cancel()
		require.NoError(t, err)
		_ = s.Close() //nolint:errcheck
	}
	require.Equal(t, int64(2*streams), checks.Load(), "signature checks for %d streams", streams)
}
