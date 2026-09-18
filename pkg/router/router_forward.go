// Package router pkg/router/router_forward.go c2-net-routing
//
// The TRANSIT write path: what this visor does with a frame it only relays.
//
// forwardPacket used to end in a SYNCHRONOUS tp.WritePacket on the router's
// single inbound packet loop, with the router's serve context — which never
// cancels. managedTransport.writeTo takes the transport write lock and only
// then sets a deadline of one minute, so ONE peer that stopped draining (a
// stranger routing through us, whose frames are the cheapest thing on the
// visor to drop) parked the loop that feeds EVERY route group. Measured live
// on 2026-09-18: a ~30 s all-paths blackout on the local visor, every
// transport logging "Dropping packet: readCh full for 30s (application not
// reading)" while the goroutine dump showed the loop in writeTo for a transit
// peer. The same class as #5018, which took the route-group READ side off this
// loop with a per-group intake worker; this is the write side.
//
// So: the loop hands the frame to a bounded per-next-hop-transport queue and
// returns. One writer goroutine per transport drains it — which also preserves
// the ordering the shared write lock used to give — and each write carries
// forward.write_timeout as its own deadline. When the peer does not drain, the
// queue fills and frames are DROPPED with a counted, named reason
// (forward_drop_queue_full / forward_drop_write_timeout), visible in
// `visor state --select diag` as intake.forward_queues and, once per
// forward.drop_event_window per transport, as a forward_drops mux event.
package router

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// The named drop reasons. They are counter names, not free text: a bench run
// greps for them and `visor state --select diag` reports them per transport.
const (
	forwardDropQueueFull    = "forward_drop_queue_full"
	forwardDropWriteTimeout = "forward_drop_write_timeout"
	forwardDropWriteError   = "forward_drop_write_error"
)

// MuxEventForwardDrops is recorded the first time a next-hop transport loses
// transit frames inside a forward.drop_event_window — a whole-transport event
// (LegIndex -1) naming the peer and the counters, so a wedged transit peer is
// readable from `visor state` long after the log ring has rolled.
const MuxEventForwardDrops = "forward_drops"

// forwardWriterIdleTimeout retires a writer goroutine whose transport has not
// relayed anything for a while, so a visor that transits for many short-lived
// peers does not accumulate one goroutine per peer for its whole uptime.
const forwardWriterIdleTimeout = 2 * time.Minute

// forwardTable holds one writer per next-hop transport. Zero value ready: a
// router built by a test never pays for the map.
type forwardTable struct {
	mu sync.Mutex
	m  map[uuid.UUID]*forwardWriter
}

// forwardFrame is one relayed frame plus the rule whose activity it refreshes
// once it is actually on the wire.
type forwardFrame struct {
	tp  *transport.ManagedTransport
	p   routing.Packet
	key routing.RouteID
}

// forwardWriter is one next-hop transport's queue and the goroutine draining
// it. Counters are atomics so IntakeStats can read them without touching the
// table lock the send path holds.
type forwardWriter struct {
	r      *router
	tpID   uuid.UUID
	tpType string
	remote cipher.PubKey
	ch     chan forwardFrame

	sent              atomic.Uint64
	dropQueueFull     atomic.Uint64
	dropWriteTimeout  atomic.Uint64
	dropWriteError    atomic.Uint64
	lastEventUnixNano atomic.Int64
}

// forwardWrite hands a relayed frame to its next hop's queue and returns. It
// NEVER blocks: a full queue drops the frame, which is the whole point — the
// alternative is parking the visor's only inbound packet loop on one peer.
//
// The dropped frame is not reported as a serve-loop error: that would log once
// per packet for as long as the peer stays wedged. It is counted, and the
// first drop in a window earns one mux event.
func (r *router) forwardWrite(tp *transport.ManagedTransport, p routing.Packet, key routing.RouteID) {
	w := r.forward.enqueue(r, tp, forwardFrame{tp: tp, p: p, key: key})
	if w == nil {
		return
	}
	n := w.dropQueueFull.Add(1)
	w.noteDrops(forwardDropQueueFull, fmt.Sprintf("%d frames queued and undrained", n))
}

// enqueue looks the writer up (creating it on first use) and makes ONE
// non-blocking send, both under the table lock so a writer cannot retire
// itself between the lookup and the send. Returns nil when the frame was
// accepted, and the writer that refused it otherwise.
func (t *forwardTable) enqueue(r *router, tp *transport.ManagedTransport, f forwardFrame) *forwardWriter {
	t.mu.Lock()
	if t.m == nil {
		t.m = make(map[uuid.UUID]*forwardWriter)
	}
	id := tp.Entry.ID
	w, ok := t.m[id]
	if !ok {
		depth := routersettings.ForwardQueueDepth.Int()
		if depth < 1 {
			depth = 1
		}
		w = &forwardWriter{
			r:      r,
			tpID:   id,
			tpType: string(tp.Entry.Type),
			remote: tp.Remote(),
			ch:     make(chan forwardFrame, depth),
		}
		t.m[id] = w
		go w.serve()
	}
	select {
	case w.ch <- f:
		t.mu.Unlock()
		return nil
	default:
		t.mu.Unlock()
		return w
	}
}

