package dmsg

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRelaySlotCap covers the always-on relay's stream-capacity bound:
// tryAcquireRelaySlot admits up to maxRelayedStreams concurrent slots and
// refuses beyond that, releaseRelaySlot frees a slot for reuse, and an
// unconfigured cap (client entities) refuses to relay at all.
func TestRelaySlotCap(t *testing.T) {
	// Unconfigured cap: never relay.
	var client EntityCommon
	require.False(t, client.tryAcquireRelaySlot(), "unset cap must not relay")

	c := EntityCommon{maxRelayedStreams: 3}

	// Fill to capacity.
	require.True(t, c.tryAcquireRelaySlot())
	require.True(t, c.tryAcquireRelaySlot())
	require.True(t, c.tryAcquireRelaySlot())
	require.False(t, c.tryAcquireRelaySlot(), "at capacity, further acquires fail")

	// Releasing one slot admits exactly one more.
	c.releaseRelaySlot()
	require.True(t, c.tryAcquireRelaySlot(), "a freed slot is reusable")
	require.False(t, c.tryAcquireRelaySlot(), "back at capacity")

	// The live count never overshoots the cap under concurrency.
	c2 := EntityCommon{maxRelayedStreams: 8}
	var granted int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c2.tryAcquireRelaySlot() {
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
