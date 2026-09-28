package dmsg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func TestPeerMissCache(t *testing.T) {
	var c EntityCommon
	dst, _ := cipher.GenerateKeyPair()

	require.False(t, c.peerMissed(dst))
	c.notePeerMiss(dst)
	require.True(t, c.peerMissed(dst))
	c.clearPeerMiss(dst)
	require.False(t, c.peerMissed(dst))

	// An expired entry no longer refuses and is dropped.
	c.notePeerMiss(dst)
	c.peerMiss[dst] = time.Now().Add(-time.Second)
	require.False(t, c.peerMissed(dst))
	require.NotContains(t, c.peerMiss, dst)
}

func TestPeerMissCacheBounded(t *testing.T) {
	var c EntityCommon
	c.peerMiss = make(map[cipher.PubKey]time.Time, peerMissCap)
	for i := 0; i < peerMissCap; i++ {
		pk, _ := cipher.GenerateKeyPair()
		c.peerMiss[pk] = time.Now().Add(time.Minute)
	}
	extra, _ := cipher.GenerateKeyPair()
	c.notePeerMiss(extra)
	require.False(t, c.peerMissed(extra), "full cache of live entries records nothing new")

	// Once entries expire, a new miss prunes them and is recorded.
	for pk := range c.peerMiss {
		c.peerMiss[pk] = time.Now().Add(-time.Second)
	}
	c.notePeerMiss(extra)
	require.True(t, c.peerMissed(extra))
	require.Len(t, c.peerMiss, 1)
}

func TestIsPeerMiss(t *testing.T) {
	require.True(t, isPeerMiss(io.EOF))
	require.True(t, isPeerMiss(fmt.Errorf("read: %w", io.ErrUnexpectedEOF)))
	require.False(t, isPeerMiss(nil))
	require.False(t, isPeerMiss(os.ErrDeadlineExceeded))
	require.False(t, isPeerMiss(ErrRelayCapacityReached))
	require.False(t, isPeerMiss(ErrReqNoListener))
	require.False(t, isPeerMiss(errors.New("dial refused")))
}

// A server asked for a destination that neither it nor its peer holds records
// the miss, and a later request for it is refused without asking the peer.
// A destination the peer does hold is forwarded and leaves no miss behind.
func TestForwardViaPeerRecordsMiss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dc := disc.NewMock(0)
	type srv struct {
		pk   cipher.PubKey
		sk   cipher.SecKey
		lis  net.Listener
		addr string
	}
	mk := func(name string) srv {
		pk, sk := GenKeyPair(t, name)
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		return srv{pk: pk, sk: sk, lis: lis, addr: lis.Addr().String()}
	}
	a, b := mk("miss-a"), mk("miss-b")
	confA := DefaultServerConfig()
	confA.Peers = []PeerEntry{{PK: b.pk, Addr: b.addr}}
	confB := DefaultServerConfig()
	confB.Peers = []PeerEntry{{PK: a.pk, Addr: a.addr}}
	srvA := NewServer(a.pk, a.sk, dc, confA, nil)
	srvB := NewServer(b.pk, b.sk, dc, confB, nil)
	go func() { _ = srvA.Serve(a.lis, a.addr) }()            //nolint:errcheck
	go func() { _ = srvB.Serve(b.lis, b.addr) }()            //nolint:errcheck
	t.Cleanup(func() { _ = srvA.Close(); _ = srvB.Close() }) //nolint:errcheck
	<-srvA.Ready()
	<-srvB.Ready()
	require.Eventually(t, func() bool {
		return len(srvA.peerServerSessions()) > 0 && len(srvB.peerServerSessions()) > 0
	}, 15*time.Second, 100*time.Millisecond, "servers must peer")

	// onlyOn returns a client whose discovery knows just one server.
	onlyOn := func(name string, s cipher.PubKey) *Client {
		cdc := disc.NewMock(0)
		e, err := dc.Entry(ctx, s)
		require.NoError(t, err)
		require.NoError(t, cdc.PostEntry(ctx, e))
		pk, sk := GenKeyPair(t, name)
		c := NewClient(pk, sk, cdc, &Config{MinSessions: 1})
		go c.Serve(ctx)
		<-c.Ready()
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		return c
	}
	onA := onlyOn("miss-client-a", a.pk)
	onB := onlyOn("miss-client-b", b.pk)
	lis, err := onB.Listen(1)
	require.NoError(t, err)
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			_ = c.Close() //nolint:errcheck
		}
	}()

	ses, ok := onA.clientSession(onA.porter, a.pk)
	require.True(t, ok)

	// Present on the peer: forwarded, no miss.
	s, err := ses.DialStream(ctx, Addr{PK: onB.LocalPK(), Port: 1})
	require.NoError(t, err)
	_ = s.Close() //nolint:errcheck
	require.False(t, srvA.peerMissed(onB.LocalPK()))

	// Nowhere: refused, and the miss is recorded.
	absent, _ := cipher.GenerateKeyPair()
	_, err = ses.DialStream(ctx, Addr{PK: absent, Port: 1})
	require.Error(t, err)
	require.Eventually(t, func() bool { return srvA.peerMissed(absent) },
		5*time.Second, 50*time.Millisecond, "server A must record the miss")
}
