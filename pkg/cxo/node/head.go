// Package node pkg/cxo/node/head.go c2-net-cxo
package node

import (
	"container/list"
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/node/msg"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/statutil"
)

// a head
//
// Every API call (receivedRoot, delConn, evict, close) comes from the single
// nodeFeeds event loop. An idle head has no goroutine: the loop updates its
// state under mu. handle runs only while a Root is filling, and exits under a
// TryLock of mu so it never leaves while the loop is handing it a message.
type nodeHead struct {
	n *nodeFeed // back reference

	delcq chan *Conn    // delete connection
	rrq   chan connRoot // received roots
	errq  chan error    // close with error (max heads limit)

	mu      sync.Mutex
	running bool      // handle goroutine is running
	f       *fillHead // owned by handle while running, else by holders of mu

	// closing
	await  sync.WaitGroup // wait goroutines
	closeo sync.Once      // close once
	closeq chan struct{}  // terminate
}

func newNodeHead(nf *nodeFeed) (n *nodeHead) {

	n = new(nodeHead)

	n.n = nf

	n.delcq = make(chan *Conn)
	n.rrq = make(chan connRoot)

	n.errq = make(chan error)
	n.closeq = make(chan struct{})

	n.f = &fillHead{
		nodeHead: n,
		cs:       make(knownRoots),
		favg:     n.node().fillavg, // reference

		ff: make(chan error), // filling error or nil (success)

		successq: make(chan doneRequest),   // release connection
		failureq: make(chan failedRequest), // failed requests
	}

	return
}

// startIfFilling starts handle when the head, updated under mu, began a fill.
func (n *nodeHead) startIfFilling() {
	if n.running || n.f.f == nil {
		return
	}
	select {
	case <-n.closeq:
		return
	default:
	}
	n.running = true
	n.await.Add(1)
	go n.handle()
}

// (api)
//
// Runs on the nodeFeeds event loop (handleDelConn, handleAddConnFeed and
// handleDelConnFeed → nodeFeed.delConn), so it has the cycle receivedRoot
// breaks below: the head loop can be parked in createFiller → broadcastRoot,
// waiting for this very goroutine to drain brorq. A bare send on delcq then
// deadlocks the whole node — every later Conn.Feeds, Subscribe and connect
// queues behind the loop forever (dmsg-discovery 2026-09-29: 30k goroutines,
// 1.2 GB heap, the process spent 80% of its CPU in GC). Drain brorq while we
// wait, as receivedRoot does.
func (n *nodeHead) delConn(c *Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.running {
		n.f.handleDelConn(c)
		return
	}
	fs := n.n.fs
	for {
		select {
		case n.delcq <- c:
			return
		case <-n.closeq:
			return
		case bcr := <-fs.brorq:
			fs.handleBroadcastRoot(bcr)
		}
	}
}

// (api)
func (n *nodeHead) receivedRoot(cr connRoot) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.running {
		n.f.onLoop = true
		n.f.handleReceivedRoot(cr)
		n.f.onLoop = false
		n.startIfFilling()
		return
	}

	// Breaks the feeds↔head cyclic deadlock. This runs on the single nodeFeeds
	// event-loop goroutine (handleReceivedRoot → nodeFeed.receivedRoot → here),
	// which is ALSO the sole drainer of nodeFeeds.brorq. n.rrq is unbuffered and
	// drained by this head's handle() loop — but that loop can itself be parked
	// sending to brorq (handle → createFiller → broadcastRoot), and broadcastRoot
	// only unblocks when the feeds loop drains brorq. So a bare `n.rrq <- cr`
	// here deadlocks: we wait for the head loop to read rrq while the head loop
	// waits for us to read brorq. Keep draining brorq while we wait to hand off,
	// so the head loop can make progress and drain rrq. handleBroadcastRoot is
	// non-blocking (fire-and-forget per-subscriber fan-out — see
	// nodeFeed.broadcastRoot), and we're on the loop goroutine so touching fs
	// state is safe.
	fs := n.n.fs
	for {
		select {
		case n.rrq <- cr:
			return
		case <-n.closeq:
			return
		case bcr := <-fs.brorq:
			fs.handleBroadcastRoot(bcr)
		}
	}

}

// (api) evict hands a running head ErrMaxHeadsLimit, draining brorq while it
// waits (see receivedRoot), and then closes the head.
func (n *nodeHead) evict() {
	n.mu.Lock()
	if n.running {
		fs := n.n.fs
	evict:
		for {
			select {
			case n.errq <- ErrMaxHeadsLimit:
				break evict
			case <-n.closeq:
				break evict
			case bcr := <-fs.brorq:
				fs.handleBroadcastRoot(bcr)
			case <-fs.closeq:
				break evict
			}
		}
	}
	n.mu.Unlock()
	n.close()
}