// retireIfIdle drops the writer from the table when its queue is empty. Under
// the same lock enqueue sends on, so no frame is ever left in a retired
// writer's queue.
func (t *forwardTable) retireIfIdle(w *forwardWriter) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(w.ch) > 0 {
		return false
	}
	if t.m[w.tpID] == w {
		delete(t.m, w.tpID)
	}
	return true
}

// snapshot is the per-transport view IntakeStats reports.
func (t *forwardTable) snapshot() []ForwardQueue {
	t.mu.Lock()
	ws := make([]*forwardWriter, 0, len(t.m))
	for _, w := range t.m {
		ws = append(ws, w)
	}
	t.mu.Unlock()
	out := make([]ForwardQueue, 0, len(ws))
	for _, w := range ws {
		out = append(out, ForwardQueue{
			TpID:              w.tpID,
			TpType:            w.tpType,
			Remote:            w.remote,
			Queue:             len(w.ch),
			Capacity:          cap(w.ch),
			Sent:              w.sent.Load(),
			DropsQueueFull:    w.dropQueueFull.Load(),
			DropsWriteTimeout: w.dropWriteTimeout.Load(),
			DropsWriteError:   w.dropWriteError.Load(),
		})
	}
	return out
}

// serve drains the queue, one frame at a time, for as long as the transport
// keeps relaying.
func (w *forwardWriter) serve() {
	idle := time.NewTimer(forwardWriterIdleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-w.r.done:
			return
		case f := <-w.ch:
			w.write(f)
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(forwardWriterIdleTimeout)
		case <-idle.C:
			if w.r.forward.retireIfIdle(w) {
				return
			}
			idle.Reset(forwardWriterIdleTimeout)
		}
	}
}

// write puts one frame on the wire under its own deadline. forward.write_timeout
// is what the transport charges the underlying conn (writeTo takes the smaller
// of it and the transport's own minute), so a peer that has stopped draining
// costs this one frame and this one goroutine's timeout — never the serve loop.
func (w *forwardWriter) write(f forwardFrame) {
	ctx, cancel := context.WithTimeout(context.Background(), routersettings.ForwardWriteTimeout.Duration())
	err := f.tp.WritePacket(ctx, f.p)
	timedOut := ctx.Err() != nil
	cancel()
	if err != nil {
		if timedOut {
			n := w.dropWriteTimeout.Add(1)
			w.noteDrops(forwardDropWriteTimeout, fmt.Sprintf("%d writes hit forward.write_timeout (%v)",
				n, routersettings.ForwardWriteTimeout.Duration()))
			return
		}
		n := w.dropWriteError.Add(1)
		w.noteDrops(forwardDropWriteError, fmt.Sprintf("%d writes failed, last: %v", n, err))
		return
	}
	w.sent.Add(1)
	// The rule's activity used to be refreshed on the serve loop right after
	// the synchronous write; it is refreshed here for the same reason, once
	// the frame is genuinely on the wire.
	if err := w.r.UpdateRuleActivity(f.key); err != nil && w.r.logger != nil {
		w.r.logger.Debugf("Failed to update activity for forwarded rule with route ID %d: %v", f.key, err)
	}
}

// noteDrops records at most one forward_drops mux event per transport per
// forward.drop_event_window. The counters carry the volume; the event carries
// the fact, with a timestamp, into `visor state`.
func (w *forwardWriter) noteDrops(reason, detail string) {
	window := routersettings.ForwardDropEventWindow.Duration()
	now := time.Now()
	last := w.lastEventUnixNano.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < window {
		return
	}
	if !w.lastEventUnixNano.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	if w.r.logger != nil {
		w.r.logger.WithField("tp_id", w.tpID).WithField("remote_pk", w.remote).
			WithField("reason", reason).
			Warnf("Dropping transit frames for a next hop that is not draining: %s", detail)
	}
	w.r.muxEvents.add(MuxEvent{
		At:       now,
		Event:    MuxEventForwardDrops,
		By:       MuxByLocal,
		LegIndex: -1,
		TpID:     w.tpID,
		TpType:   w.tpType,
		Remote:   w.remote,
		Reason:   reason + ": " + detail,
	})
}
