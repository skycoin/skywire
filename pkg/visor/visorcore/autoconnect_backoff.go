// Package visorcore pkg/visor/visorcore/autoconnect_backoff.go c3-vis-core
//
// Per-target dial backoff for the autoconnect Connector. Offline public
// visors stay in the discovery lists for a long time, and without this every
// visor on the network re-dialed the same dead keys each cycle — several
// address-resolver lookups per attempt, fleet-wide (a third of the AR's
// load on 2026-09-10 was this plus self-lookups). The wait doubles per
// consecutive failure from dialBackoffMin up to dialBackoffMax and is
// forgotten on the first success. Keyed per (target, type): a visor that is
// unreachable over one carrier may still answer on another.
package visorcore

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

type dialKey struct {
	pk  cipher.PubKey
	typ tptypes.Type
}

type dialFailure struct {
	until time.Time
	n     int
}

const (
	dialBackoffMin = 5 * time.Minute
	dialBackoffMax = 2 * time.Hour
)

// dialBackoff is the wait after the n-th consecutive failure (n >= 1).
func dialBackoff(n int) time.Duration {
	d := dialBackoffMin
	for i := 1; i < n && d < dialBackoffMax; i++ {
		d *= 2
	}
	if d > dialBackoffMax {
		return dialBackoffMax
	}
	return d
}

// inBackoff reports whether pk/typ failed recently enough to skip this cycle.
func (c *Connector) inBackoff(pk cipher.PubKey, typ tptypes.Type, now time.Time) bool {
	c.failedMu.Lock()
	defer c.failedMu.Unlock()
	f, ok := c.failed[dialKey{pk, typ}]
	return ok && now.Before(f.until)
}

// noteFailure extends the backoff for pk/typ after a failed dial and returns
// the new wait.
func (c *Connector) noteFailure(pk cipher.PubKey, typ tptypes.Type, now time.Time) time.Duration {
	c.failedMu.Lock()
	defer c.failedMu.Unlock()
	if c.failed == nil {
		c.failed = make(map[dialKey]dialFailure)
	}
	k := dialKey{pk, typ}
	f := c.failed[k]
	f.n++
	wait := dialBackoff(f.n)
	f.until = now.Add(wait)
	c.failed[k] = f
	return wait
}

// noteSuccess forgets any backoff for pk/typ.
func (c *Connector) noteSuccess(pk cipher.PubKey, typ tptypes.Type) {
	c.failedMu.Lock()
	delete(c.failed, dialKey{pk, typ})
	c.failedMu.Unlock()
}

// BackedOff returns how many (target, type) pairs are currently skipped.
func (c *Connector) BackedOff(now time.Time) int {
	c.failedMu.Lock()
	defer c.failedMu.Unlock()
	n := 0
	for _, f := range c.failed {
		if now.Before(f.until) {
			n++
		}
	}
	return n
}
