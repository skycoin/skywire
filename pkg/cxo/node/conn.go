// Package node pkg/cxo/node/conn.go c2-net-cxo
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
	// initClosed is set true the first time onConnInit or onConnInitErr
	// publishes the init result on this Conn. Atomic CAS-gated — pre-
	// shard this was a plain bool under Node.mx, but the conn-map
	// sharding moved the init signal off the global mutex (so accept
	// and removeConn paths no longer serialize on init), and the
	// guard had to follow. Guards close(initq) against a double-close
	// race: pre-fix the isPending branch of Node.initConn called
	// onConnInitErr a second time after its waitForInit returned the
	// error already published by the isNew handshaker, panicking the
	// whole visor with "close of closed channel" under sustained
	// handshake-failure load (e.g. when dmsg-discovery is CPU-starved
	// and every per-peer ConnectPK times out). Idempotency here means
	// any future caller of onConnInit/onConnInitErr can't reintroduce
	// the panic even if a new code path adds a redundant signal.
	initClosed atomic.Bool

	closeq    chan struct{}  // signal for all goroutines to exit
	closeOnce sync.Once      // guards close(closeq) — see signalClose
	doneq     chan struct{}  // closed when run() has fully completed (maps cleaned, transport closed)
	await     sync.WaitGroup // wait for all goroutines to exit

	// lastActivityNs is the UnixNano of the most recent successful
	// receiveMsg. Updated on every inbound message — chat traffic,
	// heartbeats, request responses, anything. The idle watchdog
	// goroutine (started in run()) closes the Conn when lastActivityNs
	// has been stale longer than idleWatchdogThreshold, on the
	// assumption that a half-dead underlying transport that never
	// errors on Read would otherwise hold the Conn open indefinitely.
	//
	// Surfaces in PR #2643's `connIsAlive` chain — once the watchdog
	// fires Close, closeq closes, connIsAlive returns false on next
	// ConnectPK, fresh dial replaces the dead Conn.
	lastActivityNs atomic.Int64

	sendq chan<- transport.Frame // channel from transport.Connection

	// # stat
	//
	// Stat collection uses the connection mutex; lock-free counters could reduce overhead.
	//
	// ------

	mx sync.RWMutex // locks all fields below

	// request - response
	seq  uint32                    // message seq number (for request-response)
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
		doneq:  make(chan struct{}),

		sendq: fc.GetChanOut(),

		reqs: make(map[uint32]chan<- msg.Msg),
	}

	return c
}

func (c *Conn) waitForInit() error {
	<-c.initq
	return c.initErr
}

// run defines connection lifecycle. It blocks until the connection is fully
// cleaned up (removed from node maps, transport closed, OnDisconnect called).
func (c *Conn) run() {
	// Receive messages, until close signal is received or error occurs.
	var rcvErr error
	c.await.Add(1)
	go func() {
		defer c.await.Done()
		if rcvErr = c.receiveMsg(); rcvErr != nil {
			c.signalClose()
		}
	}()

	// The idle watchdog that closes a Conn after idleWatchdogThreshold
	// of silence (half-dead-transport case: dmsg session gone but
	// io.ReadFull never errors) is no longer per-conn. One shared
	// Node.connReaper walks all active conns on the same interval —
	// same detection guarantee, minus a goroutine+ticker per conn.

	// If OnConnect returns error, connection will be closed.
	var occErr error
	if occErr = c.n.onConnect(c); occErr != nil {
		c.signalClose()
	}

	// Wait for all goroutines to exit.
	c.await.Wait()

	// Remove from node maps (must happen before signaling done).
	c.n.removeConn(c)

	// Remove from transport cache and close the underlying connection.
	// IsTCP() distinguishes TCP from UDP, but DMSG connections also
	// have IsTCP()==false and don't have a UDP transport. Nil-check
	// each so a DMSG-only node (NewWithDMSG path: TCP/UDP listeners
	// disabled) doesn't panic when its conns close.
	//
	// DMSG cleanup runs unconditionally for any node that has a DMSG
	// transport: closeConn is a no-op if c isn't in the DMSG cache,
	// and without this, a DMSG-only node never evicted dead conns —
	// turning a single tpd restart into "this publisher's AnnounceTo
	// returns the dead cached conn forever" until the publisher
	// itself restarted.
	if c.IsTCP() {
		if c.n.tcp != nil {
			c.n.tcp.closeConn(c.Address()) //nolint:errcheck,gosec
		}
	} else if c.n.udp != nil {
		c.n.udp.closeConn(c.Address()) //nolint:errcheck,gosec
	}
	if c.n.dmsg != nil {
		c.n.dmsg.closeConn(c)
	}

	// Determine which error to report.
	var odcErr error
	switch {
	case rcvErr != nil:
		odcErr = rcvErr
	case occErr != nil:
		odcErr = occErr
	}
	c.n.onDisconnect(c, odcErr)

	// Signal that the full cleanup is complete.
	close(c.doneq)
}

