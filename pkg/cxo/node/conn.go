package node

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/node/msg"
	"github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A Conn represent connection of the Node
type Conn struct {
	*transport.Connection

	n *Node // back reference

	peerID   cipher.PubKey // peer's pubkey
	incoming bool          // is incoming or not

	initErr error
	initq   chan struct{}

	closeq chan struct{}  // signal for all goroutines to exit
	await  sync.WaitGroup // wait for all goroutines to exit

	sendq chan<- []byte // channel from transport.Connection

	// # stat
	//
	// TODO (kostyarin): stat without mutexes to do not slow down the connection
	//
	// ------

	mx sync.Mutex // locks all fields below, TODO: change to RWMutex

	// request - response
	seq  uint32                    // messege seq number (for request-response)
	reqs map[uint32]chan<- msg.Msg // requests
}

func (n *Node) newConnection(
	fc *transport.Connection, isIncoming bool) *Conn {

	c := &Conn{
		Connection: fc,

		n: n,

		incoming: isIncoming,

		initq: make(chan struct{}),

		closeq: make(chan struct{}),

		sendq: fc.GetChanOut(),

		reqs: make(map[uint32]chan<- msg.Msg),
	}

	return c
}

func (c *Conn) waitForInit() error {
	<-c.initq
	return c.initErr
}

// run defines connection lifecycle.
func (c *Conn) run() {
	// Receive messages, until close signal is received or error occures.
	// In case of error stop all goroutines, started by connection.
	var rcvErr error
	c.await.Add(1)
	go func() {
		defer c.await.Done()
		if rcvErr = c.receiveMsg(); rcvErr != nil {
			close(c.closeq)
		}
	}()

	// If OnConnect returns error, connection will be closed.
	var occErr error
	if occErr = c.n.onConnect(c); occErr != nil {
		close(c.closeq)
	}

	// Wait for all groutines to exit.
	c.await.Wait()

	c.n.removeConn(c)

	// Remove connection from transport's cache and close connection.
	if c.IsTCP() {
		c.n.tcp.closeConn(c.Address())
	} else {
		c.n.udp.closeConn(c.Address())
	}

	// In connection is closed in case of error, decide which one to pass to OnDisconnect.
	var odcErr error
	switch {
	case rcvErr != nil:
		odcErr = rcvErr
	case occErr != nil:
		odcErr = occErr
	}
	c.n.onDisconenct(c, odcErr)
}

func (c *Conn) decodeRaw(raw []byte) (seq, rseq uint32, m msg.Msg, err error) {

	if len(raw) < 9 {
		err = errors.New("invlaid messege received: too short")
		return
	}

	seq = binary.LittleEndian.Uint32(raw)
	raw = raw[4:]

	rseq = binary.LittleEndian.Uint32(raw)
	raw = raw[4:]

	m, err = msg.Decode(raw)
	return
}

//
// info
//

// PeerID is id of remote peer that used
// for internals and unique
func (c *Conn) PeerID() (id cipher.PubKey) {
	return c.peerID
}

// IsIncoming returns true if this Conn is
// incoming and accepted by listener
func (c *Conn) IsIncoming() (ok bool) {
	return c.incoming
}

// IsOutgoing is inverse of the IsIncoming
func (c *Conn) IsOutgoing() (ok bool) {
	return c.incoming == false
}

// Node returns related Node
func (c *Conn) Node() (node *Node) {
	return c.n
}

// Address returns remote address
// represetned as string
func (c *Conn) Address() (address string) {
	return c.GetRemoteAddr().String()
}

// Feeds returns list of feeds this connection
// share with peer
func (c *Conn) Feeds() (feeds []cipher.PubKey) {
	return c.n.fs.feedsOfConnection(c)
}

func connString(isIncoming, isTCP bool, addr string) (s string) {
	if isIncoming == true {
		s = "↓ "
	} else {
		s = "↑ "
	}

	if isTCP == true {
		s += "tcp://"
	} else {
		s += "udp://"
	}

	return s + addr
}

// String returns string "-> network://remote_address"
// for example: "-> tcp://127.0.0.1:8887". Where the
// arrow is "->" for incoming connections and is "<-"
// for outgoing
func (c *Conn) String() (s string) {
	return connString(c.incoming, c.IsTCP(), c.Address())
}

