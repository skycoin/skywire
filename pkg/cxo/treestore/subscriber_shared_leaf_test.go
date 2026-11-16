package treestore

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestSubscriberCacheSharesStoredObjects checks that a filled leaf is held
// once: the cache value points into the CXO store's object, WalkShared hands
// out that same memory, and its capacity stops at its length.
func TestSubscriberCacheSharesStoredObjects(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()
	pub, err := NewWithTCP("127.0.0.1:0", skA, PubConfig{InMemoryDB: true, BatchWindow: 5 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	sub, err := NewSubscriberTCP("", pkA, SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck

	changed := make(chan struct{}, 1)
	sub.OnChange(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, pub.Node().TCP().Address()))

	want := bytes.Repeat([]byte("leaf"), 1024)
	require.NoError(t, pub.Put("day/2026-11-15", want))
	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("no change notification")
	}

	var got []byte
	sub.WalkShared("day/", func(_ string, v []byte) bool {
		got = v
		return false
	})
	require.Equal(t, want, got)
	require.Equal(t, len(got), cap(got), "an append must not write into the stored object")

	inStore := false
	err = sub.cxoNode.Container().DB().CXDS().Iterate(func(_ skycipher.SHA256, _ uint32, val []byte) error {
		// Leaf is the last field of a TreeEntry, so it is the object's tail.
		if len(val) >= len(got) && &val[len(val)-len(got)] == &got[0] {
			inStore = true
		}
		return nil
	})
	require.NoError(t, err)
	require.True(t, inStore, "cache value is a copy, not the stored object")
}
