package transport

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TransportEvent is one entry in the manager's bounded history of transport
// opens and closes, with the reason a close happened. It exists because the
// question "why did that transport go away" had no answer: the visor log
// ring holds a few minutes, and a transport that was there at one reading of
// `tp ls` and gone at the next left nothing behind. `visor state --select
// diag` carries the last TransportEventRingSize of these.
type TransportEvent struct {
	At        time.Time     `json:"at"`
	Event     string        `json:"event"` // "open" or "close"
	ID        uuid.UUID     `json:"id"`
	Type      string        `json:"type"`
	Remote    cipher.PubKey `json:"remote"`
	Label     Label         `json:"label"`
	Initiator bool          `json:"initiator"`
	// Reason is set on a close: what decided it, in the words of the code that
	// did ("read: EOF", "no pong for 3 consecutive pings", "deleted by
	// request", …). Empty on an open.
	Reason string `json:"reason,omitempty"`
	// AgeS is how long the transport had been open when it closed.
	AgeS float64 `json:"age_s,omitempty"`
}

// TransportEventRingSize is how many events the manager keeps.
const TransportEventRingSize = 256

type tpEventRing struct {
	mu   sync.Mutex
	buf  []TransportEvent
	next int
	full bool
}

func (r *tpEventRing) add(e TransportEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buf == nil {
		r.buf = make([]TransportEvent, TransportEventRingSize)
	}
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// snapshot returns the events oldest first.
func (r *tpEventRing) snapshot() []TransportEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buf == nil {
		return nil
	}
	if !r.full {
		return append([]TransportEvent(nil), r.buf[:r.next]...)
	}
	out := make([]TransportEvent, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	out = append(out, r.buf[:r.next]...)
	return out
}

// Events returns the manager's transport open/close history, oldest first.
func (tm *Manager) Events() []TransportEvent {
	return tm.events.snapshot()
}

// track records mt's open and arranges for its close, with reason, to be
// recorded too. Called wherever a transport enters tm.tps.
func (tm *Manager) track(mt *ManagedTransport) {
	if mt == nil {
		return
	}
	if mt.openedAt.IsZero() {
		mt.openedAt = time.Now()
	}
	tm.events.add(TransportEvent{
		At: mt.openedAt, Event: "open", ID: mt.Entry.ID, Type: string(mt.Entry.Type),
		Remote: mt.rPK, Label: mt.Entry.Label, Initiator: mt.isInitiator,
	})
	mt.onClose = func(reason string) {
		tm.events.add(TransportEvent{
			At: time.Now(), Event: "close", ID: mt.Entry.ID, Type: string(mt.Entry.Type),
			Remote: mt.rPK, Label: mt.Entry.Label, Initiator: mt.isInitiator,
			Reason: reason, AgeS: time.Since(mt.openedAt).Seconds(),
		})
	}
}