//
// requests
//

// RemoteFeeds requests list of feeds that remote peer share.
// It's possible if the remote peer is public server, otherwise
// it returns "not a public server" error. The request has
// timeout configured by Config
func (c *Conn) RemoteFeeds() (feeds []cipher.PubKey, err error) {

	var reply msg.Msg

	if reply, err = c.sendRequest(&msg.RqList{}); err != nil {
		return
	}

	switch x := reply.(type) {

	case *msg.List:

		feeds = x.Feeds

	case *msg.Err:

		err = errors.New(x.Err)

	default:

		err = fmt.Errorf("invalid response type %T", reply)

	}

	return
}

func (c *Conn) sendRoot(r *registry.Root) {
	c.sendMsg(c.nextSeq(), 0, &msg.Root{
		Feed:  r.Pub,
		Nonce: r.Nonce,
		Seq:   r.Seq,

		Value: r.Encode(),

		Sig: r.Sig,
	})
}

// send last Root to peer
func (c *Conn) sendLastRoot(pk cipher.PubKey) {

	var (
		activeHead = c.n.c.ActiveHead(pk)
		r, err     = c.n.c.LastRoot(pk, activeHead)
	)

	if err == nil {
		c.sendRoot(r)
		return
	}

	c.n.Debugf(MsgSendPin, "[%s] sendLastRoot %s: no Root objects found (%d)",
		c.String(), pk.Hex()[:7], activeHead)

}

// Subscribe to gievn feed of remote peer. The Subscribe adds
// feed to the Node if the Node doesn't have the feed calling
// the (*Node).Share method. If request fails, then the feed
// is not removed. E.g. if the Subscribe method returns error
// then it probably adds given feed to the Node, but request
// fails. Or it can returns error of the (*Node).Share
func (c *Conn) Subscribe(feed cipher.PubKey) (err error) {

	// add the feed to node

	if err = c.n.Share(feed); err != nil {
		return
	}

	var reply msg.Msg

	if reply, err = c.sendRequest(&msg.Sub{Feed: feed}); err != nil {
		return
	}

	switch x := reply.(type) {

	case *msg.Ok:
	// success

	case *msg.Err:
		err = errors.New(x.Err)

	default:
		err = fmt.Errorf("invalid response type %T", reply)

	}

	if err != nil {
		return
	}

	c.n.fs.addConnFeed(c, feed)
	c.sendLastRoot(feed)
	return
}

// just send the messege
func (c *Conn) unsubscribe(pk cipher.PubKey) {
	c.sendMsg(c.nextSeq(), 0, &msg.Unsub{
		Feed: pk,
	})
}

// Unsubscribe from given feed of remote peer
func (c *Conn) Unsubscribe(feed cipher.PubKey) {
	c.n.fs.delConnFeed(c, feed)
	c.unsubscribe(feed) // notify peer
	return
}

// PreviewFunc used by (*Conn).Preview method. The function
// receive registry.Pack and lates Root object. The Pack
// used to get objects from DB or from remote peer. If the
// function returns true, then the Node and the Connection
// will be subscribed to the feed. Given Pack and given Root
// can't be used outside the function.
type PreviewFunc func(pack registry.Pack, r *registry.Root) (subscribe bool)

// Preview a feed of remote peer. The request is blocking.
// The Preview gets latest Root of given feed from remote
// peer and uses the peer to obtain objects this node does
// not have.
func (c *Conn) Preview(
	feed cipher.PubKey, //      : feed to preview
	previewFunc PreviewFunc, // : the function
) (
	err error, //               : first error
) {

	var reply msg.Msg
	if reply, err = c.sendRequest(&msg.RqPreview{Feed: feed}); err != nil {
		return
	}

	var r *registry.Root

	switch x := reply.(type) {
	case *msg.Err:
		return errors.New("error: " + x.Err)
	case *msg.Root:
		if r, err = c.n.c.PreviewRoot(x.Feed, x.Sig, x.Value); err != nil {
			return
		}
	default:
		return fmt.Errorf("invalid msg type received: %T", reply)
	}

	var p *skyobject.Preview
	if p, err = c.n.c.Preview(r, c.getter()); err != nil {
		return
	}

	if previewFunc(p, r) == true {
		err = c.Subscribe(feed)
	}

	return
}

