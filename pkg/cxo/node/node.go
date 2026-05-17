package node

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/node/log"
	"github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/statutil"
)

// A Node represents network P2P transport
// for CX objects. The node used to receive,
// send and retransmit CX objects. The Node
// cares about last Root object of active
// head (see skyobject.Index.ActiveHead) of
// a feed. E.g. the Node never replicates an
// old Root if there is a newer one. The Node
// uses TCP and UDP transports.
type Node struct {
	mx sync.Mutex // lock

	log.Logger                      // logger
	c          *skyobject.Container // related Container

	idpk cipher.PubKey // node identity public key
	idsk cipher.SecKey // node identity secret key

	//
	// feeds and connections
	//

	fs *nodeFeeds // feeds

	pendConns  map[string]*Conn        // peer addr -> pending connection
	addrToConn map[string]*Conn        // peer addr -> connection
	pkToConn   map[cipher.PubKey]*Conn // peer pubkey (ID) -> connection

	ss map[cipher.PubKey]*Swarm // swarms

	//
	// transports
	//

	// listen and connect
	tcp  *TCP
	udp  *UDP
	dmsg *DMSG // optional DMSG transport

	//
	// other
	//

	config             *Config // keep config
	maxFillingParallel int     // copy of c.Config().MaxFillingParallel
	rollAvgSamples     int     //nolint:unused // copy of c.Config().RollAvgSamples

	//
	// stat
	//

	fillavg *statutil.Duration // filling average

	// sendMsgTimeoutCount counts sendMsg-queue-full-timeout events.
	// Each increment corresponds to one Conn whose send queue was
	// blocked for sendMsgQueueTimeout (5s by default) — the conn is
	// then closed by sendMsg. Surfaced via PublisherStats so operators
	// can tell whether a publisher fanout is silently shedding peers.
	// Pre-#2538 this would have manifested as a stalled broadcast loop
	// with no observable counter; post-#2538 the conn dies but the
	// fact that it died was only visible in WARN logs.
	sendMsgTimeoutCount atomic.Uint64

	// deadConnsClosed counts Conn instances that sendMsg closed due to
	// the timeout path. Always == sendMsgTimeoutCount today (one
	// timeout = one close), kept separate so a future refactor that
	// adds other auto-close paths (idle detection, frame-error close,
	// etc.) can be counted distinctly.
	deadConnsClosed atomic.Uint64

	//
	// rpc
	//

	rpc *rpcServer

	//
	//  closing
	//

	await  sync.WaitGroup // wait for goroutines
	closeo sync.Once      // close once
}

// NewNode creates new Node instance using provided
// Config. If the Config is nil, then default is used
// (see NewConfig for defaults)
func NewNode(conf *Config) (n *Node, err error) {

	if conf == nil {
		conf = NewConfig() // use defaults
	}

	if conf.Config == nil {
		conf.Config = skyobject.NewConfig() // use defaults
	}

	if err = conf.Validate(); err != nil {
		return // invalid
	}

	var c *skyobject.Container

	if c, err = skyobject.NewContainer(conf.Config); err != nil {
		return
	}

	return NewNodeContainer(conf, c)
}

