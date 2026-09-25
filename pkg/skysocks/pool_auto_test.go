package skysocks

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// TestPoolCeilingFollowsTheRouteBound: --standby-pool -1 holds one tunnel per
// disjoint route to the exit, never past pool.size_cap, with the cap standing
// in until the visor has counted the routes. A fixed ceiling ignores both.
func TestPoolCeilingFollowsTheRouteBound(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	ceiling := func(c *Client) int {
		c.redialMu.Lock()
		defer c.redialMu.Unlock()
		return c.poolCeilingLocked()
	}

	c := &Client{}
	c.SetStandbyPool(-1)
	require.Equal(t, 32, ceiling(c), "no count yet: the cap stands in")

	c.applyRouteBound(20)
	require.Equal(t, 20, ceiling(c), "the pool holds one tunnel per disjoint route")
	c.applyRouteBound(0)
	require.Equal(t, 20, ceiling(c), "an uncounted snapshot keeps the last count")

	c.applyRouteBound(274)
	require.Equal(t, 32, ceiling(c), "never past pool.size_cap")
	require.True(t, skysettings.Apply(map[string]int64{skysettings.PoolSizeCap: 300}))
	require.Equal(t, 274, ceiling(c), "the cap is live")

	c.SetStandbyPool(32)
	require.Equal(t, 32, ceiling(c), "a fixed ceiling ignores the count")
	c.SetStandbyPool(0)
	require.Equal(t, 0, ceiling(c), "0 still disables the pool")
}
