// Package treestore — publisher_heartbeat_test.go: the quiet-feed
// keepalive. runLoop re-publishes the current Root every heartbeatInterval
// on a feed with no changes so subscribers keep receiving an inbound Root
// inside the CXO idle-watchdog window and don't tear down + re-dial (a
// full noise+PQ handshake). This guards the core invariant:
// publishHeartbeat re-publishes a CLEAN tree, while publishIfDirty does not.
package treestore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestPublisherHeartbeatRepublishesCleanTree(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	pub, err := NewWithTCP("127.0.0.1:0", sk, PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
	})
	require.NoError(t, err, "NewWithTCP")
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	// A real change → runLoop publishes it and stamps lastPublishNs.
	require.NoError(t, pub.Put("k", []byte("v")), "Put")
	require.Eventually(t, func() bool { return pub.lastPublishNs.Load() != 0 },
		5*time.Second, 5*time.Millisecond, "initial publish should stamp lastPublishNs")

	// Tree is now clean (the change was published, nothing new). The
	// dirty-path publish must be a no-op — no re-emit, clock unchanged.
	before := pub.lastPublishNs.Load()
	require.NoError(t, pub.publishIfDirty(), "publishIfDirty on clean tree")
	require.Equal(t, before, pub.lastPublishNs.Load(),
		"publishIfDirty must NOT re-publish a clean tree")

	// The heartbeat re-publishes even though nothing changed: a new Root
	// is emitted (broadcast to subscribers) and the clock advances. This
	// is what keeps a quiet feed's subscriber connections alive.
	time.Sleep(2 * time.Millisecond) // ensure a distinct UnixNano stamp
	require.NoError(t, pub.publishHeartbeat(), "publishHeartbeat on clean tree")
	require.Greater(t, pub.lastPublishNs.Load(), before,
		"publishHeartbeat must re-publish a clean tree (quiet-feed keepalive)")
}