// NewNodeContainer use provided Container.
// Configurations related to Container ignored.
func NewNodeContainer(
	conf *Config,
	c *skyobject.Container,
) (
	n *Node,
	err error,
) {

	if conf == nil {
		conf = NewConfig() // use defaults
	}

	if err = conf.Validate(); err != nil {
		return n, err // invalid
	}

	n = new(Node)

	// Generate random keypair or derive from provided secret key
	var defSK cipher.SecKey
	if conf.SecKey == defSK {
		n.idpk, n.idsk = cipher.GenerateKeyPair()
	} else {
		if err := conf.SecKey.Verify(); err != nil {
			return nil, fmt.Errorf("invalid secret key - %s", err)
		}
		n.idsk = conf.SecKey
		n.idpk, err = cipher.PubKeyFromSecKey(conf.SecKey)
		if err != nil {
			return nil, fmt.Errorf("failed to derive public key - %s", err)
		}
	}

	n.c = c
	n.fs = newNodeFeeds(n)

	n.pendConns = make(map[string]*Conn)
	n.addrToConn = make(map[string]*Conn)
	n.pkToConn = make(map[cipher.PubKey]*Conn)

	n.ss = make(map[cipher.PubKey]*Swarm)

	n.config = conf
	n.config.Config = c.Config() // actual

	n.fillavg = statutil.NewDuration(conf.Config.RollAvgSamples)

	//
	// create
	//

	// logger

	n.Logger = log.NewLogger(conf.Logger) // logger

	// listen

	if conf.TCP.Listen != "" {
		if err = n.TCP().Listen(conf.TCP.Listen); err != nil {
			n.Close() //nolint:errcheck,gosec
			return n, err
		}
	}

	if conf.UDP.Listen != "" {
		if err = n.UDP().Listen(conf.UDP.Listen); err != nil {
			n.Close() //nolint:errcheck,gosec
			return n, err
		}
	}

	// DMSG transport (set externally via SetDMSG before or after node creation)
	// The DMSG factory and listener are managed by the caller who owns the dmsg.Client.

	// rpc

	if conf.RPC != "" {

		n.rpc = n.newRPC()

		if err = n.rpc.Listen(conf.RPC); err != nil {
			n.Close() //nolint:errcheck,gosec
			return n, err
		}

	}

	// Pings are handled at node level; per-connection pings would improve health granularity.

	return n, err
}

// ID retursn identifier of the Node. The identifier
// is unique random identifier that used to avoid
// cross-connections
func (n *Node) ID() (id cipher.PubKey) {
	return n.idpk
}

// Config returns Config with which the
// Node was created. The Config must not
// be modified. If the Node created using
// NewNodeContainer, then Config field
// replaced with config of given Container
func (n *Node) Config() (conf *Config) {
	return n.config // copy
}

// Container returns related Container instance
func (n *Node) Container() (c *skyobject.Container) {
	return n.c
}

// Publish sends given Root object to peers that
// subscribed to feed of the Root. The Publish used
// to publish new Root objects. E.g. the Node sends
// last Root object of a feed to subsctibers. But
// the Node knows nothing about new Root objects.
// And to share an updated Root, call the Publish.
// And don't call the publish for Root objects that
// alredy saved (that saved before subscription)
func (n *Node) Publish(r *registry.Root) {
	n.fs.broadcastRoot(connRoot{nil, r})
}

// ConnectionsOfFeed returns list of connections of given
// feed. Use blank public key to get all connections that
// does not share a feed
func (n *Node) ConnectionsOfFeed(feed cipher.PubKey) (cs []*Conn) {
	return n.fs.connectionsOfFeed(feed)
}

// Connections returns all established connections
func (n *Node) Connections() (cs []*Conn) {

	n.mx.Lock()
	defer n.mx.Unlock()

	cs = make([]*Conn, 0, len(n.addrToConn))

	for _, c := range n.addrToConn {
		cs = append(cs, c)
	}

	return
}

// PublisherStats is the snapshot of CXO-publisher-side health counters.
// Surfaced upward (treestore.Publisher.Stats() → visor RPC →
// `skywire cli visor doctor`) so operators can spot a publisher
// fanout that's silently shedding subscribers without parsing WARN
// logs.
//
// Counter semantics:
//   - SendMsgTimeouts counts sendMsg-queue-full-timeout events.
//     Each tick is one subscriber whose queue was blocked for the
//     full sendMsgQueueTimeout window before the conn was force-closed.
//     A non-zero counter is the smoking gun for the pre-#2538 stuck
//     publisher pattern, now bounded but worth surfacing.
//   - DeadConnsClosed counts Conn instances closed by sendMsg's
//     timeout path. Today this is always equal to SendMsgTimeouts;
//     kept distinct so future auto-close paths (idle, frame-error,
//     etc.) can be counted without rebasing this struct.
//   - ActiveConnections / ActiveFeeds are point-in-time gauges
//     useful for sanity checking ("did my publisher lose all its
//     subscribers?").
type PublisherStats struct {
	SendMsgTimeouts   uint64 `json:"sendmsg_timeouts"`
	DeadConnsClosed   uint64 `json:"dead_conns_closed"`
	ActiveConnections int    `json:"active_connections"`
	ActiveFeeds       int    `json:"active_feeds"`
}