// (api)
func (n *nodeHead) close() {
	n.closeo.Do(func() {
		close(n.closeq)
	})
	n.await.Wait()
}

// code readability
func (n *nodeHead) node() *Node {
	return n.n.fs.n
}

type failedRequest struct {
	c    *Conn         // connection
	fill uint64        // filler that made the request
	seq  uint64        // seq of the filling Root
	key  cipher.SHA256 // requested object
	err  error         // failed if the err is not nil
}

type doneRequest struct {
	c    *Conn  // connection
	fill uint64 // filler that made the request
}

// handle local "fields" of the nodeHead
type fillHead struct {
	*nodeHead

	r  connRoot           // filling Root
	f  *skyobject.Filler  // filler of the r
	rq chan cipher.SHA256 // request objects (parallelism bounded by filler limit)
	ff chan error         // filler failure

	ft       *time.Timer      // fill STALL timeout (reset on each fetched object)
	tc       <-chan time.Time // ------------
	stallDur time.Duration    // the stall window ft is (re)armed with

	hardT  *time.Timer      // hard total-fill ceiling (never reset)
	hardTc <-chan time.Time // ------------

	tp   time.Time          // start point (start filling, for stat)
	favg *statutil.Duration // average filling time

	p connRoot // waits to be filled

	cs knownRoots // conn -> known root objects (seq)

	successq chan doneRequest   // succeeded requests
	failureq chan failedRequest // failed requests

	rqo *list.List // request objects (cipher.SHA256)
	fc  *list.List // connections to fill from (*Conn)

	requesting int  // number of running requests
	inflight   int  // request goroutines not yet heard from, across fills
	onLoop     bool // running inline on the nodeFeeds loop, which drains brorq

	// fill identifies the current filler. A reply to an earlier one must not
	// touch requesting or fc, which closeFiller already reset.
	fill uint64
}

func (n *nodeHead) handle() {

	defer n.await.Done()

	var (
		delcq  = n.delcq  //
		rrq    = n.rrq    //
		closeq = n.closeq //
		errq   = n.errq   //

		f = n.f

		key cipher.SHA256
		c   *Conn
		dr  doneRequest
		cr  connRoot
		fc  failedRequest
		err error // fillign failure or nil
	)

	for {
		select {
		case key = <-f.rq:

			f.handleRequest(key)

		case dr = <-f.successq:

			f.handleSuccess(dr)

		case fc = <-f.failureq:

			f.handleRequestFailure(fc)

		case err = <-f.ff:

			f.handleFillingResult(err)

		case cr = <-rrq: // root received

			f.handleReceivedRoot(cr)

		case c = <-delcq: // delete connection

			f.handleDelConn(c)

		case <-f.tc: // fill stalled (no object fetched within MaxFillingTime)

			if f.f != nil {
				f.f.Fail(ErrTimeout)
			}

		case <-f.hardTc: // hard total-fill ceiling hit (MaxTotalFillTime)

			if f.f != nil {
				f.f.Fail(ErrTimeout)
			}

		case err = <-errq:

			f.p = connRoot{} // remove pending Root
			if f.f != nil {
				f.f.Close() //nolint:errcheck,gosec
				f.handleFillingResult(err)
			}
			f.terminate()
			return

		case <-closeq: // terminate

			f.terminate()
			return

		}

		// Idle: no filler and no request goroutine left to report back. The
		// feeds loop holds mu while it hands us a message, so keep serving then.
		if f.f == nil && f.inflight == 0 && n.mu.TryLock() {
			n.running = false
			n.mu.Unlock()
			return
		}
	}

}

func (f *fillHead) handleRequest(key cipher.SHA256) {
	f.node().Debugln(FillPin, "[fill] handleRequest", key.Hex()[:7])

	// closeFiller niled f.rqo before this message drained — drop the
	// stale request rather than nil-deref. See closeFiller for the
	// race: in-flight Filler goroutines can land here after the
	// fillHead's lists were torn down.
	if f.rqo == nil {
		return
	}
	f.rqo.PushBack(key)
	f.triggerRequest()
}

func (f *fillHead) handleSuccess(dr doneRequest) {
	f.inflight--
	f.node().Debugln(FillPin, "[fill] handleSuccess", dr.c.String())

	if dr.fill != f.fill || f.fc == nil {
		// Same closeFiller race as handleRequest — production saw
		// "panic: runtime error: invalid memory address" at the
		// PushBack below until this guard landed.
		return
	}
	f.requesting--
	f.rearmStall()      // progress: an object arrived, re-arm the stall timer
	f.fc.PushBack(dr.c) // push
	f.triggerRequest()
}

