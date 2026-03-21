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
type nodeHead struct {
	n *nodeFeed // back reference

	delcq chan *Conn    // delete connection
	rrq   chan connRoot // received roots
	errq  chan error    // close with error (max heads limit)

	// api info

	inforq chan struct{}  // info request
	inforn chan *headInfo // info response

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

	n.await.Add(1)
	go n.handle()

	return
}

// (api)
func (n *nodeHead) closeByError(err error) { //nolint:unused

	select {
	case n.errq <- err:
	case <-n.closeq:
	}

}

// (api)
func (n *nodeHead) delConn(c *Conn) {

	select {
	case n.delcq <- c:
	case <-n.closeq:
	}

}

// (api)
func (n *nodeHead) receivedRoot(cr connRoot) {

	select {
	case n.rrq <- cr:
	case <-n.closeq:
	}

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
	c   *Conn         // connection
	seq uint64        // seq of the filling Root
	key cipher.SHA256 // requested object
	err error         // failed if the err is not nil
}

// handle local "fields" of the nodeHead
type fillHead struct {
	*nodeHead

	r  connRoot           // filling Root
	f  *skyobject.Filler  // filler of the r
	rq chan cipher.SHA256 // request objects (TODO: maxParall)
	ff chan error         // filler failure

	ft *time.Timer      // fill timeout
	tc <-chan time.Time // ------------

	tp   time.Time          // start point (start filling, for stat)
	favg *statutil.Duration // average filling time

	p connRoot // waits to be filled

	cs knownRoots // conn -> known root objects (seq)

	successq chan *Conn         // succeeded requests
	failureq chan failedRequest // failed requests

	rqo *list.List // request objects (cipher.SHA256)
	fc  *list.List // connections to fill from (*Conn)

	requesting int // number of running requests
}