// Stats returns a snapshot of the Node's publisher-side health
// counters + active gauges. Cheap: atomic loads + map-length read
// under the node mutex.
func (n *Node) Stats() PublisherStats {
	n.mx.Lock()
	conns := len(n.addrToConn)
	n.mx.Unlock()
	return PublisherStats{
		SendMsgTimeouts:   n.sendMsgTimeoutCount.Load(),
		DeadConnsClosed:   n.deadConnsClosed.Load(),
		ActiveConnections: conns,
		ActiveFeeds:       len(n.fs.list()),
	}
}

// don't create TCP in background
// returning nil, if the TCP doesn't
// exist
func (n *Node) getTCP() (t *TCP) {
	n.mx.Lock()
	defer n.mx.Unlock()

	return n.tcp
}

// TCP returns TCP transport of the Node
func (n *Node) TCP() (tcp *TCP) {

	n.mx.Lock()
	defer n.mx.Unlock()

	n.createTCP()

	return n.tcp
}

// don't create TCP in background
// returning nil, if the TCP doesn't
// exist
func (n *Node) getUDP() (u *UDP) { //nolint:unused
	n.mx.Lock()
	defer n.mx.Unlock()

	return n.udp
}

// UDP returns UDP transport of the Node
func (n *Node) UDP() (tcp *UDP) {

	n.mx.Lock()
	defer n.mx.Unlock()

	n.createUDP()

	return n.udp
}

// call under lock of the mx
func (n *Node) createTCP() {

	if n.tcp != nil {
		return // already created
	}

	n.tcp = newTCP(n)

}

// call under lock of the mx
func (n *Node) createUDP() {

	if n.udp != nil {
		return // alrady created
	}

	n.udp = newUDP(n)

}

// DMSG returns the DMSG transport of the Node, or nil if not configured.
func (n *Node) DMSG() *DMSG {
	n.mx.Lock()
	defer n.mx.Unlock()
	return n.dmsg
}

// SetDMSG sets the DMSG transport for this node.
// Must be called before Listen/Connect operations on the DMSG transport.
func (n *Node) SetDMSG(d *DMSG) {
	n.mx.Lock()
	defer n.mx.Unlock()
	n.dmsg = d
}

// EnableDMSG creates and starts a DMSG transport on this node using the given
// DMSG factory. It starts listening for incoming CXO connections over DMSG.
func (n *Node) EnableDMSG(factory *transport.DMSGFactory) error {
	d := newDMSG(n, factory)
	if err := d.Listen(); err != nil {
		return err
	}
	n.SetDMSG(d)
	n.Debugf(NewInConnPin, "CXO DMSG transport enabled on %s", d.Address())
	return nil
}

func (n *Node) onConnect(c *Conn) error {
	if occ := n.config.OnConnect; occ != nil {
		if err := occ(c); err != nil {
			return err
		}
	}

	n.Debugf(ConnEstPin, "[%s] established", c.String())

	return nil
}

func (n *Node) onDisconnect(c *Conn, reason error) {
	if odc := n.config.OnDisconnect; odc != nil {
		odc(c, reason)
	}

	if reason != nil {
		n.Debugf(CloseConnPin, "[%s] closed: %v", c.String(), reason)
	} else {
		n.Debugf(CloseConnPin, "[%s] closed", c.String())
	}
}

func (n *Node) connCap() int {
	n.mx.Lock()
	defer n.mx.Unlock()

	count := len(n.pendConns) + len(n.addrToConn)
	if count >= n.config.MaxConnections {
		return 0
	}

	return n.config.MaxConnections - count
}

