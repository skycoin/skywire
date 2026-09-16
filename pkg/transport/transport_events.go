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

// TransportEventRingSize is how many events the manager keeps. A public visor
// is a magnet for autoconnect: on one exit visor 256 entries spanned under
// three minutes (130 stcpr opens, 55 squicr, 41 sudph, 28 webrtc, 2 dmsg),
// so the one close that was being chased had already rolled out of the ring
// by the time anyone read it. 2048 entries cost a few hundred KiB and buy
// roughly half an hour on that same visor.
const TransportEventRingSize = 2048

// TransportLastCloseMax is how many transports the manager remembers the last
// close of. The ring answers "what has been happening"; this answers "why did
// transport X die", and it has to survive any amount of open churn — opens
// never evict a close here, only closes of 1024 other transports do.
const TransportLastCloseMax = 1024

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

// tpLastCloseMap is the last close event per transport id, bounded at
// TransportLastCloseMax and evicting the transport whose close was recorded
// longest ago. Re-closing a transport already in the map overwrites its entry
// and leaves its position, so a flapping transport cannot push the rest out.
type tpLastCloseMap struct {
	mu sync.Mutex
	m  map[uuid.UUID]TransportEvent
	// ids is the insertion order, oldest first, with exactly one entry per
	// key in m.
	ids []uuid.UUID
}

func (c *tpLastCloseMap) add(e TransportEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[uuid.UUID]TransportEvent)
	}
	if _, ok := c.m[e.ID]; !ok {
		for len(c.ids) >= TransportLastCloseMax {
			delete(c.m, c.ids[0])
			c.ids = c.ids[1:]
		}
		c.ids = append(c.ids, e.ID)
	}
	c.m[e.ID] = e
}

// snapshot returns a copy of the map.
func (c *tpLastCloseMap) snapshot() map[uuid.UUID]TransportEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) == 0 {
		return nil
	}
	out := make(map[uuid.UUID]TransportEvent, len(c.m))
	for id, e := range c.m {
		out[id] = e
	}
	return out
}

// Events returns the manager's transport open/close history, oldest first.
func (tm *Manager) Events() []TransportEvent {
	return tm.events.snapshot()
}

// LastCloses returns the last close event of each of the last
// TransportLastCloseMax transports that closed, keyed by transport id. Unlike
// Events it is not flooded out by open churn: on a public visor every peer's
// autoconnect keeps opening transports, and the one close being chased was
// gone from the ring minutes later.
func (tm *Manager) LastCloses() map[uuid.UUID]TransportEvent {
	return tm.lastCloses.snapshot()
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
		ev := TransportEvent{
			At: time.Now(), Event: "close", ID: mt.Entry.ID, Type: string(mt.Entry.Type),
			Remote: mt.rPK, Label: mt.Entry.Label, Initiator: mt.isInitiator,
			Reason: reason, AgeS: time.Since(mt.openedAt).Seconds(),
		}
		tm.events.add(ev)
		tm.lastCloses.add(ev)
	}
}
