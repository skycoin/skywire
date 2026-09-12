// Package dmsg pkg/dmsg/dmsg/relay_share_test.go c1-net-dmsg
package dmsg

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// The global relay budget is one counter, so without a per-peer share the
// first peer to open every slot starves every other peer on the hub — and the
// victims see refused streams that look like unreachable services rather than
// a busy relay.
func TestRelaySlotPerPeerShare(t *testing.T) {
	c := &EntityCommon{maxRelayedStreams: 8} // share = 8/4 = 2
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()

	require.True(t, c.tryAcquireRelaySlot(a))
	require.True(t, c.tryAcquireRelaySlot(a))
	require.False(t, c.tryAcquireRelaySlot(a), "a peer must not exceed its share")

	// The budget is NOT exhausted — a starves only itself.
	require.True(t, c.tryAcquireRelaySlot(b), "another peer still gets its share")
	require.True(t, c.tryAcquireRelaySlot(b))
	require.False(t, c.tryAcquireRelaySlot(b))

	require.Equal(t, int64(4), c.relayedStreams, "four live streams across two peers")

	// Releasing frees the share again.
	c.releaseRelaySlot(a)
	require.True(t, c.tryAcquireRelaySlot(a))
}

// The map tracks LIVE relaying, not every peer ever seen, or a long-running
// hub accumulates an entry per peer forever.
func TestRelayShareForgetsIdlePeers(t *testing.T) {
	c := &EntityCommon{maxRelayedStreams: 8}
	pk, _ := cipher.GenerateKeyPair()

	require.True(t, c.tryAcquireRelaySlot(pk))
	require.True(t, c.tryAcquireRelaySlot(pk))
	c.releaseRelaySlot(pk)
	c.releaseRelaySlot(pk)

	c.relayShareMx.Lock()
	n := len(c.relayShare)
	c.relayShareMx.Unlock()
	require.Zero(t, n, "a peer holding no slots must leave no entry behind")
	require.Zero(t, c.relayedStreams)
}

// A cap too small to divide must still relay something: a share that rounds
// to zero would refuse every stream and read as a broken relay.
func TestRelayShareNeverRoundsToZero(t *testing.T) {
	c := &EntityCommon{maxRelayedStreams: 2}
	pk, _ := cipher.GenerateKeyPair()
	require.Equal(t, int64(1), c.maxRelayedStreamsPerPeer())
	require.True(t, c.tryAcquireRelaySlot(pk))
	require.False(t, c.tryAcquireRelaySlot(pk))
}

// Relaying unconfigured still refuses rather than relaying unbounded.
func TestRelayShareUnconfiguredRefuses(t *testing.T) {
	c := &EntityCommon{}
	pk, _ := cipher.GenerateKeyPair()
	require.False(t, c.tryAcquireRelaySlot(pk))
}