func (n *Node) pendingConnCap() int {
	n.mx.Lock()
	defer n.mx.Unlock()

	count := len(n.pendConns)
	if count >= n.config.MaxPendingConnections {
		return 0
	}

	return n.config.MaxPendingConnections - count
}

// initConn initializes new connection.
func (n *Node) initConn(
	fc *transport.Connection, isIncoming bool) (*Conn, error) {

	var (
		addr    = fc.GetRemoteAddr().String()
		connStr = connString(isIncoming, fc.IsTCP(), addr)
	)

	n.Debugf(ConnHskPin, "[%s] init connection", connStr)

	// Get existing connection or create new one.
	c, isNew, isPending, err := n.onNewConn(fc, isIncoming)
	if err != nil {
		return nil, err
	}

	switch {
	// In case of existing connection return it.
	case !isNew && !isPending:
		return c, nil

	// In case of existing pending connnection:
	// 1. Request for new incoming connection from the same address will fail.
	// 2. Request for new outgoing connection to the same address will return existing
	//    connection (or error) after it finishes handshake.
	case !isNew && isPending:
		if isIncoming {
			return nil, errors.New("already have incoming pending connection")
		}
		// Wait for the in-flight isNew goroutine to publish the init
		// result on c.initq. If it fails, that goroutine has already
		// called onConnInitErr (deletes from pendConns, sets initErr,
		// closes initq). The waiter must only propagate the error —
		// pre-fix a redundant onConnInitErr call here double-closed
		// c.initq and panicked the visor with "close of closed channel"
		// under sustained handshake failure (peer offline / dmsg
		// transport CPU-starved). The idempotency guard added in
		// onConnInitErr below makes this defense-in-depth, but the
		// behavioral contract is: waiter propagates, handshaker
		// publishes.
		if err = c.waitForInit(); err != nil {
			return nil, err
		}
		return c, nil

	// In case of new connection perform/accept handshake.
	case isNew:
		if err = c.handshake(); err != nil {
			err = fmt.Errorf("handshake failed: %s", err)
			n.onConnInitErr(c, err)
			return nil, err
		}

		n.onConnInit(c)
		go c.run()

		return c, nil

	default:
		panic("onNewConn return invalid values")
	}
}

// onNewConn atomically checks for existing connection
// to/form address and creates new one if necessary.
func (n *Node) onNewConn(
	fc *transport.Connection, isIncoming bool) (c *Conn, isNew, isPending bool, err error) {

	n.mx.Lock()
	defer n.mx.Unlock()

	// Check limits for number of open connections.
	if len(n.pendConns)+len(n.addrToConn) >= n.config.MaxConnections {
		return nil, false, false, ErrConnLimit
	}
	if len(n.pendConns) >= n.config.MaxPendingConnections {
		return nil, false, false, ErrPendConnLimit
	}

	addr := fc.GetRemoteAddr().String()

	// Check if connection to/from address already exists.
	if c, ok := n.addrToConn[addr]; ok {
		return c, false, false, nil
	}
	if c, ok := n.pendConns[addr]; ok {
		return c, false, true, nil
	}

	// Create new pending connection.
	c = n.newConnection(fc, isIncoming)
	n.pendConns[addr] = c

	return c, true, false, nil
}

// onConnInitErr atomically removes pending connection and signals
// about initConn failure. Idempotent: c.initClosed gates the close
// of initq so a double-call (which pre-fix the isPending branch of
// initConn triggered, double-closing initq and panicking the visor
// under handshake-failure load) becomes a no-op after the first.
func (n *Node) onConnInitErr(c *Conn, initErr error) {
	n.mx.Lock()
	defer n.mx.Unlock()

	if c.initClosed {
		return
	}
	c.initClosed = true

	delete(n.pendConns, c.Address())

	c.initErr = initErr
	close(c.initq)
}