// implements skyobject.Getter
// wrapping the Conn
type cget struct {
	c *Conn
}

func (c *cget) Get(key cipher.SHA256) (val []byte, err error) {

	var reply msg.Msg
	if reply, err = c.c.sendRequest(&msg.RqObject{Key: key}); err != nil {
		return
	}

	switch x := reply.(type) {
	case *msg.Object:
		if cipher.SumSHA256(x.Value) != key {
			return nil, errors.New("wrong object received (different hash)")
		}
		val = x.Value
	case *msg.Err:
		return nil, errors.New("error: " + x.Err)
	default:
		return nil, fmt.Errorf("invalid msg type received: %T", reply)
	}

	return
}

func (c *Conn) getter() (cg skyobject.Getter) {
	return &cget{c}
}

// Close the Conn
func (c *Conn) Close() (err error) {
	close(c.closeq)
	c.await.Wait()

	// TODO: c.await.Wait() can exit beofre connection is removed from cache.
	// TODO: get errors from background goroutines and retrun them.

	return nil
}

func (c *Conn) nextSeq() uint32 {
	return atomic.AddUint32(&c.seq, 1)
}

func (c *Conn) encodeMsg(seq, rseq uint32, m msg.Msg) (raw []byte) {

	var em = m.Encode()

	raw = make([]byte, 8, 8+len(em))

	binary.LittleEndian.PutUint32(raw, seq)
	binary.LittleEndian.PutUint32(raw[4:], rseq)

	raw = append(raw, em...)

	return

}

func (c *Conn) sendMsg(seq, rseq uint32, m msg.Msg) error {
	c.n.Debugf(MsgSendPin, "[%s] send %d %T", c.String(), rseq, m)

	select {
	case <-c.closeq:
		return ErrClosed
	default:
	}

	c.sendq <- c.encodeMsg(seq, rseq, m)

	return nil
}

func (c *Conn) receiveMsg() error {
	c.n.Debugf(ConnPin, "[%s] receiving", c.String())

	receiveq := c.GetChanIn()

	// Read raw messages from transport.Connection.
	// Terminate if connection is closed or if close signal is sent.
	for {
		select {
		case raw, ok := <-receiveq:
			if ok == false {
				return errors.New("connection closed")
			}

			// Check message size.
			// [ 4 seq ][ 4 rseq ][ 1 msg type ]
			if len(raw) < 9 {
				return errors.New("invalid message size")
			}

			// Decode message.
			var (
				seq    = binary.LittleEndian.Uint32(raw[:4])
				rseq   = binary.LittleEndian.Uint32(raw[4:8])
				msgRaw = raw[8:]
			)
			msg, err := msg.Decode(msgRaw)
			if err != nil {
				return fmt.Errorf("failed to decode message: %s", err)
			}

			c.n.Debugf(MsgReceivePin, "[%s] receive %T", c.String(), msg)

			// Handle message.
			if rq, ok := c.isResponse(rseq); ok == true {
				rq <- msg
				continue
			}
			if err := c.handle(seq, msg); err != nil {
				return fmt.Errorf("failed to handle message %s", err)
			}

		case <-c.closeq:
			return nil
		}
	}
}

func (c *Conn) isResponse(rseq uint32) (rq chan<- msg.Msg, ok bool) {
	c.mx.Lock()
	defer c.mx.Unlock()

	rq, ok = c.reqs[rseq]
	return
}

func (c *Conn) addRequest(seq uint32, rq chan<- msg.Msg) {
	c.mx.Lock()
	defer c.mx.Unlock()

	c.reqs[seq] = rq
}

func (c *Conn) delRequest(seq uint32) {
	c.mx.Lock()
	defer c.mx.Unlock()

	delete(c.reqs, seq)
}

func (c *Conn) responseTimeout() (rt time.Duration) {
	if c.IsTCP() == true {
		rt = c.n.config.TCP.ResponseTimeout
	} else {
		rt = c.n.config.UDP.ResponseTimeout
	}
	return
}

