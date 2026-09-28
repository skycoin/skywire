// Package treestore pkg/cxo/treestore/subscription_test.go: Subscriptions finds
// the conn once a peer subscribes to the feed, and only for that peer.
package treestore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestPublisherSubscriptions(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()
	pub, err := NewWithTCP("127.0.0.1:0", skA, PubConfig{InMemoryDB: true, BatchWindow: 5 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	require.NoError(t, pub.Put("k", []byte("v")))

	sub, err := NewSubscriberTCP("", pkA, SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	subPK := cipher.PubKey(sub.cxoNode.ID())
	stranger, _ := cipher.GenerateKeyPair()

	require.Empty(t, pub.Subscriptions(subPK))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, pub.Node().TCP().Address()))

	require.Eventually(t, func() bool { return len(pub.Subscriptions(subPK)) == 1 }, 10*time.Second, 10*time.Millisecond)
	require.Empty(t, pub.Subscriptions(stranger))
}