// rearmStall re-arms the MaxFillingTime stall timer after progress (a fetched
// object). Runs only on the single handle() goroutine, so no lock is needed.
// Under Go 1.23+ timer semantics Reset alone would suffice, but the Stop+drain
// keeps it correct on any toolchain if a fire raced into f.tc unread.
func (f *fillHead) rearmStall() {
	if f.ft == nil {
		return
	}
	if !f.ft.Stop() {
		select {
		case <-f.tc:
		default:
		}
	}
	f.ft.Reset(f.stallDur)
}

func (f *fillHead) handleRequestFailure(fr failedRequest) {
	f.inflight--
	f.node().Debugln(FillPin, "[fill] handleRequestFailure", fr.c.String(),
		fr.key.Hex()[:7])

	switch fr.err {
	case ErrInvalidResponse:

		// close connections that sends invalid responses
		f.n.node().Errorf(fr.err, "[%s]", fr.c.Address())
		go fr.c.Close()    //nolint:errcheck,gosec
		delete(f.cs, fr.c) // remove connection

	case ErrClosed:

		// closed
		delete(f.cs, fr.c) // remove connection

	case ErrTimeout, ErrNotHeld:

		// probably don't have object we're requesting anymore
		f.cs.removeKnown(fr.c, fr.seq)

	default:

		// skyobject.ErrTerminated or other error

	}

	if fr.fill != f.fill || f.rqo == nil {
		return // closeFiller race; see handleRequest
	}
	f.requesting--
	f.rqo.PushFront(fr.key) // shift
	f.triggerRequest()

}

func (f *fillHead) handleReceivedRoot(cr connRoot) {
	f.node().Debugln(FillPin, "[fill] handleReceivedRoot",
		cr.c.String(), cr.r.Short())

	// there are a filling Root

	if f.r.r != nil {

		if cr.r.Seq < f.r.r.Seq {
			return // ignore the old Root
		}

		f.cs.addKnown(cr.c, cr.r.Seq) // add to known

		if cr.r.Seq == f.r.r.Seq {
			if f.fc == nil {
				return // closeFiller race
			}
			f.fc.PushBack(cr.c) // add to filling connections
			f.triggerRequest()
			return
		}

		if f.p.r == nil {

			// callback
			if reject := f.node().onRootReceived(cr.c, cr.r); reject != nil {
				return // rejected
			}

			f.p = cr // next to be filled
			return
		}

		// else -> f.p.r != nil

		if f.p.r.Seq < cr.r.Seq {

			// callback
			if reject := f.node().onRootReceived(cr.c, cr.r); reject != nil {
				return // rejected
			}

			f.p = cr // replace the next with newer one
		}

		return
	}

	// else -> the f.r.r is nil (there aren't)

	f.cs.addKnown(cr.c, cr.r.Seq) // add connection to known

	// callback
	if reject := f.node().onRootReceived(cr.c, cr.r); reject != nil {
		return // rejected
	}

	f.createFiller(cr) // fill the Root

}

// maxParallel returns the per-Filler parallelism cap used to size
// both Filler.limit (max in-flight subtree-walking goroutines per
// Filler) and Filler.rq (the per-Filler request channel buffer).
//
// When (*Node).maxFillingParallel is 0 (Config.MaxFillingParallel
// wasn't set, e.g. callers that construct Config{} directly instead
// of via NewConfig), we fall back to the documented package default
// — skyobject.MaxFillingParallel ("ten parallel subtrees"). The
// previous fallback was a magic 1024 which sized each Filler's
// channels to 1024; with a handful of concurrent Fillers that pinned
// 5000+ goroutines parked in (*Filler).get's select on fill.go:82,
// creating scheduler pressure visible in production pprof. The
// skyobject docstring notes the cap "should be closer to number of
// connections that used to fill a Root" — for the typical CXO node
// that's <= 20, so 10 is a sensible safety-net default. Operators
// who want a higher cap set Config.MaxFillingParallel explicitly.
func (f *fillHead) maxParallel() (mp int) {
	if mp = f.node().maxFillingParallel; mp <= 0 {
		mp = skyobject.MaxFillingParallel // documented default = 10
	}
	return
}