func (n *nodeHead) handle() {

	defer n.await.Done()

	var (
		delcq  = n.delcq  //
		rrq    = n.rrq    //
		closeq = n.closeq //
		errq   = n.errq   //
		inforq = n.inforq //

		f = fillHead{
			nodeHead: n,
			rq:       make(chan cipher.SHA256, 10),
			cs:       make(knownRoots),
			favg:     n.node().fillavg, // reference

			ff: make(chan error), // filling error or nil (success)

			successq: make(chan *Conn),         // release connection
			failureq: make(chan failedRequest), // failed requests
		}

		key cipher.SHA256
		c   *Conn
		cr  connRoot
		fc  failedRequest
		err error // fillign failure or nil
	)

	for {
		select {
		case key = <-f.rq:

			f.handleRequest(key)

		case c = <-f.successq:

			f.handleSuccess(c)

		case fc = <-f.failureq:

			f.handleRequestFailure(fc)

		case err = <-f.ff:

			f.handleFillingResult(err)

		case cr = <-rrq: // root received

			f.handleReceivedRoot(cr)

		case c = <-delcq: // delete connection

			f.handleDelConn(c)

		case <-f.tc: // filling timeout

			if f.f != nil {
				f.f.Fail(ErrTimeout)
			}

		// api info

		case <-inforq:
			n.handleInfo(&f)

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
	}

}

func (f *fillHead) handleRequest(key cipher.SHA256) {
	f.node().Debugln(FillPin, "[fill] handleRequest", key.Hex()[:7])

	f.rqo.PushBack(key)
	f.triggerRequest()
}

func (f *fillHead) handleSuccess(c *Conn) {
	f.node().Debugln(FillPin, "[fill] handleSuccess", c.String())

	f.requesting--
	f.fc.PushBack(c) // push
	f.triggerRequest()
}

func (f *fillHead) handleRequestFailure(fr failedRequest) {
	f.node().Debugln(FillPin, "[fill] handleRequestFailure", fr.c.String(),
		fr.key.Hex()[:7])

	f.requesting--

	switch fr.err {
	case ErrInvalidResponse:

		// close connections that sends invalid responses
		f.n.node().Errorf(fr.err, "[%s]", fr.c.Address())
		go fr.c.Close()    //nolint:errcheck,gosec
		delete(f.cs, fr.c) // remove connection

	case ErrClosed:

		// closed
		delete(f.cs, fr.c) // remove connection

	case ErrTimeout:

		// probably don't have object we're requesting anymore
		f.cs.removeKnown(fr.c, fr.seq)

	default:

		// skyobject.ErrTerminated or other error

	}

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

// value for channels, if hte (*Node).maxFillingParallel
// is zero, then the skyobject.Filler has no limits for
// goroutines, but we can't create an unlimited channel,
// thus we cahnge the zero to 1024 (I think it's enough)
func (f *fillHead) maxParallel() (mp int) {
	if mp = f.node().maxFillingParallel; mp <= 0 {
		mp = 1024 // TODO (kostyarin): make it constant
	}
	return
}

func (f *fillHead) createFiller(cr connRoot) {
	f.node().Debugln(FillPin, "[fill] createFiller", cr.c.String(),
		cr.r.Short())

	// broadcast the Root we are going to fill
	f.nodeHead.n.fs.broadcastRoot(cr)

	f.tp = time.Now() // time point

	if ft := f.node().config.MaxFillingTime; ft > 0 {
		f.ft = time.NewTimer(ft)
		f.tc = f.ft.C
	}

	f.r = cr
	f.rq = make(chan cipher.SHA256, f.maxParallel())
	f.f = f.node().c.Fill(cr.r, f.rq, f.maxParallel())

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

	f.f.Close() //nolint:errcheck,gosec

	f.rqo, f.fc, f.rq = nil, nil, nil

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

	f.await.Add(1) // nodeHead.await
	go f.request(c, f.r.r.Seq, key)

	return fatal
}

// code readability
func (f *fillHead) node() *Node {
	return f.n.fs.n
}

// (async) request object
func (f *fillHead) request(c *Conn, seq uint64, key cipher.SHA256) {
	defer f.await.Done()

	f.node().Debugf(FillPin, "[fill] request from [%s] %d %s", c.String(), seq,
		key.Hex()[:7])

	var reply, err = c.sendRequest(&msg.RqObject{Key: key})

	if err != nil {
		f.failureq <- failedRequest{c, seq, key, err}
		return
	}

	switch x := reply.(type) {
	case *msg.Object:
		var rk = cipher.SumSHA256(x.Value)

		if rk != key {
			f.failureq <- failedRequest{c, seq, key, ErrInvalidResponse}
			return
		}

		// incremented by the Want call(s)
		if _, err := f.node().c.SetWanted(key, x.Value); err != nil {
			f.node().Fatal("DB failure:", err)
			return
		}

		f.successq <- c

	default:
		f.failureq <- failedRequest{c, seq, key, ErrInvalidResponse}
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

	var (
		ks uint64
		i  int
	)

	for c, known := range k {

		for i, ks = range known {

			if ks >= seq {
				k[c] = append(known[:i], known[i+1:]...)
				break
			}

		}

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

type headInfo struct {
	nonce uint64 //nolint:unused // nonce of the head

	fillingRoot    bool   // has filling Root
	fillingRootSeq uint64 // its seq

	pendingRoot    bool   // has pending Root
	pendingRootSeq uint64 // its seq

	// TODO (kostyarin): connections used for current filling

	// known Root objects of peers
	known map[*Conn][]uint64
}

// (api) request, can return nil
func (n *nodeHead) info() (hi *headInfo) { //nolint:unused

	select {
	case n.inforq <- struct{}{}:
	case <-n.closeq:
		return
	}

	select {
	case hi = <-n.inforn:
	case <-n.closeq:
	}

	return

}

func (n *nodeHead) handleInfo(f *fillHead) {

	var ni = new(headInfo)

	ni.fillingRoot = (f.r.r != nil)

	if ni.fillingRoot == true { //nolint:staticcheck
		ni.fillingRootSeq = f.r.r.Seq
	}

	ni.pendingRoot = (f.p.r != nil)

	if ni.pendingRoot == true { //nolint:staticcheck
		ni.pendingRootSeq = f.r.r.Seq
	}

	// make copy

	ni.known = make(map[*Conn][]uint64)

	for c, known := range f.cs {
		var kc = make([]uint64, len(known))
		copy(kc, known)
		ni.known[c] = kc
	}

	// the nonce field should be set by caller (by requester)

	select {
	case n.inforn <- ni:
	case <-n.closeq:
	}

}