// onConnInit atomically moves pending connection to cache
// and signals about initConn success. It also chekcs if
// peer's pubkey, receieved during hanshake is duplicate.
//
// Mirrors onConnInitErr's c.initClosed gate: success and failure
// must publish exactly one close of initq, regardless of which
// goroutine wins the race for the final write.
func (n *Node) onConnInit(c *Conn) {
	n.mx.Lock()
	defer n.mx.Unlock()

	if c.initClosed {
		return
	}
	c.initClosed = true

	delete(n.pendConns, c.Address())

	n.addrToConn[c.Address()] = c
	n.pkToConn[c.peerID] = c

	close(c.initq)
}

// removeConn reomoves connection from cache.
//
// Identity-checks the slot before deleting: when evictStalePeer
// closes a stale Conn AND its run() drains past evictStalePeer's
// timeout AND the handshake completes AND onConnInit overwrites
// pkToConn[id] with a NEW Conn — all before the old run() finally
// calls removeConn(old) — the old removeConn must NOT wipe the
// slot now pointing at the live new Conn. Same shape for
// addrToConn. Without the identity check, the late removeConn of
// the evicted Conn silently strands the live one (hasPeer returns
// false, feed routing skips the peer) until something else
// re-registers.
func (n *Node) removeConn(c *Conn) {
	n.mx.Lock()
	defer n.mx.Unlock()

	if existing, ok := n.addrToConn[c.Address()]; ok && existing == c {
		delete(n.addrToConn, c.Address())
	}
	if existing, ok := n.pkToConn[c.peerID]; ok && existing == c {
		delete(n.pkToConn, c.peerID)
	}

	// Remove from feed tracking
	n.fs.delConn(c)
}

// Share given feed. By default the Node dousn't share
// a feed. Even if underlying Container have any. To
// start sharing a feed, call this method. Thus, you can
// keep some feeds locally. To share all feeds of the
// Container use following code
//
//	for _, pk := range n.Container().Feeds() {
//	    n.Share(pk)
//	 }
//
// The Share method adds given feed to underlying
// Container calling (*skyobjec.Container).AddFeed()
// method. The method never return an error if given
// feed is already shared. The share never associate
// the feed with a connection. You should to call
// (*Conn).Subscribe to do that.
func (n *Node) Share(feed cipher.PubKey) (err error) {

	if feed == (cipher.PubKey{}) {
		return ErrBlankFeed
	}

	// add to the Container
	if err = n.c.AddFeed(feed); err != nil {
		return
	}

	n.fs.addFeed(feed)

	return
}

// DontShare given feed. The method does't remove the
// feed from underlying Container. The method calls
// Unsubscribe() for all connections that share the feed.
// The method does nothing if the Node doesn't share the
// feed The Node never sync with Container automatically,
// and before removing a feed from Container, call this
// method to prevent sharing feed that does not exist
func (n *Node) DontShare(feed cipher.PubKey) (err error) {

	n.fs.delFeed(feed)

	return
}

func (n *Node) onSubscribeRemote(c *Conn, feed cipher.PubKey) (reject error) {

	if osr := n.config.OnSubscribeRemote; osr != nil {
		reject = osr(c, feed)
	}

	return
}

func (n *Node) onUnsubscribeRemote(c *Conn, feed cipher.PubKey) { //nolint:unused

	if ousr := n.config.OnUnsubscribeRemote; ousr != nil {
		ousr(c, feed)
	}

}

// Feeds the Node share. The reply is read-only
func (n *Node) Feeds() (feeds []cipher.PubKey) {
	return n.fs.list()
}

// IsSharing returns true if the Node is sharing given feed
func (n *Node) IsSharing(feed cipher.PubKey) (ok bool) {
	return n.fs.hasFeed(feed)
}

func (n *Node) onRootReceived(c *Conn, r *registry.Root) (err error) {

	if orr := n.config.OnRootReceived; orr != nil {
		err = orr(c, r)
	}

	return
}

func (n *Node) onRootFilled(r *registry.Root) {

	if orf := n.config.OnRootFilled; orf != nil {
		orf(n, r)
	}

}

