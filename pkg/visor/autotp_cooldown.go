// Package visor pkg/visor/autotp_cooldown.go
package visor

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// An automatic transport of a type that just failed to a peer is not tried
// again at once: every route setup to an unreachable peer used to repeat the
// whole attempt, each try resolving the peer at the address resolver. The
// wait starts at autoTpCooldownMin and doubles per failure up to
// autoTpCooldownMax; a success clears it.
const (
	autoTpCooldownMin = 2 * time.Minute
	autoTpCooldownMax = 30 * time.Minute
)

type autoTpFailure struct {
	until time.Time
	fails int
}

type autoTpCooldown struct {
	mu sync.Mutex
	m  map[string]autoTpFailure
}

func autoTpKey(pk cipher.PubKey, t types.Type) string { return pk.Hex() + "/" + string(t) }

// blocked reports whether t to pk failed recently enough to skip it now.
func (c *autoTpCooldown) blocked(pk cipher.PubKey, t types.Type, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.m[autoTpKey(pk, t)]
	return ok && now.Before(f.until)
}

// failed records a failed attempt and lengthens the wait.
func (c *autoTpCooldown) failed(pk cipher.PubKey, t types.Type, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]autoTpFailure)
	}
	if len(c.m) > 8192 {
		for k, f := range c.m {
			if now.After(f.until) {
				delete(c.m, k)
			}
		}
	}
	k := autoTpKey(pk, t)
	f := c.m[k]
	f.fails++
	wait := autoTpCooldownMin << (f.fails - 1)
	if wait > autoTpCooldownMax || wait <= 0 {
		wait = autoTpCooldownMax
	}
	f.until = now.Add(wait)
	c.m[k] = f
}

// succeeded forgets past failures of t to pk.
func (c *autoTpCooldown) succeeded(pk cipher.PubKey, t types.Type) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, autoTpKey(pk, t))
}