// Idle watchdog timing constants. The threshold is set above any
// realistic gap between heartbeats — a healthy publisher emits
// heartbeats every 30s, and the watchdog fires only after roughly
// three consecutive missed beats. That keeps false-positives away
// from steady-state Conns while still catching half-dead transports
// within ~2 ticks of going silent.
//
// Pick interval relative to threshold: 30s interval means worst-case
// detection is threshold + 30s. With threshold=90s, worst case is
// 120s (faster than the prior #2606 stale-detect+reconnect cycle's
// ~150s recovery latency observed in T2-redux2-prep).
const (
	idleWatchdogThreshold = 90 * time.Second
	idleWatchdogInterval  = 30 * time.Second
)

// The idle-connection watchdog now lives at the Node level as the
// single shared Node.connReaper goroutine (node.go); the per-Conn
// idleWatchdog method it replaced cost one goroutine + one time.Ticker
// per connection. lastActivityNs (seeded/bumped by receiveMsg) and the
// idleWatchdog* constants remain here as the reaper's inputs.

func (c *Conn) decodeRaw(raw []byte) (seq, rseq uint32, m msg.Msg, err error) {

	if len(raw) < 9 {
		err = errors.New("invalid message received: too short")
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
	return c.incoming == false //nolint:staticcheck
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
	if isIncoming == true { //nolint:staticcheck
		s = "↓ "
	} else {
		s = "↑ "
	}

	if isTCP == true { //nolint:staticcheck
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
	c.sendMsg(c.nextSeq(), 0, &msg.Root{ //nolint:errcheck,gosec
		Feed:  r.Pub,
		Nonce: r.Nonce,
		Seq:   r.Seq,

		Value: r.Encode(),

		Sig: r.Sig,
	})
}

// encodeRootBody encodes the msg.Root wire body for r ONCE, so a broadcast can
// hand the same immutable []byte to every subscriber via sendSharedBody instead
// of re-running the (expensive) r.Encode() + msg.Root.Encode() per connection.
// It is exactly the body sendRoot builds per-conn; only the 8-byte header differs.
func encodeRootBody(r *registry.Root) []byte {
	return (&msg.Root{
		Feed:  r.Pub,
		Nonce: r.Nonce,
		Seq:   r.Seq,
		Value: r.Encode(),
		Sig:   r.Sig,
	}).Encode()
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

	// A peer subscribed to a feed we don't have a Root for yet
	// (activeHead=0 / "no such head") — benign and expected during
	// startup and for any feed we haven't published to. There's
	// nothing to send and nothing actionable; the peer receives the
	// Root once we publish one. Pin-gated debug instead of an always-on
	// [WARN] so it stops spamming the visor's log at INFO.
	c.n.Debugf(MsgSendPin, "[%s] sendLastRoot %s: %v (activeHead=%d)",
		c.String(), pk.Hex(), err, activeHead)

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
		return err
	}

	var reply msg.Msg

	if reply, err = c.sendRequest(&msg.Sub{Feed: feed}); err != nil {
		return err
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
		return err
	}

	c.n.fs.addConnFeed(c, feed)
	c.sendLastRoot(feed)
	return err
}

// just send the message
func (c *Conn) unsubscribe(pk cipher.PubKey) {
	c.sendMsg(c.nextSeq(), 0, &msg.Unsub{ //nolint:errcheck,gosec
		Feed: pk,
	})
}

// Unsubscribe from given feed of remote peer
func (c *Conn) Unsubscribe(feed cipher.PubKey) {
	c.n.fs.delConnFeed(c, feed)
	c.unsubscribe(feed) // notify peer
	return              //nolint:staticcheck,gofmt
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
		return err
	}

	var r *registry.Root

	switch x := reply.(type) {
	case *msg.Err:
		return errors.New("error: " + x.Err)
	case *msg.Root:
		if r, err = c.n.c.PreviewRoot(x.Feed, x.Sig, x.Value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid msg type received: %T", reply)
	}

	var p *skyobject.Preview
	if p, err = c.n.c.Preview(r, c.getter()); err != nil {
		return err
	}

	if previewFunc(p, r) == true { //nolint:staticcheck
		err = c.Subscribe(feed)
	}

	return err
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

// signalClose idempotently closes closeq to signal shutdown. Multiple paths
// race to signal it — run()'s receiveMsg/onConnect failure branches, the idle
// watchdog, and external Close() (including concurrent closeAll iteration) — so
// the close MUST be serialized: a plain `select { case <-closeq: default:
// close(closeq) }` has a check-then-close TOCTOU window where two goroutines
// both take the default branch and double-close (panic: close of closed
// channel). sync.Once removes that window.
func (c *Conn) signalClose() {
	c.closeOnce.Do(func() { close(c.closeq) })
}

// Close the Conn
// Close signals the connection to shut down. The connection is fully cleaned
// up asynchronously by run(). Use Done() to wait for full cleanup if needed.
func (c *Conn) Close() (err error) {
	c.signalClose()
	return nil
}

// Done returns a channel that is closed when run() has fully completed
// (connection removed from node maps, transport closed, OnDisconnect called).
func (c *Conn) Done() <-chan struct{} {
	return c.doneq
}

func (c *Conn) nextSeq() uint32 {
	return atomic.AddUint32(&c.seq, 1)
}

// msgHead builds the 8-byte per-message header (seq + rseq).
func msgHead(seq, rseq uint32) []byte {
	head := make([]byte, 8)
	binary.LittleEndian.PutUint32(head, seq)
	binary.LittleEndian.PutUint32(head[4:], rseq)
	return head
}

// encodeMsg builds the outbound frame for m: the per-conn Head plus the encoded
// Body. Kept as a split Frame (rather than one concatenated buffer) so a
// broadcast can encode the Body once and share it across every subscriber — see
// transport.Frame and sendSharedBody.
func (c *Conn) encodeMsg(seq, rseq uint32, m msg.Msg) transport.Frame {
	return transport.Frame{Head: msgHead(seq, rseq), Body: m.Encode()}
}

// sendMsgQueueTimeout is the max time sendMsg will wait to enqueue a
// message into the per-conn sendq. The underlying transport.Connection
// buffers 256 frames in the `out` channel; if the buffer is full it
// means the writeLoop is stuck (e.g. the underlying net.Conn is in a
// silent half-close — the kernel TCP write hasn't surfaced an error
// yet, but the writer isn't draining either).
//
// Pre-fix sendMsg waited indefinitely on `c.sendq <- ...`. A single
// stuck subscriber would block broadcastRoot's sequential iteration
// over n.cs, so the publisher's runLoop hung and NO subscriber got
// the next root push — symptom: peers reported group heartbeats
// silently stopped arriving and detectStaleActive eventually fired
// across all subs in lockstep.
//
// Bounded with a short timeout: if the buffer can't drain in N
// seconds the conn is declared dead, Close fires (idempotent), and
// the conn's run() loop removes it from the nodeFeeds map so future
// broadcasts skip it. The bounded path means a single dead sub costs
// at most one timeout's worth of latency on the broadcast — bad but
// bounded — instead of stopping pushes entirely.
const sendMsgQueueTimeout = 5 * time.Second

func (c *Conn) sendMsg(seq, rseq uint32, m msg.Msg) error {
	c.n.Debugf(MsgSendPin, "[%s] send %d %T", c.String(), rseq, m)
	return c.sendFrame(c.encodeMsg(seq, rseq, m))
}

// sendSharedBody queues a frame whose Body is a caller-owned, immutable []byte
// that MAY be shared across connections — the broadcast fast path: encode the
// (large) message body once, then hand every subscriber the same Body with only
// its own per-conn Head. The transport writes Body without copying, so N
// subscribers cost one Body allocation, not N.
func (c *Conn) sendSharedBody(seq, rseq uint32, body []byte) error {
	return c.sendFrame(transport.Frame{Head: msgHead(seq, rseq), Body: body})
}

// sendFrame enqueues an already-built frame onto the per-conn send queue,
// bounding the wait so one stuck subscriber can't stall a broadcast (see
// sendMsgQueueTimeout).
func (c *Conn) sendFrame(f transport.Frame) error {
	select {
	case <-c.closeq:
		return ErrClosed
	default:
	}

	// Fast path: buffer has room, enqueue immediately.
	select {
	case c.sendq <- f:
		return nil
	default:
	}

	// Slow path: buffer is full. Bound the wait so a stuck subscriber
	// doesn't stall the publisher's broadcastRoot iteration. closeq
	// is also selected so a concurrent Close unblocks us cleanly.
	select {
	case c.sendq <- f:
		return nil
	case <-c.closeq:
		return ErrClosed
	case <-time.After(sendMsgQueueTimeout):
		c.n.sendMsgTimeoutCount.Add(1)
		c.n.deadConnsClosed.Add(1)
		c.n.Printf("[WARN] [%s] sendMsg: queue full for %s; declaring conn dead and closing",
			c.String(), sendMsgQueueTimeout)
		_ = c.Close() //nolint:errcheck,gosec
		return ErrClosed
	}
}

func (c *Conn) receiveMsg() error {
	c.n.Debugf(ConnPin, "[%s] receiving", c.String())

	receiveq := c.GetChanIn()

	// Initialize activity timestamp so the watchdog has a starting
	// point — without this, a Conn that handshakes but never receives
	// chat traffic looks idle from t=0 and the watchdog would fire
	// the moment it starts.
	c.lastActivityNs.Store(time.Now().UnixNano())

	// Read raw messages from transport.Connection.
	// Terminate if connection is closed or if close signal is sent.
	for {
		select {
		case raw, ok := <-receiveq:
			if ok == false { //nolint:staticcheck
				return errors.New("connection closed")
			}

			// Bump activity on every inbound — chat traffic, heartbeats,
			// request responses. The watchdog reads this to decide whether
			// the Conn has gone idle past the threshold.
			c.lastActivityNs.Store(time.Now().UnixNano())

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
			if rq, ok := c.isResponse(rseq); ok == true { //nolint:staticcheck
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
	c.mx.RLock()
	defer c.mx.RUnlock()

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
	if c.IsTCP() == true { //nolint:staticcheck
		rt = c.n.config.TCP.ResponseTimeout
	} else {
		rt = c.n.config.UDP.ResponseTimeout
	}
	if rt <= 0 {
		// A non-positive response timeout leaves the per-request wait channel
		// (tc) nil, so an object request to a peer that never delivers pins its
		// wanted-cache entry + handler goroutine for the whole connection
		// lifetime. Treat 0 as "use the default", not "disable" — a disabled
		// per-request timeout is a footgun, not a feature.
		rt = ResponseTimeout
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

	if err := c.sendMsg(seq, 0, m); err != nil {
		return nil, err
	}

	select {
	case reply = <-rq:
		return reply, err

	case <-tc:
		return nil, ErrTimeout

	case <-c.closeq:
		return nil, ErrClosed
	}

}

func (c *Conn) sendErr(rseq uint32, err error) {
	c.sendMsg(c.nextSeq(), rseq, &msg.Err{Err: err.Error()}) //nolint:errcheck,gosec
}

func (c *Conn) sendOk(rseq uint32) {
	c.sendMsg(c.nextSeq(), rseq, &msg.Ok{}) //nolint:errcheck,gosec
}

// handle messages except responses and handshakes
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
		return err

	// preview

	case *msg.RqPreview: // -> RqPreview (feed)
		return c.handleRqPreview(seq, x)

	// peer exchange

	case *msg.RqPeers: // -> RqPeers (feed)
		return c.handleRqPeers(seq, x)

	//
	// delayed messages (ignore them)
	//
	// the delayed messages are responses that received
	// after timeout, e.g. the requst is closed with
	// ErrTimeout and noone waits them

	case *msg.Object: // -> O (delayed)
	case *msg.Err: // -> Err (delayed)
	case *msg.Ok: // -> Ok (delayed)
	case *msg.List: // -> List (delayed)
	case *msg.Peers: // -> Peers (delayed)

	default:

		return fmt.Errorf("invalid message type %T", m)

	}

	return err

}

// subscribe (with reply)
func (c *Conn) handleSub(seq uint32, sub *msg.Sub) (_ error) {

	c.n.Debugf(MsgReceivePin, "[%s] handleSub %s",
		c.String(), sub.Feed.Hex())

	// don't allow blank

	if sub.Feed == (cipher.PubKey{}) {
		return errors.New("blank public key") // fatal (invalid request)
	}

	// Idempotent Subscribe: even when the conn is already registered for
	// this feed, push the current Root again. Subscribers re-issue
	// Subscribe on reconnect (via ReconnectPeer → conn.Subscribe), and
	// the only signal they have that we accepted is Ok. Without a Root
	// follow-up, a subscriber whose previous fill failed (transient
	// dmsg blip, partial fetch) has no way to recover — it stays Ok'd
	// with no backlog and only sees Roots published after its
	// reconnect lands. Empirically observed: post-restart subscriber
	// joined a publisher mid-burst, received probebX-19+ live but
	// missed probeb-01..18 entirely because that initial fill aborted
	// silently and the publisher never re-pushed.
	//
	// sendLastRoot is cheap — one fetch from the local Container plus
	// one message send. Repeating it on duplicate subscribes is the
	// minimum-surface fix that doesn't require new message types or
	// subscriber-side recovery logic.
	if c.n.fs.hasConnFeed(c, sub.Feed) == true { //nolint:staticcheck
		c.sendOk(seq)            // already subscribed
		c.sendLastRoot(sub.Feed) // push current Root anyway
		return nil
	}

	// callback
	var reject = c.n.onSubscribeRemote(c, sub.Feed)

	// reject subscription by callback
	if reject != nil {
		c.sendErr(seq, reject)
		return nil
	}

	// the callback can subscibe the node to the feed,
	// and anyway we can't subscribe to a feed we don't
	// share

	if c.n.fs.hasFeed(sub.Feed) == false { //nolint:staticcheck
		c.sendErr(seq, errors.New("do not share the feed"))
		return nil
	}

	// ok

	c.n.fs.addConnFeed(c, sub.Feed)
	c.sendOk(seq)

	c.sendLastRoot(sub.Feed) // and push last Root

	return nil
}

// unsubscribe (no reply)
func (c *Conn) handleUnsub(seq uint32, unsub *msg.Unsub) (err error) { //nolint:unparam

	c.n.Debugf(MsgReceivePin, "[%s] handleUnsub %s",
		c.String(), unsub.Feed.Hex())

	if unsub.Feed == (cipher.PubKey{}) {
		return errors.New("invalid request Unsub blank feed") // fatal
	}

	c.n.fs.delConnFeed(c, unsub.Feed) // delete
	return
}

// request list of feeds
func (c *Conn) handleRqList(seq uint32, rq *msg.RqList) (_ error) { //nolint:unparam

	c.n.Debugf(MsgReceivePin, "[%s] handleRqList", c.String())

	if c.n.config.Public == false { //nolint:staticcheck
		c.sendErr(seq, ErrNotPublic)
		return
	}

	c.sendMsg(c.nextSeq(), seq, &msg.List{ //nolint:errcheck,gosec
		Feeds: c.n.Feeds(),
	})

	return
}

// got Root (preview Root objects are handled by request-responnse, not here)
func (c *Conn) handleRoot(root *msg.Root) (_ error) {

	c.n.Debugf(MsgReceivePin, "[%s] handleRoot %s/%d/%d",
		c.String(), root.Feed.Hex(), root.Nonce, root.Seq)

	// check seq first (avoid verify-signature for old unwanted Root objects)

	var last, err = c.n.c.LastRootSeq(root.Feed, root.Nonce) // last is full

	switch err {
	case data.ErrNoSuchFeed:

		return nil // unexpected Root

	case data.ErrNoSuchHead, data.ErrNotFound:

		// go dow

	default: // nil (found)

		if last >= root.Seq {
			return nil // we have newer one
		}

	}

	var r *registry.Root

	r, err = c.n.c.ReceivedRoot(root.Feed, root.Sig, root.Value)

	if err != nil {
		c.n.Printf("[ERR] [%s] received Root error: %s", c.String(), err)
		return nil // keep connection ?
	}

	// do nothing, because the Node already have this Root
	if r.IsFull == true { //nolint:staticcheck
		return nil
	}

	// fill the Root only if the node and the connection
	// subscribed to feed of the Root
	c.n.fs.receivedRoot(c, r)
	return nil
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
		// A peer requested an object we can't provide (a "not found" DB miss is
		// the common case — we simply don't hold it, or don't hold it yet).
		// This is recoverable: tell the requesting peer we can't serve it and
		// move on. A peer's object request must NEVER crash this visor.
		c.n.Errorf(err, "[%s] handleRqObject: cannot serve %s",
			c.String(), rq.Key.Hex()[:7])
		c.sendMsg(c.nextSeq(), seq, &msg.Err{}) //nolint:errcheck,gosec
		return
	}
	defer c.n.c.Unwant(rq.Key, gc) // to be memory safe

	select {
	case obj := <-gc:
		// got
		c.sendMsg(c.nextSeq(), seq, &msg.Object{Value: obj.Val}) //nolint:errcheck,gosec
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
		c.sendMsg(c.nextSeq(), seq, &msg.Object{Value: obj.Val}) //nolint:errcheck,gosec
	case <-tc:
		c.sendMsg(c.nextSeq(), seq, &msg.Err{}) //nolint:errcheck,gosec // timeout
	case <-c.closeq:
		// closed
	}

	return //nolint:staticcheck
}

func (c *Conn) handleRqPreview(seq uint32, rqp *msg.RqPreview) (_ error) {

	c.n.Debugf(MsgReceivePin, "[%s] handleRqPreview %s", c.String(),
		rqp.Feed.Hex())

	var r, err = c.n.c.LastRoot(rqp.Feed, c.n.c.ActiveHead(rqp.Feed))

	if err != nil {
		c.sendMsg(c.nextSeq(), seq, &msg.Err{Err: err.Error()}) //nolint:errcheck,gosec
		return
	}

	c.sendMsg(c.nextSeq(), seq, &msg.Root{ //nolint:errcheck,gosec
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
		rqp.Feed.Hex())

	s, ok := c.n.InSwarm(rqp.Feed)
	if !ok {
		c.sendMsg(c.nextSeq(), seq, &msg.Err{Err: "not in swarm"}) //nolint:errcheck,gosec
		return errors.New("node in not in swarm")
	}

	peers := s.peersForExchange(c.PeerID())

	c.n.Debugf(PEXPin, "sending info about %d peers of feed %s to peer %s, addr %s",
		len(peers), rqp.Feed.Hex(), c.PeerID().Hex(), c.Address())

	c.sendMsg(c.nextSeq(), seq, &msg.Peers{ //nolint:errcheck,gosec
		Feed: rqp.Feed,
		List: peers,
	})

	return nil
}
