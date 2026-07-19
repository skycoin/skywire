// Package node pkg/cxo/node/feed.go c2-net-cxo
package node

import (
	"github.com/skycoin/skycoin/src/cipher"
)

// feed of the Node
type nodeFeed struct {
	fs *nodeFeeds // back reference

	this cipher.PubKey // this feed (to c.Unsubscribe)

	cs map[*Conn]struct{}   // connections of the feed
	hs map[uint64]*nodeHead // heads of the feed
	ho []uint64             // heads order (max heads limit)
}

func newNodeFeed(nf *nodeFeeds, pk cipher.PubKey) (n *nodeFeed) {

	n = new(nodeFeed)
	n.fs = nf
	n.this = pk
	n.cs = make(map[*Conn]struct{})
	n.hs = make(map[uint64]*nodeHead)

	return
}

func (n *nodeFeed) addConn(c *Conn) {
	n.cs[c] = struct{}{}
}

func (n *nodeFeed) delConn(c *Conn) {
	delete(n.cs, c)

	for _, nh := range n.hs {
		nh.delConn(c)
	}

}

func (n *nodeFeed) hasConn(c *Conn) (ok bool) {
	_, ok = n.cs[c]
	return
}

func (n *nodeFeed) node() *Node {
	return n.fs.n
}

func (n *nodeFeed) receivedRoot(cr connRoot) {

	// do we have the connection?

	if _, ok := n.cs[cr.c]; ok == false { //nolint:staticcheck
		return // the connection is not subscribed to the feed
	}

	var nh, ok = n.hs[cr.r.Nonce]

	if ok == false { //nolint:staticcheck

		// max heads limit
		if mh := n.node().config.MaxHeads; mh > 0 && len(n.ho) == mh {

			// Using slice; container/list would allow O(1) removal.

			// max heads
			var torm = n.ho[0]             // to remove
			copy(n.ho, n.ho[1:])           // shift all
			n.ho[len(n.ho)-1] = cr.r.Nonce // push

			var tormh = n.hs[torm]

			// Hand the evicted head its error while draining brorq — same
			// feeds↔head deadlock avoidance as nodeHead.receivedRoot. This runs
			// on the nodeFeeds event-loop goroutine (handleReceivedRoot →
			// nodeFeed.receivedRoot → here), the sole drainer of brorq; a bare
			// `tormh.errq <- ...` would deadlock if tormh's handle loop is parked
			// sending to brorq (broadcastRoot). handleBroadcastRoot is
			// non-blocking and same-goroutine-safe. Once the error is delivered
			// (or the head/node is closing), close() waits for the head loop to
			// drain it and exit.
			fs := n.fs
		evict:
			for {
				select {
				case tormh.errq <- ErrMaxHeadsLimit:
					break evict
				case <-tormh.closeq:
					break evict
				case bcr := <-fs.brorq:
					fs.handleBroadcastRoot(bcr)
				case <-fs.closeq:
					break evict
				}
			}
			tormh.close() // wait

			delete(n.hs, torm) // remove

		}

		nh = newNodeHead(n)
		n.hs[cr.r.Nonce] = nh
	}

	nh.receivedRoot(cr)

}

func (n *nodeFeed) close() {

	if n.this == (cipher.PubKey{}) {
		return // special blank feed
	}

	for _, nh := range n.hs {
		nh.close() // close head
	}

	for c := range n.cs {
		c.unsubscribe(n.this) // send Unsub messege to peer
	}

}

func (n *nodeFeed) broadcastRoot(cr connRoot) {

	// Fan out per-subscriber pushes concurrently so one slow conn's
	// bounded-but-non-zero queue wait (see sendMsgQueueTimeout in
	// conn.go) doesn't serialize the broadcast latency. Without this,
	// even with sendMsg bounded at 5s, three stuck subs would mean a
	// 15s gap before the third healthy sub gets pushed. With fan-out
	// the per-sub queue wait is parallelized and the publisher's
	// runLoop returns to drain wakeup events sooner.
	//
	// Goroutines complete (or self-timeout via sendMsgQueueTimeout)
	// independently; no WaitGroup needed because the caller doesn't
	// observe individual sub-side completions. sendRoot is fire-and-
	// forget by design — errors translate to a Close that the
	// conn's run() loop reaps.
	//
	// The root message body is encoded ONCE and shared across every
	// subscriber. Before that, each conn re-ran r.Encode() +
	// msg.Root.Encode() on the (large) all-transports root and buffered its
	// own copy — O(rootSize * subs) CPU and RSS that ballooned the
	// transport-discovery CXO node to multi-GB. Now the body is a single
	// immutable buffer and each conn adds only its 8-byte head.
	//
	// CRITICAL: the encode must NOT run on this goroutine. We are on the
	// single nodeFeeds event loop (handleBroadcastRoot), which is the sole
	// drainer of the UNBUFFERED brorq channel — so while we encode, every
	// producer calling nodeFeeds.broadcastRoot is blocked. A first cut did
	// encode here and the result was visible in production: 87 goroutines
	// parked on that send, the feeds<->head filler pipeline backing up
	// behind a multi-MB encode. Snapshot the subscriber set here (n.cs is
	// actor-owned, so it can only be read on this goroutine — that part is
	// cheap) and hand the encode plus the fan-out to a goroutine.
	if len(n.cs) == 0 {
		return
	}
	conns := make([]*Conn, 0, len(n.cs))
	for c := range n.cs {
		if c == cr.c {
			continue
		}
		conns = append(conns, c)
	}
	if len(conns) == 0 {
		return
	}

	r := cr.r
	go func() {
		body := encodeRootBody(r)
		for _, c := range conns {
			// nextSeq is atomic, so it is safe off the event loop.
			go c.sendSharedBody(c.nextSeq(), 0, body) //nolint:errcheck
		}
	}()

}
