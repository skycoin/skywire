package treestore

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// slowProxy forwards TCP to target, holding each chunk for delay in both
// directions, the way a relayed dmsg stream stretches every request.
func slowProxy(t *testing.T, target string, delay time.Duration) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck
	pipe := func(dst, src net.Conn) {
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				time.Sleep(delay)
				if _, werr := dst.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = dst.Close() //nolint:errcheck
				return
			}
		}
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close() //nolint:errcheck
				continue
			}
			go pipe(u, c)
			go pipe(c, u)
		}
	}()
	return l.Addr().String()
}

// TestKeepRootsLetsSlowFillsFinish publishes Roots faster than a subscriber on
// a slow link fills them. Keeping only the newest Root deletes the objects of
// the Root being filled, the request for them waits out the response timeout,
// and the subscriber never catches up. Keeping enough Roots lets it.
func TestKeepRootsLetsSlowFillsFinish(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	pk, err := sk.PubKey()
	require.NoError(t, err)
	pub, err := NewWithTCP("127.0.0.1:0", sk, PubConfig{InMemoryDB: true, KeepRoots: 20})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	addr := slowProxy(t, pub.Node().TCP().Address(), 20*time.Millisecond)

	cs := newChurnSub()
	sub, err := NewSubscriberTCP("", pk, SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	sub.OnUpdate(cs.apply)

	const leaves, changed = 200, 60
	put := func(from, n int) {
		for i := from; i < from+n; i++ {
			v := make([]byte, 1024)
			_, _ = rand.Read(v) //nolint:errcheck
			require.NoError(t, pub.Put(fmt.Sprintf("tp/%04d", i%leaves), v))
		}
		require.NoError(t, pub.Flush())
	}
	put(0, leaves)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, addr))
	_, ok := cs.waitConverge(leaves, 30*time.Second)
	require.True(t, ok, "seed set must converge")

	for i, end := 0, time.Now().Add(5*time.Second); time.Now().Before(end); i++ {
		put(i*changed, changed)
		time.Sleep(250 * time.Millisecond)
	}
	require.NoError(t, pub.Put("done", []byte("x")))
	require.NoError(t, pub.Flush())
	require.Eventually(t, func() bool { return cs.has("done") }, 40*time.Second, 20*time.Millisecond,
		"subscriber never caught up to the last Root")
}
