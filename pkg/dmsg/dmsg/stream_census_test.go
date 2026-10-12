//go:build !tinygo

// Package dmsg pkg/dmsg/dmsg/stream_census_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

const censusPort = 4321

func censusCount(initiator bool, state string) int {
	for _, r := range StreamCensus() {
		if r.Port == censusPort && r.Initiator == initiator && r.State == state {
			return r.Count
		}
	}
	return 0
}

// The census shows a stream by port and state while it is in memory and
// forgets it once it is closed and collected.
func TestStreamCensus_TracksLifecycle(t *testing.T) {
	dc := disc.NewMock(0)
	srvPK, srvSK := GenKeyPair(t, "census-srv")
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	srv := NewServer(srvPK, srvSK, dc, &ServerConfig{MaxSessions: 20, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("census-srv"))
	entry := disc.NewServerEntry(srvPK, 0, addr, 20)
	require.NoError(t, entry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck

	newClient := func(name string) (*Client, ClientSession) {
		pk, sk := GenKeyPair(t, name)
		c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
		c.SetLogger(logging.MustGetLogger(name))
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		ses, err := c.EnsureAndObtainSession(context.Background(), srvPK)
		require.NoError(t, err)
		return c, ses
	}
	_, aSes := newClient("census-a")
	b, _ := newClient("census-b")

	bl, err := b.Listen(censusPort)
	require.NoError(t, err)
	t.Cleanup(func() { _ = bl.Close() }) //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// B's session can reach the server a moment after it is returned.
	var out *Stream
	require.Eventually(t, func() bool {
		out, err = aSes.DialStream(ctx, Addr{PK: b.LocalPK(), Port: censusPort})
		return err == nil
	}, 10*time.Second, 50*time.Millisecond, "dial never reached B")
	in, err := bl.AcceptStream()
	require.NoError(t, err)

	require.Equal(t, 1, censusCount(true, "dialed"))
	require.Equal(t, 1, censusCount(false, "accepted"))

	require.NoError(t, out.Close())
	require.NoError(t, in.Close())
	require.GreaterOrEqual(t, censusCount(true, "closed"), 1)

	out, in = nil, nil //nolint:ineffassign,wastedassign
	require.Eventually(t, func() bool {
		runtime.GC()
		return censusCount(true, "closed")+censusCount(false, "closed") == 0
	}, 5*time.Second, 50*time.Millisecond, "closed streams stayed in memory")
}

// Nothing reads the census on a server, so tracking alone must keep the map
// from holding an entry for every stream ever made.
func TestStreamCensus_BoundedWithoutReads(t *testing.T) {
	for i := 0; i < 20*censusPruneMin; i++ {
		censusTrack(&Stream{}, true)
		if i%censusPruneMin == 0 {
			runtime.GC()
		}
	}
	streamCensus.mu.Lock()
	n := len(streamCensus.m)
	streamCensus.mu.Unlock()
	require.Less(t, n, 4*censusPruneMin)
}
