// Package dmsg pkg/dmsg/dmsg/client_dialflight_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// Concurrent EnsureAndObtainSession calls for one server must produce ONE
// session, not one per caller.
//
// A dmsg server keeps a single session per client PK, and both ends install a
// reconnect newest-session-wins, so every duplicate dial EVICTS the session the
// previous caller was handed — at the server (which closes it remotely) and in
// the client's own map. The callers that raced hold dead sessions, their streams
// die, and the Serve loop re-dials a server it never lost.
//
// The pinned-rendezvous hostname in pkg/dmsgweb (<server-pk>.<dest-pk>.dmsg) is
// how a user reaches this from a browser: one page opens several connections,
// each one dials the named server, and a hostname anyone can type costs a live
// session. Before the coalescing in dialSessionOnce, six concurrent callers got
// six distinct sessions and five of them were dead on arrival.
func TestEnsureAndObtainSession_CoalescesConcurrentDials(t *testing.T) {
	dc := disc.NewMock(0)
	srvPK, srvSK := GenKeyPair(t, "dialflight-srv")

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()

	srv := NewServer(srvPK, srvSK, dc, &ServerConfig{MaxSessions: 20, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("dialflight-srv"))
	entry := disc.NewServerEntry(srvPK, 0, addr, 20)
	require.NoError(t, entry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck

	// No Serve loop: the pinned dials below are the only dials, so the race is
	// the one under test rather than one with the client's own reconnect loop.
	pk, sk := GenKeyPair(t, "dialflight-cli")
	c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
	c.SetLogger(logging.MustGetLogger("dialflight-cli"))
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck

	const callers = 6
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		got   = make([]ClientSession, callers)
		errs  = make([]error, callers)
	)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i], errs[i] = c.EnsureAndObtainSession(context.Background(), srvPK)
		}(i)
	}
	close(start)
	wg.Wait()

	distinct := make(map[*SessionCommon]struct{}, callers)
	for i := 0; i < callers; i++ {
		require.NoError(t, errs[i], "caller %d", i)
		distinct[got[i].SessionCommon] = struct{}{}
	}
	require.Len(t, distinct, 1, "concurrent callers dialed duplicate sessions to one server")

	// Give any duplicate's eviction time to land, then check that the session
	// the first caller was handed is still the live one.
	time.Sleep(500 * time.Millisecond)
	live, ok := c.Session(srvPK)
	require.True(t, ok, "the client lost its session to the server")
	require.Same(t, got[0].SessionCommon, live.SessionCommon,
		"the session handed to the first caller was replaced and closed")
	require.Equal(t, 1, c.SessionCount())
}