func (c *Conn) sendRequest(m msg.Msg) (reply msg.Msg, err error) {

	c.n.Debugf(MsgSendPin, "[%s] sendRequest %T", c.String(), m)

	var (
		tr *time.Timer
		tc <-chan time.Time
	)

	if rt := c.responseTimeout(); rt > 0 {
		tr = time.NewTimer(rt)
		tc = tr.C

		defer tr.Stop()
	}

	var (
		rq  = make(chan msg.Msg, 1)
		seq = c.nextSeq()
	)

	c.addRequest(seq, rq)
	defer c.delRequest(seq)

	c.sendMsg(seq, 0, m)

	select {
	case reply = <-rq:
		return

	case <-tc:
		return nil, ErrTimeout

	case <-c.closeq:
		return nil, ErrClosed
	}

}

func (c *Conn) sendErr(rseq uint32, err error) {
	c.sendMsg(c.nextSeq(), rseq, &msg.Err{Err: err.Error()})
}

func (c *Conn) sendOk(rseq uint32) {
	c.sendMsg(c.nextSeq(), rseq, &msg.Ok{})
}

// handle messeges except responses and handshakes
func (c *Conn) handle(seq uint32, m msg.Msg) (err error) {

	switch x := m.(type) {

	// subscriptions

	case *msg.Sub: // <- Sub (feed)
		return c.handleSub(seq, x)

	case *msg.Unsub: // <- Unsub (feed)
		return c.handleUnsub(seq, x)

	// public server features

	case *msg.RqList: // <- RqList ()
		return c.handleRqList(seq, x)

	// the *List is response and handled outside the handle()

	// root (push)

	case *msg.Root: // <- Root (feed, nonce, seq, sig, val)
		return c.handleRoot(x)

	// objects

	case *msg.RqObject: // <- RqO (key, prefetch)
		c.await.Add(1)
		go c.handleRqObject(seq, x)
		return

	// preview

	case *msg.RqPreview: // -> RqPreview (feed)
		return c.handleRqPreview(seq, x)

	// peer exchange

	case *msg.RqPeers: // -> RqPeers (feed)
		return c.handleRqPeers(seq, x)

	//
	// delayed messeges (ignore them)
	//
	// the delayed messeges are responses that received
	// after timeout, e.g. the requst is closed with
	// ErrTimeout and noone waits them

	case *msg.Object: // -> O (delayed)
	case *msg.Err: // -> Err (delayed)
	case *msg.Ok: // -> Ok (delayed)
	case *msg.List: // -> List (delayed)
	case *msg.Peers: // -> Peers (delayed)

	default:

		return fmt.Errorf("invalid messege type %T", m)

	}

	return

}

// subscribe (with reply)
func (c *Conn) handleSub(seq uint32, sub *msg.Sub) (_ error) {

	c.n.Debugf(MsgReceivePin, "[%s] handleSub %s",
		c.String(), sub.Feed.Hex()[:7])

	// don't allow blank

	if sub.Feed == (cipher.PubKey{}) {
		return errors.New("blank public key") // fatal (invalid request)
	}

	// check first
	if c.n.fs.hasConnFeed(c, sub.Feed) == true {
		c.sendOk(seq) // already subscribed
		return
	}

	// callback
	var reject = c.n.onSubscribeRemote(c, sub.Feed)

	// reject subscription by callback
	if reject != nil {
		c.sendErr(seq, reject)
		return
	}

	// the callback can subscibe the node to the feed,
	// and anyway we can't subscribe to a feed we don't
	// share

	if c.n.fs.hasFeed(sub.Feed) == false {
		c.sendErr(seq, errors.New("do not share the feed"))
		return
	}

	// ok

	c.n.fs.addConnFeed(c, sub.Feed)
	c.sendOk(seq)

	c.sendLastRoot(sub.Feed) // and push last Root

	return
}

// unsubscribe (no reply)
func (c *Conn) handleUnsub(seq uint32, unsub *msg.Unsub) (err error) {

	c.n.Debugf(MsgReceivePin, "[%s] handleUnsub %s",
		c.String(), unsub.Feed.Hex()[:7])

	if unsub.Feed == (cipher.PubKey{}) {
		return errors.New("invalid request Unsub blank feed") // fatal
	}

	c.n.fs.delConnFeed(c, unsub.Feed) // delete
	return
}

// request list of feeds
func (c *Conn) handleRqList(seq uint32, rq *msg.RqList) (_ error) {

	c.n.Debugf(MsgReceivePin, "[%s] handleRqList", c.String())

	if c.n.config.Public == false {
		c.sendErr(seq, ErrNotPublic)
		return
	}

	c.sendMsg(c.nextSeq(), seq, &msg.List{
		Feeds: c.n.Feeds(),
	})

	return
}

