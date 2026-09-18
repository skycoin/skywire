// Package emu pkg/router/emu/netem.go
//
// Package emu is an in-process network emulator for the router's multipath
// legs. It provides a pair of connected endpoints that satisfy
// network.Transport — what a transport.ManagedTransport wraps — with
// per-direction one-way delay, jitter, loss, rate limiting, queueing,
// reordering and a "cut" switch.
//
// It emulates the WIRE only. There are no transport handshakes, no
// discovery, no NAT, no dmsg and no encryption: two endpoints are simply
// handed to each other. See docs/design/emulated-testbed.md.
package emu

import (
	"container/heap"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultQueueBytes is the depth of a direction's egress queue when
// LinkConfig.QueueBytes is left at zero. Queueing delay is
// QueueBytes/RateBps, so this is what turns a rate limit into the
// seconds-deep buffer a slow real leg actually has.
const DefaultQueueBytes = 1 << 20

// ErrCut is returned by a write to a link that is cut and configured to
// report the cut rather than black-hole the frame.
var ErrCut = errors.New("emu: link is cut")

// LinkConfig is one DIRECTION's network conditions. The zero value is an
// instant, lossless, infinite-rate link.
type LinkConfig struct {
	// Delay is the one-way propagation delay.
	Delay time.Duration
	// Jitter is added to Delay as a uniform sample in [-Jitter, +Jitter]
	// (clamped so the total delay is never negative).
	Jitter time.Duration
	// LossPct is the chance, in percent, that a frame is dropped. A lost
	// frame still occupies the queue and the rate limiter: loss costs
	// bandwidth, which is what makes wire/goodput move.
	LossPct float64
	// RateBps is the link rate in bytes per second. Zero is unlimited.
	// Frames are serialized: a frame's departure is the previous frame's
	// departure plus len/RateBps, so a rate limit produces real queueing
	// delay rather than a per-frame sleep.
	RateBps int64
	// QueueBytes bounds the bytes in flight between enqueue and departure.
	// A write that would exceed it BLOCKS (the backpressure a full socket
	// buffer applies), honoring the write deadline. Zero means
	// DefaultQueueBytes.
	QueueBytes int64
	// ReorderPct is the chance, in percent, that a frame is delayed by an
	// extra ReorderDelay, so it arrives after frames sent behind it.
	ReorderPct float64
	// ReorderDelay is how late a reordered frame is. Zero means Delay
	// (one extra propagation time), which reorders past roughly one BDP.
	ReorderDelay time.Duration
	// Seed seeds this direction's PRNG. Two links with the same seed and
	// the same frame sizes drop and reorder the same frames, which is what
	// makes a loss scenario reproducible.
	Seed int64
}

// LinkStats are a direction's lifetime counters.
type LinkStats struct {
	SentFrames      uint64
	SentBytes       uint64
	DeliveredFrames uint64
	DeliveredBytes  uint64
	DroppedFrames   uint64
	DroppedBytes    uint64
	ReorderedFrames uint64
	CutFrames       uint64
}

// link is one direction of a pair.
type link struct {
	cfg LinkConfig

	rndMu sync.Mutex
	rnd   *rand.Rand

	mu       sync.Mutex
	room     *sync.Cond
	queued   int64
	nextFree time.Time
	seq      uint64
	arrivals inflightHeap
	wake     chan struct{}

	cut atomic.Bool

	dst *inbox

	sentFrames, sentBytes           atomic.Uint64
	deliveredFrames, deliveredBytes atomic.Uint64
	droppedFrames, droppedBytes     atomic.Uint64
	reorderedFrames, cutFrames      atomic.Uint64

	closed    chan struct{}
	closeOnce sync.Once
}

func newLink(cfg LinkConfig, dst *inbox) *link {
	if cfg.QueueBytes <= 0 {
		cfg.QueueBytes = DefaultQueueBytes
	}
	if cfg.ReorderDelay <= 0 {
		cfg.ReorderDelay = cfg.Delay
	}
	l := &link{
		cfg:    cfg,
		rnd:    rand.New(rand.NewSource(cfg.Seed)), //nolint:gosec // G404: emulation, not cryptography
		dst:    dst,
		wake:   make(chan struct{}, 1),
		closed: make(chan struct{}),
	}
	l.room = sync.NewCond(&l.mu)
	go l.deliver()
	return l
}

// inflight is one frame between its departure and its arrival.
type inflight struct {
	at   time.Time
	seq  uint64
	b    []byte
	lost bool
}

// inflightHeap orders frames by arrival instant, ties broken by send order.
//
// Delivery used to be one time.AfterFunc per frame. On a direction with no
// delay and no jitter every one of those timers is already expired when it is
// created, so N of them become runnable at once and the runtime ran their
// callbacks — the pushes into the peer's inbox — in whatever order it liked:
// a bare NewPair with no impairment configured at all reordered 64 back-to-back
// frames in 20 runs out of 20, which is what made
// TestTransitWriteStillRelaysInOrder fail on CI. One goroutine draining this
// heap delivers in arrival order instead, so a direction reorders only when
// ReorderPct, Jitter or a rate limit says it does.
type inflightHeap []*inflight

func (h inflightHeap) Len() int      { return len(h) }
func (h inflightHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h inflightHeap) Less(i, j int) bool {
	if h[i].at.Equal(h[j].at) {
		return h[i].seq < h[j].seq
	}
	return h[i].at.Before(h[j].at)
}

func (h *inflightHeap) Push(x any) { *h = append(*h, x.(*inflight)) } //nolint:forcetypeassert

func (h *inflightHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// schedule queues a frame for delivery and nudges the delivery goroutine.
func (l *link) schedule(f *inflight) {
	l.mu.Lock()
	heap.Push(&l.arrivals, f)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// deliver is the direction's single delivery goroutine: it hands the peer's
// inbox whichever in-flight frame is due next, and sleeps until the one after
// that is due.
func (l *link) deliver() {
	for {
		l.mu.Lock()
		var (
			due  *inflight
			wait time.Duration
		)
		if len(l.arrivals) > 0 {
			if d := time.Until(l.arrivals[0].at); d <= 0 {
				due, _ = heap.Pop(&l.arrivals).(*inflight)
			} else {
				wait = d
			}
		}
		l.mu.Unlock()

		if due != nil {
			l.land(due)
			continue
		}
		if wait <= 0 {
			// Nothing in flight: park until a frame is scheduled or the
			// direction closes.
			wait = time.Hour
		}
		t := time.NewTimer(wait)
		select {
		case <-l.closed:
			t.Stop()
			return
		case <-l.wake:
		case <-t.C:
		}
		t.Stop()
	}
}

// land is one frame arriving — or not, if the direction lost or cut it.
func (l *link) land(f *inflight) {
	n := uint64(len(f.b))
	if f.lost || l.cut.Load() {
		l.droppedFrames.Add(1)
		l.droppedBytes.Add(n)
		return
	}
	select {
	case <-l.closed:
		return
	default:
	}
	l.deliveredFrames.Add(1)
	l.deliveredBytes.Add(n)
	l.dst.push(f.b)
}

// Cut makes the direction black-hole every frame — the one already in
// flight included — without closing it. This is the "the leg went away but
// the socket is still open" failure the live rig produces.
func (l *link) Cut() { l.cut.Store(true) }

// Restore lifts a Cut.
func (l *link) Restore() { l.cut.Store(false) }

// IsCut reports whether the direction is cut.
func (l *link) IsCut() bool { return l.cut.Load() }

func (l *link) stats() LinkStats {
	return LinkStats{
		SentFrames:      l.sentFrames.Load(),
		SentBytes:       l.sentBytes.Load(),
		DeliveredFrames: l.deliveredFrames.Load(),
		DeliveredBytes:  l.deliveredBytes.Load(),
		DroppedFrames:   l.droppedFrames.Load(),
		DroppedBytes:    l.droppedBytes.Load(),
		ReorderedFrames: l.reorderedFrames.Load(),
		CutFrames:       l.cutFrames.Load(),
	}
}

func (l *link) roll() float64 {
	l.rndMu.Lock()
	defer l.rndMu.Unlock()
	return l.rnd.Float64() * 100
}

func (l *link) jitter(window time.Duration) time.Duration {
	if window <= 0 {
		return 0
	}
	l.rndMu.Lock()
	defer l.rndMu.Unlock()
	return time.Duration(l.rnd.Int63n(int64(2*window))) - window
}

func (l *link) close() {
	l.closeOnce.Do(func() {
		close(l.closed)
		l.mu.Lock()
		l.room.Broadcast()
		l.mu.Unlock()
	})
}

// send enqueues one frame. It blocks while the egress queue is full and
// returns when the frame has been ACCEPTED (not when it arrives).
func (l *link) send(b []byte, deadline time.Time) error {
	select {
	case <-l.closed:
		return errClosed
	default:
	}
	n := int64(len(b))
	l.sentFrames.Add(1)
	l.sentBytes.Add(uint64(n)) //nolint:gosec // frame length is non-negative

	if l.cut.Load() {
		// A cut link accepts the write (the socket is still open) and the
		// frame simply never lands.
		l.cutFrames.Add(1)
		l.droppedFrames.Add(1)
		l.droppedBytes.Add(uint64(n)) //nolint:gosec
		return nil
	}

	frame := make([]byte, n)
	copy(frame, b)

	l.mu.Lock()
	for l.queued+n > l.cfg.QueueBytes && l.queued > 0 {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			l.mu.Unlock()
			return errTimeout
		}
		select {
		case <-l.closed:
			l.mu.Unlock()
			return errClosed
		default:
		}
		// sync.Cond has no deadline; a waker keeps the wait bounded so a
		// deadline is observed even if no frame departs.
		t := time.AfterFunc(20*time.Millisecond, func() {
			l.mu.Lock()
			l.room.Broadcast()
			l.mu.Unlock()
		})
		l.room.Wait()
		t.Stop()
	}
	l.queued += n
	cfg := l.cfg // snapshot: the knobs are settable while the link runs

	now := time.Now()
	if l.nextFree.Before(now) {
		l.nextFree = now
	}
	var service time.Duration
	if cfg.RateBps > 0 {
		service = time.Duration(float64(n) / float64(cfg.RateBps) * float64(time.Second))
	}
	departure := l.nextFree.Add(service)
	l.nextFree = departure
	// The send order is fixed here, under the same lock that orders
	// departures, so it can break ties between frames that arrive in the
	// same instant.
	l.seq++
	seq := l.seq
	l.mu.Unlock()

	lost := cfg.LossPct > 0 && l.roll() < cfg.LossPct
	arrival := departure.Add(cfg.Delay + l.jitter(cfg.Jitter))
	if arrival.Before(departure) {
		arrival = departure
	}
	if cfg.ReorderPct > 0 && l.roll() < cfg.ReorderPct {
		arrival = arrival.Add(cfg.ReorderDelay)
		l.reorderedFrames.Add(1)
	}

	// The bytes leave the queue at departure, and land at arrival.
	time.AfterFunc(time.Until(departure), func() {
		l.mu.Lock()
		l.queued -= n
		l.room.Broadcast()
		l.mu.Unlock()
	})
	l.schedule(&inflight{at: arrival, seq: seq, b: frame, lost: lost})
	return nil
}

// setCfg applies f to the direction's live configuration. Every knob is
// settable while the link is running, so a scenario can change conditions
// mid-transfer the way the live rig's conditions change under it.
func (l *link) setCfg(f func(*LinkConfig)) {
	l.mu.Lock()
	f(&l.cfg)
	if l.cfg.QueueBytes <= 0 {
		l.cfg.QueueBytes = DefaultQueueBytes
	}
	l.room.Broadcast()
	l.mu.Unlock()
}
