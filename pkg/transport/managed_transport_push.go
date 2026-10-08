package transport

import (
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport/network"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// pushReads serves sudph and webrtc transports from a few shared workers
// instead of a read goroutine each. It is on under TinyGo, where every
// goroutine is a thread, and under the push tests.
var pushReads = runtime.Compiler == "tinygo" //nolint:gochecknoglobals

func pushableType(t tptypes.Type) bool { return t == tptypes.SUDPH || t == tptypes.WEBRTC }

const (
	pushBudget    = 64       // reads from one transport before the next gets a turn
	pushBufSize   = 64 << 10 // read buffer of each worker
	pushRetryWait = 2 * time.Millisecond
)

// pushState is a transport served without a read goroutine. One worker at a
// time runs it, so its packets reach the router and its handlers in order.
type pushState struct {
	mt     *ManagedTransport
	readCh chan<- routing.Packet
	onEnd  func()

	// sched is 0 when idle, 1 when queued or running, and 2 when more data
	// arrived while it ran.
	sched atomic.Int32
	next  atomic.Pointer[pushSrc] // a conn attached since the last run
	done  atomic.Bool

	// Used only by the worker running the transport.
	src     *pushSrc
	dec     packetDecoder
	pending []routing.Packet // waiting for room in readCh, in order

	classic bool // under transportMx: a conn without push got a read goroutine
}

type pushSrc struct {
	r  network.PushReader
	tp network.Transport
}

// Start serves the transport, and calls onEnd once it has ended. A transport
// whose reads can be pushed gets no goroutine of its own.
func (mt *ManagedTransport) Start(readCh chan<- routing.Packet, onEnd func()) {
	if !pushReads || !pushableType(mt.Entry.Type) {
		go func() {
			mt.Serve(readCh)
			if onEnd != nil {
				onEnd()
			}
		}()
		return
	}

	mt.wg.Add(1)
	mt.lastRecvNanos.Store(time.Now().UnixNano())
	mt.log.WithField("tp_id", mt.Entry.ID).WithField("remote_pk", mt.rPK).
		WithField("tp_index", atomic.AddInt32(&mTpCount, 1)).Debug("Serving without a read goroutine.")

	ps := &pushState{mt: mt, readCh: readCh, onEnd: onEnd}
	mt.transportMx.Lock()
	mt.push = ps
	if mt.transport != nil {
		mt.attachPush(mt.transport)
	}
	mt.transportMx.Unlock()
	if !mt.isServing() {
		go ps.finish() // the caller may hold the manager's lock, which onEnd takes
	}
}

// attachPush moves reads of a newly set conn to the push workers, or to a read
// goroutine when the conn cannot push. It is called with transportMx held.
func (mt *ManagedTransport) attachPush(tp network.Transport) {
	ps := mt.push
	if ps == nil || ps.classic {
		return
	}
	r, ok := network.PushReaderOf(tp)
	if !ok {
		ps.classic = true
		mt.wg.Add(1)
		go mt.readLoop(ps.readCh)
		return
	}
	ps.next.Store(&pushSrc{r: r, tp: tp})
	r.SetNotify(ps.kick)
	ps.kick()
}

// finish runs once the transport has closed, as the end of Serve does.
func (ps *pushState) finish() {
	if !ps.done.CompareAndSwap(false, true) {
		return
	}
	mt := ps.mt
	mt.recordLog()
	mt.log.WithField("remaining_tps", atomic.AddInt32(&mTpCount, -1)).Debug("Stopped serving.")
	mt.wg.Done()
	if ps.onEnd != nil {
		ps.onEnd()
	}
}

// kick asks for the transport to be run because data may be ready.
func (ps *pushState) kick() {
	for {
		switch ps.sched.Load() {
		case 0:
			if ps.sched.CompareAndSwap(0, 1) {
				pushPool.enqueue(ps)
				return
			}
		case 1:
			if ps.sched.CompareAndSwap(1, 2) {
				return
			}
		default:
			return
		}
	}
}

type pushResult int

const (
	pushIdle  pushResult = iota // nothing more was ready
	pushMore                    // the budget ran out with data maybe left
	pushRetry                   // readCh was full
	pushEnded                   // the transport ended
)

// run reads what is ready and passes it on. buf belongs to the worker.
func (ps *pushState) run(buf []byte) pushResult {
	mt := ps.mt
	if s := ps.next.Swap(nil); s != nil {
		ps.src, ps.dec = s, packetDecoder{}
	}
	if ps.done.Load() || ps.src == nil {
		return pushIdle
	}
	if !mt.isServing() {
		return pushEnded
	}
	if !ps.flush() {
		return pushRetry
	}
	for i := 0; i < pushBudget; i++ {
		n, err := ps.src.r.ReadPlain(buf, ps.emitPlain)
		if len(ps.pending) > 0 {
			// Stop reading: the data stays in the conn, so its flow control
			// slows the sender until the router catches up.
			return pushRetry
		}
		if err != nil {
			return ps.readErr(err)
		}
		if n == 0 {
			return pushIdle
		}
	}
	return pushMore
}

func (ps *pushState) emitPlain(p []byte) { ps.dec.feed(p, ps.deliver) }

func (ps *pushState) deliver(p routing.Packet) {
	mt := ps.mt
	if n := len(p); n > routing.PacketHeaderSize {
		mt.logRecv(uint64(n - routing.PacketHeaderSize)) //nolint:gosec
	}
	if !mt.dispatchPacket(p) {
		return
	}
	if len(ps.pending) == 0 {
		select {
		case ps.readCh <- p:
			return
		default:
		}
	}
	ps.pending = append(ps.pending, p)
}

// flush passes held packets to the router and reports whether all went.
func (ps *pushState) flush() bool {
	for len(ps.pending) > 0 {
		select {
		case ps.readCh <- ps.pending[0]:
			ps.pending[0] = nil
			ps.pending = ps.pending[1:]
		default:
			return false
		}
	}
	ps.pending = nil
	return true
}

// readErr handles the end of the current conn, as readLoop does.
func (ps *pushState) readErr(err error) pushResult {
	mt := ps.mt
	if cur := mt.getTransport(); cur != nil && cur != ps.src.tp {
		mt.log.WithError(err).Debug("Underlying conn replaced; reading from the new one")
		mt.lastRecvNanos.Store(time.Now().UnixNano())
		ps.src = nil
		if ps.next.Load() != nil {
			return pushMore
		}
		return pushIdle
	}
	errStr := err.Error()
	if strings.Contains(errStr, "closed pipe") ||
		strings.Contains(errStr, "closed network connection") ||
		errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		mt.log.WithError(err).Debug("Transport closed, stopping reads")
	} else {
		mt.log.WithError(err).Warn("Failed to read packet, closing transport")
	}
	mt.closeWith("read: " + err.Error())
	return pushEnded
}

