// Package transport pkg/transport/manager_inbound.go c2-net-transport
package transport

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
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

// InboundGate reports whether a new inbound transport from remote, dialed
// with label, is taken.
type InboundGate func(remote cipher.PubKey, label Label) bool

// ErrInboundRefused is returned when an inbound gate turns a transport away.
var ErrInboundRefused = errors.New("inbound transport refused")

// SetInboundGate sets the check a new inbound transport must pass during the
// settlement handshake. Reconnects of an existing transport are not checked.
// A nil gate takes everything.
func (tm *Manager) SetInboundGate(gate InboundGate) {
	if gate == nil {
		tm.inboundGate.Store(nil)
		return
	}
	tm.inboundGate.Store(&gate)
}

// CreateRestriction narrows outbound transport creation on top of
// ManagerConfig. The visor's metered modes set it.
type CreateRestriction int32

const (
	// RestrictNone leaves creation to ManagerConfig.
	RestrictNone CreateRestriction = iota
	// RestrictDirect refuses every direct type, as NoDirectTransports does.
	RestrictDirect
	// RestrictAll refuses every type, dmsg included.
	RestrictAll
)

// SetCreateRestriction applies r to every later creation attempt.
func (tm *Manager) SetCreateRestriction(r CreateRestriction) { tm.restrict.Store(int32(r)) }

// CloseDirectTransports deletes every direct transport, inbound ones too, and
// returns how many it closed. dmsg transports stay.
func (tm *Manager) CloseDirectTransports() int {
	var ids []uuid.UUID
	tm.WalkTransports(func(tp *ManagedTransport) bool {
		if types.IsDirect(types.NormalizeType(tp.Type())) {
			ids = append(ids, tp.Entry.ID)
		}
		return true
	})
	for _, id := range ids {
		tm.DeleteTransport(id)
	}
	return len(ids)
}
