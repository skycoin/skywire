// Package visor pkg/visor/service_health_cache.go c3-vis-core
//
// A phone's Home asks for the service health every 4 s, and every answer
// probed all the deployment services and dmsg servers over the network. With
// a positive serviceHealthMaxAge (the mobile build) callers share one probe
// until it is that old.
package visor

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

type healthCache struct {
	mu   sync.Mutex
	at   time.Time
	last []visorapi.ServiceHealthEntry
}

// get returns the last probe while it is younger than maxAge, else probes. A
// probe that could not reach the services yet (complete false) is not kept.
func (c *healthCache) get(maxAge time.Duration, now func() time.Time, probe func() ([]visorapi.ServiceHealthEntry, bool)) []visorapi.ServiceHealthEntry {
	if maxAge <= 0 {
		entries, _ := probe()
		return entries
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && now().Sub(c.at) < maxAge {
		return append([]visorapi.ServiceHealthEntry(nil), c.last...)
	}
	entries, complete := probe()
	if complete {
		c.last, c.at = entries, now()
	}
	return entries
}
