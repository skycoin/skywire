// Package router pkg/router/cascade_ack_registry.go
//
// ackRegistry is the shared correlation table that maps a cascade
// sessionID to the goroutine waiting for that session's CascadeAck.
//
// It is shared between the CascadeHandler (which receives ACK packets off
// the transport manager's single cascade-handler slot and dispatches them)
// and any CascadeBuilder that injects cascades on the same visor. On the
// SOURCE visor, the source-side CascadeBuilder sends reserve/install
// cascades down the route's transports, but the ACKs come back on those
// same transports and are delivered to the visor's one registered cascade
// handler. Sharing this registry lets the handler hand those ACKs to the
// waiting builder instead of needing a second handler slot.
package router

import (
	"sync"

	"github.com/skycoin/skywire/pkg/routing"
)

// ackRegistry correlates cascade sessionIDs to waiting receivers.
type ackRegistry struct {
	mu      sync.Mutex
	pending map[uint64]chan routing.Packet
}

// newAckRegistry creates an empty registry.
func newAckRegistry() *ackRegistry {
	return &ackRegistry{pending: make(map[uint64]chan routing.Packet)}
}

// register installs a wait channel for sessionID, returning it. The caller
// must eventually call unregister (directly or via a successful dispatch).
func (r *ackRegistry) register(sessionID uint64) chan routing.Packet {
	ch := make(chan routing.Packet, 1)
	r.mu.Lock()
	r.pending[sessionID] = ch
	r.mu.Unlock()
	return ch
}

// unregister removes any wait channel for sessionID.
func (r *ackRegistry) unregister(sessionID uint64) {
	r.mu.Lock()
	delete(r.pending, sessionID)
	r.mu.Unlock()
}

// dispatch hands the packet to the goroutine waiting on its sessionID, if
// any, removing the registration. Returns true if a waiter was found.
func (r *ackRegistry) dispatch(sessionID uint64, p routing.Packet) bool {
	r.mu.Lock()
	ch, ok := r.pending[sessionID]
	if ok {
		delete(r.pending, sessionID)
	}
	r.mu.Unlock()
	if ok {
		ch <- p
	}
	return ok
}
