package dmsg

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestRelaySlotCap covers the always-on relay's stream-capacity bound:
// tryAcquireRelaySlot admits up to maxRelayedStreams concurrent slots and
// refuses beyond that, releaseRelaySlot frees a slot for reuse, and an
// unconfigured cap (client entities) refuses to relay at all.
//
// Each acquire uses a DISTINCT peer, because the global budget is also shared
// per-peer now (relayPeerShareDivisor): one peer filling the whole cap is the
// starvation this test would otherwise be asserting is fine. The per-peer
// half is covered in relay_share_test.go.
func TestRelaySlotCap(t *testing.T) {
	peer := func() cipher.PubKey { pk, _ := cipher.GenerateKeyPair(); return pk }

	// Unconfigured cap: never relay.
	var client EntityCommon
	require.False(t, client.tryAcquireRelaySlot(peer()), "unset cap must not relay")

	c := EntityCommon{maxRelayedStreams: 3}

	// Fill to capacity, one slot per peer.
	require.True(t, c.tryAcquireRelaySlot(peer()))
	require.True(t, c.tryAcquireRelaySlot(peer()))
	last := peer()
	require.True(t, c.tryAcquireRelaySlot(last))
	require.False(t, c.tryAcquireRelaySlot(peer()), "at capacity, further acquires fail")

	// Releasing one slot admits exactly one more.
	c.releaseRelaySlot(last)
	require.True(t, c.tryAcquireRelaySlot(peer()), "a freed slot is reusable")
	require.False(t, c.tryAcquireRelaySlot(peer()), "back at capacity")

	// The live count never overshoots the cap under concurrency.
	c2 := EntityCommon{maxRelayedStreams: 8}
	var granted int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c2.tryAcquireRelaySlot(peer()) {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int64(8), granted, "exactly cap slots granted, no overshoot")
	require.Equal(t, int64(8), c2.relayedStreams, "live count equals granted slots")
}
