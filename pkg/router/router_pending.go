// Package router pkg/router/router_pending.go c2-net-routing
package router

import (
	"context"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// Receive-side setup-race mitigation.
//
// AcceptRoutes / DialRoutes save the routing rules BEFORE saveRouteGroupRules
// registers the route group, so a handshake or ping frame for a freshly
// installed route can arrive in that window: the frame's routing rule resolves,
// but no route group exists yet, so dispatchToRouteGroup would return
// errRouteDescNotExist and DROP it. Dropping the noise handshake's first frame
// stalls the handshake — the initiator then times out at handshakeAwaitTimeout
// and futilely retries through a different intermediate, which on a loaded node
// turns a sub-second multihop setup into a multi-second or timed-out one (the
// rare, intermediate-agnostic, busy-node-concentrated failures).
//
// Instead of dropping, we park such frames briefly and re-dispatch them the
// moment the route group registers.
const (
	// pendingPacketTTL must exceed the registration window but stay well under
	// handshakeAwaitTimeout (see router.go) so a parked handshake frame is
	// re-delivered while the initiator is still waiting.
	pendingPacketTTL     = 2 * time.Second
	pendingSweepInterval = time.Second
	// Memory bounds: a flood of frames for descriptors that never register
	// must not grow the buffer without limit.
	maxPendingPerDesc = 16
	maxPendingTotal   = 1024
)

type pendingPacket struct {
	pkt      routing.Packet
	deadline time.Time
}

// pendingPackets buffers transport frames whose routing rule exists but whose
// route group has not registered yet (the receive-side setup race). It is safe
// for concurrent use.
type pendingPackets struct {
	mu     sync.Mutex
	byDesc map[routing.RouteDescriptor][]pendingPacket
	total  int
}

func newPendingPackets() *pendingPackets {
	return &pendingPackets{byDesc: make(map[routing.RouteDescriptor][]pendingPacket)}
}

// park stores pkt for desc until the route group registers (take) or the TTL
// expires (sweep). Returns false when the buffer is full, in which case the
// caller should fall back to dropping the frame.
func (p *pendingPackets) park(desc routing.RouteDescriptor, pkt routing.Packet, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total >= maxPendingTotal || len(p.byDesc[desc]) >= maxPendingPerDesc {
		return false
	}
	p.byDesc[desc] = append(p.byDesc[desc], pendingPacket{pkt: pkt, deadline: now.Add(pendingPacketTTL)})
	p.total++
	return true
}

// take removes and returns all packets parked for desc. Called when the route
// group for desc registers.
func (p *pendingPackets) take(desc routing.RouteDescriptor) []pendingPacket {
	p.mu.Lock()
	defer p.mu.Unlock()
	pkts := p.byDesc[desc]
	if len(pkts) == 0 {
		return nil
	}
	delete(p.byDesc, desc)
	p.total -= len(pkts)
	return pkts
}

// sweep drops packets past their deadline and returns the count dropped.
func (p *pendingPackets) sweep(now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	dropped := 0
	for desc, pkts := range p.byDesc {
		kept := pkts[:0]
		for _, pp := range pkts {
			if now.Before(pp.deadline) {
				kept = append(kept, pp)
			} else {
				dropped++
			}
		}
		if len(kept) == 0 {
			delete(p.byDesc, desc)
		} else {
			p.byDesc[desc] = kept
		}
	}
	p.total -= dropped
	return dropped
}

// runSweep periodically drops expired parked packets until ctx is done. Parked
// frames are normally taken within milliseconds (on route-group registration);
// the sweep only reclaims frames for descriptors that never register.
func (p *pendingPackets) runSweep(ctx context.Context, log *logging.Logger) {
	t := time.NewTicker(pendingSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n := p.sweep(now); n > 0 && log != nil {
				log.WithField("dropped", n).Debug("Dropped parked packets whose route group never registered")
			}
		}
	}
}