func (f *fillHead) createFiller(cr connRoot) {
	// handleDelConn nils out connRoot.c when the source connection is
	// removed (line 517 in handleDelConn). The connRoot may still
	// have a non-zero r, so the f.p == (connRoot{}) check at
	// handleFillingResult does NOT catch the nil-c case. Without
	// this guard, cr.c.String() below nil-derefs and panics the
	// process. The fill simply can't proceed without a source
	// connection — drop it.
	if cr.c == nil {
		f.node().Debugln(FillPin, "[fill] createFiller: source connection gone, skipping",
			cr.r.Short())
		return
	}
	f.node().Debugln(FillPin, "[fill] createFiller", cr.c.String(),
		cr.r.Short())

	// broadcast the Root we are going to fill
	if f.onLoop {
		f.nodeHead.n.fs.handleBroadcastRoot(cr)
	} else {
		f.nodeHead.n.fs.broadcastRoot(cr)
	}

	f.tp = time.Now() // time point

	// A non-positive MaxFillingTime would leave f.tc nil and disable the stall
	// timeout entirely, so a peer that flaps mid-fill pins all the fill's
	// "wanted" objects in the cache until the connection closes (the leak class
	// behind #3562). Treat <= 0 as "use the default", not "disable".
	ft := f.node().config.MaxFillingTime
	if ft <= 0 {
		ft = MaxFillingTime
	}
	// Hard total-fill ceiling. It must exceed the stall window, or it would
	// pre-empt the progress-reset stall timer and re-impose a flat total cap
	// (the very thing that churned big hub Roots). A configured value <= the
	// stall window (or <= 0) falls back to the package default; if that is
	// still not larger (an unusually large stall override), use 4x the stall.
	htime := f.node().config.MaxTotalFillTime
	if htime <= ft {
		htime = MaxTotalFillTime
		if htime <= ft {
			htime = ft * 4
		}
	}
	f.stallDur = ft
	f.ft = time.NewTimer(ft)
	f.tc = f.ft.C
	f.hardT = time.NewTimer(htime)
	f.hardTc = f.hardT.C

	f.r = cr
	f.fill++
	f.rq = make(chan cipher.SHA256, f.maxParallel())
	f.f = f.node().c.Fill(cr.r, f.rq, f.maxParallel())
	f.node().fillsActive.Add(1)

	f.rqo = list.New()                   // create list of keys
	f.fc = f.cs.buildConnsList(cr.r.Seq) // create list of connections

	f.await.Add(1)
	go f.runFiller(f.f)
}

// (async)
func (f *fillHead) runFiller(fill *skyobject.Filler) {
	defer f.await.Done()

	select {
	case f.ff <- fill.Run():
	case <-f.closeq:
		fill.Close() // since, the result ignored
	}
}

func (f *fillHead) closeFiller() {

	if f.f == nil {
		return
	}

	if f.ft != nil {
		f.ft.Stop()
	}
	if f.hardT != nil {
		f.hardT.Stop()
	}

	f.f.Close() //nolint:errcheck,gosec
	f.f = nil
	f.node().fillsActive.Add(-1)

	f.rqo, f.fc, f.rq = nil, nil, nil
	f.tc, f.hardTc = nil, nil

	f.r = connRoot{}
	f.requesting = 0

}

func (f *fillHead) handleFillingResult(err error) {

	f.node().Debugf(FillPin, "handleFillingResult %s: %v", f.r.r.Short(), err)

	if err == nil {
		f.node().onRootFilled(f.r.r)     // callback
		f.favg.Add(time.Now().Sub(f.tp)) //nolint:staticcheck // average time
		f.cs.moveForward(f.r.r.Seq + 1)  // move forward
	} else {
		f.node().onFillingBreaks(f.r.r, err) // callback
	}

	f.closeFiller() // close the filler and wait it's goroutines

	// is there a pending Root to be filled?

	// no

	if f.p == (connRoot{}) {
		return
	}

	// yes

	f.createFiller(f.p)
	f.p = connRoot{}

}

func (f *fillHead) triggerRequest() {

	if fatal := f.tryRequest(); fatal == true { //nolint:staticcheck
		if f.f != nil {
			f.f.Fail(ErrNoConnectionsToFillFrom)
		}
	}

}

// the fatal means that we haven't connections to
// request objects from anymore, neither busy nor idle
func (f *fillHead) tryRequest() (fatal bool) {

	if f.rqo.Len() == 0 {
		return fatal // no objects to request
	}

	if f.fc.Len() == 0 {
		fatal = (f.requesting == 0)
		return fatal // no connections to request from
	}

	var c = f.fc.Remove(f.fc.Front()).(*Conn) // unshift

	// the c can be removed from the head, let's check it out

	for _, ok := f.cs[c]; ok == false; _, ok = f.cs[c] { //nolint:staticcheck

		if f.fc.Len() == 0 {
			fatal = (f.requesting == 0)
			return fatal // no connections
		}

		c = f.fc.Remove(f.fc.Front()).(*Conn) // unshift next

	}

	var key = f.rqo.Remove(f.rqo.Front()).(cipher.SHA256) // unshift

	// do the request

	f.requesting++
	f.inflight++

	f.await.Add(1) // nodeHead.await
	go f.request(c, f.fill, f.r.r.Seq, key)

	return fatal
}

