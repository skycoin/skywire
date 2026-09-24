// Package skysocks pkg/skysocks/pool_auto.go
//
// Auto-sizing the standby pool (--standby-pool -1, the default).
//
// A fixed ceiling is wrong both ways: on a visor with a handful of transports
// the fill stops at exhaustion long before it, and on a well-connected one it
// stops a pool that could hold every disjoint route to the exit. The number
// that actually bounds the pool is the topology's: how many routes to the exit
// share no intermediate and no first hop — one per intermediate this visor and
// the exit both hold a transport to, plus the direct transports. The visor
// counts that from the route oracle's candidate set and reports it on the
// proxy-status snapshot the client already polls; the pool follows it, never
// past pool.size_cap, because every standby tunnel is a route group on the
// exit too.
package skysocks

// poolCeilingLocked is how many tunnels the pool may hold right now, active
// ones included. 0 disables the pool. Callers hold c.redialMu.
func (c *Client) poolCeilingLocked() int {
	if !c.poolAuto {
		return c.poolMax
	}
	limit := setPoolSizeCap()
	if c.poolRouteBound > 0 && c.poolRouteBound < limit {
		return c.poolRouteBound
	}
	return limit
}

// applyRouteBound installs the disjoint-route count the visor reported for this
// client's exit. 0 means the visor has not counted them yet (no oracle query
// has run) and leaves the last count in place. A count that RAISES the ceiling
// re-arms a fill that had settled at the old one.
func (c *Client) applyRouteBound(n int) {
	if n <= 0 {
		return
	}
	c.redialMu.Lock()
	before := c.poolCeilingLocked()
	c.poolRouteBound = n
	raised := c.poolAuto && c.poolCeilingLocked() > before
	c.redialMu.Unlock()
	if raised {
		c.armPoolFillAfter(0)
	}
}
