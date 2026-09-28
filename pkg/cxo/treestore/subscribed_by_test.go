// Package treestore pkg/cxo/treestore/subscribed_by_test.go: SubscribedBy
// turns true once a peer holds a subscription to the feed, and only for it.
package treestore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestPublisherSubscribedBy(t *testing.T) {
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

	require.False(t, pub.SubscribedBy(subPK))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, pub.Node().TCP().Address()))

	require.Eventually(t, func() bool { return pub.SubscribedBy(subPK) }, 10*time.Second, 10*time.Millisecond)
	require.False(t, pub.SubscribedBy(stranger))
}