// got Root (preview Root objects are handled by request-responnse, not here)
func (c *Conn) handleRoot(root *msg.Root) (_ error) {

	c.n.Debugf(MsgReceivePin, "[%s] handleRoot %s/%d/%d",
		c.String(), root.Feed.Hex()[:7], root.Nonce, root.Seq)

	// check seq first (avoid verify-signature for old unwanted Root objects)

	var last, err = c.n.c.LastRootSeq(root.Feed, root.Nonce) // last is full

	switch err {
	case data.ErrNoSuchFeed:

		return // unexpected Root

	case data.ErrNoSuchHead, data.ErrNotFound:

		// go dow

	default: // nil (found)

		if last >= root.Seq {
			return // we have newer one
		}

	}

	var r *registry.Root

	r, err = c.n.c.ReceivedRoot(root.Feed, root.Sig, root.Value)

	if err != nil {
		c.n.Printf("[ERR] [%s] received Root error: %s", c.String(), err)
		return // keep connection ?
	}

	// do nothing, because the Node already have this Root
	if r.IsFull == true {
		return
	}

	// fill the Root only if the node and the connection
	// subscribed to feed of the Root
	c.n.fs.receivedRoot(c, r)
	return
}

// async
func (c *Conn) handleRqObject(seq uint32, rq *msg.RqObject) {
	defer c.await.Done()

	c.n.Debugf(MsgReceivePin, "[%s] handleRqObject %s", c.String(),
		rq.Key.Hex()[:7])

	var (
		gc = make(chan skyobject.Object, 1)

		tm *time.Timer
		tc <-chan time.Time
	)

	if err := c.n.c.Want(rq.Key, gc, 0); err != nil {
		c.n.Fatal("DB failure: ", err)
	}
	defer c.n.c.Unwant(rq.Key, gc) // to be memory safe

	select {
	case obj := <-gc:
		// got
		c.sendMsg(c.nextSeq(), seq, &msg.Object{Value: obj.Val})
		return
	default:
		// wait
	}

	if rt := c.responseTimeout(); rt > 0 {
		tm = time.NewTimer(rt)
		tc = tm.C

		defer tm.Stop()
	}

	select {
	case obj := <-gc:
		c.sendMsg(c.nextSeq(), seq, &msg.Object{Value: obj.Val})
	case <-tc:
		c.sendMsg(c.nextSeq(), seq, &msg.Err{}) // timeout
	case <-c.closeq:
		// closed
	}

	return
}

func (c *Conn) handleRqPreview(seq uint32, rqp *msg.RqPreview) (_ error) {

	c.n.Debugf(MsgReceivePin, "[%s] handleRqPreview %s", c.String(),
		rqp.Feed.Hex()[:7])

	var r, err = c.n.c.LastRoot(rqp.Feed, c.n.c.ActiveHead(rqp.Feed))

	if err != nil {
		c.sendMsg(c.nextSeq(), seq, &msg.Err{Err: err.Error()})
		return
	}

	c.sendMsg(c.nextSeq(), seq, &msg.Root{
		Feed:  r.Pub,
		Nonce: r.Nonce,
		Seq:   r.Seq,

		Value: r.Encode(),

		Sig: r.Sig,
	})

	return
}

func (c *Conn) handleRqPeers(seq uint32, rqp *msg.RqPeers) error {

	c.n.Debugf(MsgReceivePin, "[%s] handleRqPeers %s", c.String(),
		rqp.Feed.Hex()[:7])

	s, ok := c.n.InSwarm(rqp.Feed)
	if !ok {
		c.sendMsg(c.nextSeq(), seq, &msg.Err{Err: "not in swarm"})
		return errors.New("node in not in swarm")
	}

	peers := s.peersForExchange(c.PeerID())

	c.n.Debugf(PEXPin, "sending info about %d peers of feed %s to peer %s, addr %s",
		len(peers), rqp.Feed.Hex()[:8], c.PeerID().Hex()[:8], c.Address())

	c.sendMsg(c.nextSeq(), seq, &msg.Peers{
		Feed: rqp.Feed,
		List: peers,
	})

	return nil
}
