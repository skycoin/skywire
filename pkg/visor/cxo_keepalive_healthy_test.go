// Package visor pkg/visor/cxo_keepalive_healthy_test.go: a recent announce is
// not enough to skip an HTTP keepalive; the service must be subscribed, and
// the epoch stays put while the subscription stays on one conn.
package visor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
)

func TestCXOKeepaliveHealthy(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{InMemoryDB: true, BatchWindow: 5 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	require.NoError(t, pub.Put("k", []byte("v")))

	sub, err := treestore.NewSubscriberTCP("", pk, treestore.SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	stranger, _ := cipher.GenerateKeyPair()
	lastOK := new(atomic.Int64)
	ok, _ := cxoKeepaliveHealthy(pub, stranger, lastOK)()
	require.False(t, ok, "never announced")
	lastOK.Store(time.Now().UnixNano())
	ok, _ = cxoKeepaliveHealthy(pub, stranger, lastOK)()
	require.False(t, ok, "announced but not subscribed")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, pub.Node().TCP().Address()))
	require.Eventually(t, func() bool { return len(pub.Node().Connections()) == 1 }, 10*time.Second, 10*time.Millisecond)
	health := cxoKeepaliveHealthy(pub, cipher.PubKey(pub.Node().Connections()[0].PeerID()), lastOK)
	require.Eventually(t, func() bool { ok, _ := health(); return ok }, 10*time.Second, 10*time.Millisecond)

	_, e1 := health()
	_, e2 := health()
	require.EqualValues(t, 1, e1, "first subscription conn")
	require.Equal(t, e1, e2, "same conn keeps the epoch")

	lastOK.Store(time.Now().Add(-cxoKeepaliveHealthyWindow).UnixNano())
	ok, _ = health()
	require.False(t, ok, "announces stopped")
}
