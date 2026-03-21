package node

import (
	"sync"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// connection and received Root
type connRoot struct {
	c *Conn
	r *registry.Root
}

// connection and feed
type connFeed struct {
	c *Conn
	f cipher.PubKey
}

// feeds of the Node
type nodeFeeds struct {
	n *Node // back reference

	fs map[cipher.PubKey]*nodeFeed // feeds
	fl []cipher.PubKey             // clear-on-write

	addq chan cipher.PubKey // add feed
	addb chan bool          // add feed boolean reply

	delq chan cipher.PubKey // del feed

	addcfq chan connFeed // add connection to a feed
	delcfq chan connFeed // del connection from a feed

	delcq chan *Conn // delete connection (closed connection and similar)

	brorq chan connRoot // broadcast root to feed (triggered by head)

	// info api

	listrq chan struct{}        // list request
	listrn chan []cipher.PubKey // list response

	fcrq chan *Conn           // feeds of connection request
	fcrn chan []cipher.PubKey // feeds of connection response

	cfrq chan cipher.PubKey // connections of feed request
	cfrn chan []*Conn       // connections of feed response

	hascfrq chan connFeed // has connection a feed
	hascfrn chan bool     // response

	hasfrq chan cipher.PubKey // has feed
	hasfrn chan bool          // response

	// roo objects

	rrq chan connRoot // received root

	await  sync.WaitGroup // wait the handle
	closeo sync.Once      // once
	closeq chan struct{}  // terminate
}

func newNodeFeeds(node *Node) (n *nodeFeeds) {

	n = new(nodeFeeds)

	n.n = node

	n.fs = make(map[cipher.PubKey]*nodeFeed)

	// idle connection (not pending)
	n.fs[cipher.PubKey{}] = newNodeFeed(n, cipher.PubKey{})

	n.addq = make(chan cipher.PubKey) // add feed
	n.addb = make(chan bool)

	n.delq = make(chan cipher.PubKey) // del feed

	n.addcfq = make(chan connFeed) // add connection to a feed
	n.delcfq = make(chan connFeed) // del connection from a feed

	n.delcq = make(chan *Conn)

	n.brorq = make(chan connRoot)

	// info api

	n.listrq = make(chan struct{})
	n.listrn = make(chan []cipher.PubKey)

	n.fcrq = make(chan *Conn)
	n.fcrn = make(chan []cipher.PubKey)

	n.cfrq = make(chan cipher.PubKey)
	n.cfrn = make(chan []*Conn)

	n.hascfrq = make(chan connFeed)
	n.hascfrn = make(chan bool)

	n.hasfrq = make(chan cipher.PubKey)
	n.hasfrn = make(chan bool)

	// root objects

	n.rrq = make(chan connRoot, 10) // received Root objects

	n.closeq = make(chan struct{}) // terminate

	n.await.Add(1)
	go n.handle()

	return
}

func (n *nodeFeeds) close() {
	n.closeo.Do(func() {
		close(n.closeq)
	})
	n.await.Wait()
}

func (n *nodeFeeds) terminate() {

	for _, nf := range n.fs {
		nf.close()
	}

}

func (n *nodeFeeds) handle() {

	defer n.await.Done()
	defer n.terminate()

	var (
		addq = n.addq
		delq = n.delq

		addcfq = n.addcfq
		delcfq = n.delcfq
		delcq  = n.delcq
		broq   = n.brorq

		listrq  = n.listrq
		fcrq    = n.fcrq
		cfrq    = n.cfrq
		hascfrq = n.hascfrq
		hasfrq  = n.hasfrq

		rrq = n.rrq

		closeq = n.closeq

		pk cipher.PubKey
		cr connRoot
		cf connFeed
		c  *Conn
	)

	for {

		select {

		case cr = <-rrq:
			n.handleReceivedRoot(cr)

		case cf = <-addcfq:
			n.handleAddConnFeed(cf)

		case cf = <-delcfq:
			n.handleDelConnFeed(cf)

		case cr = <-broq:
			n.handleBroadcastRoot(cr)

		case pk = <-addq:
			n.handleAddFeed(pk)

		case pk = <-delq:
			n.handleDelFeed(pk)

		case c = <-delcq:
			n.handleDelConn(c)

		//
		// info api
		//

		case <-listrq:
			n.handleList()

		case c = <-fcrq:
			n.handleFeedsOfConnection(c)

		case pk = <-cfrq:
			n.handleConnectionsOfFeed(pk)

		case cf = <-hascfrq:
			n.handleHasConnFeed(cf)

		case pk = <-hasfrq:
			n.handleHasFeed(pk)

		// close

		case <-closeq:
			return
		}

	}

}

// (api)
func (n *nodeFeeds) addFeed(pk cipher.PubKey) (added bool) {

	if pk == (cipher.PubKey{}) {
		return // blank feed is special
	}

	select {
	case n.addq <- pk:
	case <-n.closeq:
	}

	select {
	case added = <-n.addb:
	case <-n.closeq:
	}

	return

}

// (handler)
func (n *nodeFeeds) handleAddFeed(pk cipher.PubKey) {

	n.n.Debug(FeedPin, "handleAddFeed ", pk.Hex()[:7])

	var ok bool

	if _, ok = n.fs[pk]; ok == false {
		n.fs[pk] = newNodeFeed(n, pk)
		n.fl = nil
	}

	// or, already have the feed

	select {
	case n.addb <- (ok == false):
	case <-n.closeq:
	}

}

// (api)
func (n *nodeFeeds) delFeed(pk cipher.PubKey) {

	if pk == (cipher.PubKey{}) {
		return // blank feed is special
	}

	select {
	case n.delq <- pk:
	case <-n.closeq:
	}

}

// (handler)
func (n *nodeFeeds) handleDelFeed(pk cipher.PubKey) {

	n.n.Debug(FeedPin, "handleDelFeed ", pk.Hex()[:7])

	var nf, ok = n.fs[pk]

	if ok == false {
		return // doesn't have the feed, nothing to delete
	}

	nf.close() // close the feed, terminating all internal

	delete(n.fs, pk)
	n.fl = nil

}

// (api)
func (n *nodeFeeds) addConnFeed(c *Conn, pk cipher.PubKey) {

	select {
	case n.addcfq <- connFeed{c, pk}:
	case <-n.closeq:
	}

}

// (handler)
func (n *nodeFeeds) handleAddConnFeed(cf connFeed) {

	n.n.Debugln(FeedPin, "handleAddConnFeed", cf.c.String(), cf.f.Hex()[:7])

	// if the cf.f is blank the this connection is new

	if cf.f == (cipher.PubKey{}) {
		n.fs[cipher.PubKey{}].addConn(cf.c)
		return
	}

	var nf, ok = n.fs[cf.f]

	if ok == false {
		nf = newNodeFeed(n, cf.f)
		n.fs[cf.f] = nf
	}

	nf.addConn(cf.c)
	n.fs[cipher.PubKey{}].delConn(cf.c) // delete from idle

}

// (api)
func (n *nodeFeeds) delConnFeed(c *Conn, pk cipher.PubKey) {

	select {
	case n.delcfq <- connFeed{c, pk}:
	case <-n.closeq:
	}

}

// (handler)
func (n *nodeFeeds) handleDelConnFeed(cf connFeed) {

	n.n.Debugln(FeedPin, "handleDelConnFeed", cf.c.String(), cf.f.Hex()[:7])

	var nf, ok = n.fs[cf.f]

	if ok == false {
		return // no such feed
	}

	nf.delConn(cf.c)

	// if the connection does't share a feed, then
	// we put it to idle

	for pk, nf := range n.fs {

		if pk == (cipher.PubKey{}) {
			continue // avoid map lookup
		}

		if nf.hasConn(cf.c) == true {
			return
		}

	}

	n.fs[cipher.PubKey{}].addConn(cf.c) // add to idle

}

// (bubbling api)
func (n *nodeFeeds) broadcastRoot(cr connRoot) {

	select {
	case n.brorq <- cr:
	case <-n.closeq:
	}

}

// (handler)
func (n *nodeFeeds) handleBroadcastRoot(cr connRoot) {

	var nf, ok = n.fs[cr.r.Pub]

	if ok == false {
		return
	}

	nf.broadcastRoot(cr)
}

// (api)
func (n *nodeFeeds) delConn(c *Conn) {

	select {
	case n.delcq <- c:
	case <-n.closeq:
	}

}

// (handler)
func (n *nodeFeeds) handleDelConn(c *Conn) {
	for _, nf := range n.fs {
		nf.delConn(c) // from all
	}
}

// (api)
func (n *nodeFeeds) receivedRoot(c *Conn, r *registry.Root) {

	select {
	case n.rrq <- connRoot{c, r}:
	case <-n.closeq:
	}

}

// (handler)
func (n *nodeFeeds) handleReceivedRoot(cr connRoot) {

	if cr.r.Pub == (cipher.PubKey{}) {
		return // invalid case
	}

	var nf, ok = n.fs[cr.r.Pub]

	if ok == false {
		return // no such feed (drop the Root)
	}

	nf.receivedRoot(cr)

}

//
// info api
//

// (api) get lsit of feeds
func (n *nodeFeeds) list() (list []cipher.PubKey) {

	select {
	case n.listrq <- struct{}{}:
	case <-n.closeq:
		return
	}

	select {
	case list = <-n.listrn:
	case <-n.closeq:
	}

	return
}

// (handler)
func (n *nodeFeeds) handleList() {

	if n.fl == nil && len(n.fs) > 1 {

		var fl = make([]cipher.PubKey, 0, len(n.fl))

		for pk := range n.fs {

			if pk == (cipher.PubKey{}) {
				continue // special case for idle connections
			}

			fl = append(fl, pk)
			n.fl = fl
		}

	}

	select {
	case n.listrn <- n.fl:
	case <-n.closeq:
	}
	return

}

// (api) feeds of connection
func (n *nodeFeeds) feedsOfConnection(c *Conn) (feeds []cipher.PubKey) {

	select {
	case n.fcrq <- c:
	case <-n.closeq:
		return
	}

	select {
	case feeds = <-n.fcrn:
	case <-n.closeq:
	}

	return
}

// (handler)
func (n *nodeFeeds) handleFeedsOfConnection(c *Conn) {

	var feeds []cipher.PubKey

	for pk, nf := range n.fs {

		if pk == (cipher.PubKey{}) {
			continue
		}

		if _, ok := nf.cs[c]; ok == true {
			feeds = append(feeds, pk)
		}

	}

	select {
	case n.fcrn <- feeds:
	case <-n.closeq:
	}
	return

}

// (api) connections of feed
func (n *nodeFeeds) connectionsOfFeed(pk cipher.PubKey) (cs []*Conn) {

	select {
	case n.cfrq <- pk:
	case <-n.closeq:
		return
	}

	select {
	case cs = <-n.cfrn:
	case <-n.closeq:
	}

	return
}

// (handler)
func (n *nodeFeeds) handleConnectionsOfFeed(pk cipher.PubKey) {

	var cs []*Conn

	var nf, ok = n.fs[pk]

	if ok == true {

		for c := range nf.cs {
			cs = append(cs, c)
		}

	}

	select {
	case n.cfrn <- cs:
	case <-n.closeq:
	}
	return

}

// (api) has connection feed
func (n *nodeFeeds) hasConnFeed(c *Conn, pk cipher.PubKey) (yep bool) {

	select {
	case n.hascfrq <- connFeed{c, pk}:
	case <-n.closeq:
		return
	}

	select {
	case yep = <-n.hascfrn:
	case <-n.closeq:
	}

	return
}

// (handler)
func (n *nodeFeeds) handleHasConnFeed(cf connFeed) {

	var nf, ok = n.fs[cf.f]

	if ok == true {
		_, ok = nf.cs[cf.c]
	}

	select {
	case n.hascfrn <- ok:
	case <-n.closeq:
	}
	return

}

// (api) has connection feed
func (n *nodeFeeds) hasFeed(pk cipher.PubKey) (yep bool) {

	select {
	case n.hasfrq <- pk:
	case <-n.closeq:
		return
	}

	select {
	case yep = <-n.hasfrn:
	case <-n.closeq:
	}

	return
}

// (handler)
func (n *nodeFeeds) handleHasFeed(pk cipher.PubKey) {

	var _, ok = n.fs[pk]

	select {
	case n.hasfrn <- ok:
	case <-n.closeq:
	}
	return

}