// code readability
func (f *fillHead) node() *Node {
	return f.n.fs.n
}

// (async) request object
func (f *fillHead) request(c *Conn, fill, seq uint64, key cipher.SHA256) {
	defer f.await.Done()

	f.node().Debugf(FillPin, "[fill] request from [%s] %d %s", c.String(), seq,
		key.Hex()[:7])

	var reply, err = c.sendRequest(&msg.RqObject{Key: key})

	// Each send below is guarded with <-f.closeq (the nodeHead's close
	// channel, promoted via the embedded *nodeHead). handle() — the only
	// receiver of successq/failureq — stops draining them once it takes its
	// own <-closeq and terminates, so an unguarded send on these unbuffered
	// channels would block forever; the deferred await.Done() would never
	// fire and nodeHead.close's await.Wait() would deadlock (the hang that
	// t.Skip'd the #3047 leak test).
	if err != nil {
		select {
		case f.failureq <- failedRequest{c, fill, seq, key, err}:
		case <-f.closeq:
		}
		return
	}

	switch x := reply.(type) {
	case *msg.Object:
		var rk = cipher.SumSHA256(x.Value)

		if rk != key {
			select {
			case f.failureq <- failedRequest{c, fill, seq, key, ErrInvalidResponse}:
			case <-f.closeq:
			}
			return
		}

		// incremented by the Want call(s)
		if _, err := f.node().c.SetWanted(key, x.Value); err != nil {
			// Recoverable DB error storing a received object — signal the fill
			// as a failed request (same as any other failure above) instead of
			// crashing the whole visor.
			f.node().Errorf(err, "SetWanted for %s failed", key.Hex()[:7])
			select {
			case f.failureq <- failedRequest{c, fill, seq, key, err}:
			case <-f.closeq:
			}
			return
		}

		select {
		case f.successq <- doneRequest{c, fill}:
		case <-f.closeq:
		}

	case *msg.Err:
		// handleRqObject's answer when it cannot serve the object: the peer is
		// no source for this Root, but its connection is fine.
		select {
		case f.failureq <- failedRequest{c, fill, seq, key, ErrNotHeld}:
		case <-f.closeq:
		}

	default:
		select {
		case f.failureq <- failedRequest{c, fill, seq, key, ErrInvalidResponse}:
		case <-f.closeq:
		}
	}

}

func (f *fillHead) handleDelConn(c *Conn) {
	delete(f.cs, c) // just remove it from list of known

	if f.r.c == c {
		f.r.c = nil // GC
	}

	if f.p.c == c {
		f.p.c = nil // GC
	}

}

func (f *fillHead) terminate() {
	f.closeFiller()
}

type knownRoots map[*Conn][]uint64

func (k knownRoots) addKnown(c *Conn, seq uint64) {

	var known, ok = k[c]

	if ok == false { //nolint:staticcheck
		k[c] = []uint64{seq}
		return
	}

	for i, ks := range known {

		// already have
		if ks == seq {
			return
		}

		// middle
		if ks > seq {
			known = append(known[:i], append([]uint64{seq}, known[i:]...)...)
			k[c] = known
			return
		}

	}

	// tail
	known = append(known, seq)
	k[c] = known

}

// remove known Root object from a connection, from which
// we can't request an object (request failure)
func (k knownRoots) removeKnown(c *Conn, seq uint64) {

	var (
		known = k[c]
		ks    uint64
		i     int
	)

	for i, ks = range known {

		if ks == seq {
			k[c] = append(known[:i], known[i+1:]...)
			return
		}

	}

}

// a Root filled, and we can rid out of old known
// Root objects of all peers
func (k knownRoots) moveForward(seq uint64) {

	for c, known := range k {

		var i int
		for i < len(known) && known[i] < seq {
			i++
		}

		k[c] = known[i:] // addKnown keeps known sorted

	}

}

// build list of connections to fill Root with given seq
func (k knownRoots) buildConnsList(seq uint64) (l *list.List) {

	l = list.New()

	for c, known := range k {

		for _, ks := range known {

			if ks == seq {
				l.PushBack(c)
				break
			}

		}
	}

	return
}