func (n *Node) onFillingBreaks(r *registry.Root, reason error) {

	if brk := n.config.OnFillingBreaks; brk != nil {
		brk(n, r, reason)
	}

}

// has connection to peer with given id (pk)
func (n *Node) hasPeer(id cipher.PubKey) (c *Conn, yep bool) { //nolint:unparam
	n.mx.Lock()
	defer n.mx.Unlock()

	c, yep = n.pkToConn[id]
	return
}

// evictStalePeer closes the existing Conn for `id` and waits up to
// the response timeout for run() to clean up (removeConn clears the
// pkToConn slot). Used by handshake when a peer's fresh SYN/Ack
// names the same NodeID as a Conn we already track — that record
// is, by construction, stale (the peer wouldn't be redialing if
// the old session were healthy on their side). The wait bounds
// the race window before the caller registers the new Conn via
// onConnInit; if cleanup overruns the timeout we proceed anyway
// and accept the pkToConn slot being overwritten by onConnInit.
func (n *Node) evictStalePeer(existing *Conn, id cipher.PubKey) {
	if existing == nil {
		return
	}
	n.Debugf(ConnHskPin, "[%s] evicting stale Conn for peer %s on rejoin",
		existing.String(), id.Hex())
	existing.Close() //nolint:errcheck,gosec
	rt := n.config.TCP.ResponseTimeout
	if existing.IsTCP() == false { //nolint:staticcheck
		rt = n.config.UDP.ResponseTimeout
	}
	if rt <= 0 {
		rt = 5 * time.Second
	}
	select {
	case <-existing.Done():
	case <-time.After(rt):
	}
}

// A Stat represents Node stat
type Stat struct {
	*skyobject.Stat
	Fillavg time.Duration
}

// Stat returns statistic of the Node
func (n *Node) Stat() (s *Stat) {

	// Stat returns basic connection summary; could include bandwidth, latency.

	s = new(Stat)
	s.Stat = n.c.Stat()
	s.Fillavg = n.fillavg.Value()

	return
}

// Close the Node. The Close returns error
// of (skyobject.Container).Close once.
func (n *Node) Close() (err error) {
	n.closeo.Do(func() {
		n.mx.Lock()
		defer n.mx.Unlock()

		// Shutdown peer exchange.
		for _, s := range n.ss {
			s.shutdown()
		}

		// Close all connections.
		for _, c := range n.pendConns {
			c.Close() //nolint:errcheck,gosec
		}
		for _, c := range n.pkToConn {
			c.Close() //nolint:errcheck,gosec
		}

		// Shutdown all transports.
		if n.tcp != nil {
			n.tcp.Close() //nolint:errcheck,gosec
		}
		if n.udp != nil {
			n.udp.Close() //nolint:errcheck,gosec
		}
		if n.dmsg != nil {
			n.dmsg.Close()
		}
		if n.rpc != nil {
			n.rpc.Close() //nolint:errcheck,gosec
		}

		// Close database.
		err = n.c.Close() //nolint:errcheck,gosec

		n.await.Wait()

	})

	return err
}

func (n *Node) JoinSwarm(
	feed cipher.PubKey, cfg SwarmConfig) (*Swarm, error) {

	n.mx.Lock()
	defer n.mx.Unlock()

	if s, ok := n.ss[feed]; ok {
		return s, nil
	}

	s := newSwarm(n, feed, cfg)

	go s.run()

	n.ss[feed] = s

	return s, nil
}

func (n *Node) LeaveSwarm(feed cipher.PubKey) error {
	n.mx.Lock()
	defer n.mx.Unlock()

	s, ok := n.ss[feed]
	if !ok {
		return errors.New("node is not in swarm")
	}

	s.shutdown()

	delete(n.ss, feed)

	return nil
}

func (n *Node) InSwarm(feed cipher.PubKey) (*Swarm, bool) {
	n.mx.Lock()
	defer n.mx.Unlock()

	s, ok := n.ss[feed]

	return s, ok
}