// pushPool is the workers serving pushed transports, and the retry list for
// transports waiting for room in readCh.
var pushPool = &pushWorkers{} //nolint:gochecknoglobals

type pushWorkers struct {
	once sync.Once
	mu   sync.Mutex
	cond *sync.Cond
	q    []*pushState
	head int

	retryMu sync.Mutex
	retry   []*pushState
	retryCh chan struct{}
}

func (w *pushWorkers) start() {
	w.cond = sync.NewCond(&w.mu)
	w.retryCh = make(chan struct{}, 1)
	n := min(max(2*runtime.NumCPU(), 4), 16)
	for i := 0; i < n; i++ {
		go w.work()
	}
	go w.retryLoop()
}

func (w *pushWorkers) enqueue(ps *pushState) {
	w.once.Do(w.start)
	w.mu.Lock()
	if w.head > len(w.q)/2 {
		n := copy(w.q, w.q[w.head:])
		clear(w.q[n:])
		w.q, w.head = w.q[:n], 0
	}
	w.q = append(w.q, ps)
	w.mu.Unlock()
	w.cond.Signal()
}

func (w *pushWorkers) work() {
	buf := make([]byte, pushBufSize)
	for {
		w.mu.Lock()
		for w.head == len(w.q) {
			w.cond.Wait()
		}
		ps := w.q[w.head]
		w.q[w.head] = nil
		w.head++
		if w.head == len(w.q) {
			w.q, w.head = w.q[:0], 0
		}
		w.mu.Unlock()

		switch ps.run(buf) {
		case pushMore:
			ps.sched.Store(1)
			w.enqueue(ps)
		case pushRetry:
			ps.sched.Store(0)
			w.addRetry(ps)
		case pushEnded:
			// Stays scheduled, so no worker runs it again.
			go ps.finish()
		case pushIdle:
			if !ps.sched.CompareAndSwap(1, 0) {
				// More data arrived while it ran.
				ps.sched.Store(1)
				w.enqueue(ps)
			}
		}
	}
}

func (w *pushWorkers) addRetry(ps *pushState) {
	w.retryMu.Lock()
	w.retry = append(w.retry, ps)
	w.retryMu.Unlock()
	select {
	case w.retryCh <- struct{}{}:
	default:
	}
}

// retryLoop runs transports that were waiting for room in readCh again.
func (w *pushWorkers) retryLoop() {
	var batch []*pushState
	for range w.retryCh {
		for {
			time.Sleep(pushRetryWait)
			w.retryMu.Lock()
			batch, w.retry = w.retry, batch[:0]
			w.retryMu.Unlock()
			if len(batch) == 0 {
				break
			}
			for i, ps := range batch {
				ps.kick()
				batch[i] = nil
			}
		}
	}
}
