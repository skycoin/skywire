// Package transport pkg/transport/manager_inbound.go c2-net-transport
package transport

import (
	"sync"
	"time"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// inboundTimes records, per transport type, when an inbound transport was
// last accepted. The zero value is ready to use.
type inboundTimes struct {
	mu sync.Mutex
	at map[types.Type]time.Time
}

func (in *inboundTimes) note(t types.Type, now time.Time) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.at == nil {
		in.at = make(map[types.Type]time.Time)
	}
	in.at[types.NormalizeType(t)] = now
}

// LastInbound returns, per transport type, when this visor last accepted an
// inbound transport of it. Types never accepted are absent.
func (tm *Manager) LastInbound() map[types.Type]time.Time {
	tm.lastInbound.mu.Lock()
	defer tm.lastInbound.mu.Unlock()
	out := make(map[types.Type]time.Time, len(tm.lastInbound.at))
	for t, at := range tm.lastInbound.at {
		out[t] = at
	}
	return out
}
