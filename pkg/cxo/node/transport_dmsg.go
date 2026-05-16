// Package node provides CXO node transport adapters.
// transport_dmsg.go implements the DMSG transport for CXO nodes,
// allowing CXO peer connections over skywire's DMSG network.
package node

import (
	"errors"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node/transport"
)

// DMSG represents the DMSG transport of a CXO Node.
// It manages CXO connections over DMSG, using public keys
// as addresses instead of IP:port.
type DMSG struct {
	*transport.DMSGFactory

	n *Node

	mx sync.Mutex

	isListening bool
	cs          map[cipher.PubKey]*Conn // remote PK -> connection
}

func newDMSG(n *Node, factory *transport.DMSGFactory) *DMSG {
	d := &DMSG{
		DMSGFactory: factory,
		n:           n,
		cs:          make(map[cipher.PubKey]*Conn),
	}
	d.AcceptedCallback = d.acceptConn
	// Cap concurrent in-flight accepts at the Node's pending-connection
	// limit so the dmsg listener applies backpressure when handshake
	// processing falls behind n.mx, instead of fanning out unbounded
	// goroutines that pile up on the same mutex (and OOM the process
	// via per-conn buffers + read/write loops).
	d.MaxPendingAccepts = n.config.MaxPendingConnections
	return d
}

func (d *DMSG) addConn(pk cipher.PubKey, c *Conn) {
	d.mx.Lock()
	defer d.mx.Unlock()
	d.cs[pk] = c
}

func (d *DMSG) getConn(pk cipher.PubKey) *Conn {
	d.mx.Lock()
	defer d.mx.Unlock()
	return d.cs[pk]
}

// Listen starts accepting CXO connections over DMSG.
func (d *DMSG) Listen() error {
	d.mx.Lock()
	defer d.mx.Unlock()

	if d.isListening {
		return ErrAlreadyListen
	}

	if err := d.DMSGFactory.Listen(); err != nil {
		return err
	}

	d.isListening = true
	return nil
}

// ConnectPK connects to a remote CXO node over DMSG by public key.
// If a healthy connection already exists, returns the existing one.
//
// Cached-Conn liveness gate: a cached Conn whose closeq is signaled
// (run loop has observed the underlying transport going away, but
// removeConn hasn't yet completed) OR whose underlying transport
// reports IsClosed() is evicted here and re-dialed. Without this
// gate, the symmetric peer-side bug fixed in #2625's
// acceptHandshake also manifested on the dial side: a half-dead
// Conn whose run loop is stuck in receiveMsg's select on a transport
// that will never return — closeq not closed yet — would be returned
// from this cache. Subsequent Subscribe calls on the dead Conn hang,
// reconnect timeouts pile up, and the visor's peerSubs go silent
// indefinitely.
//
// Concretely observed in production (2026-05-16): after an Alpha
// visor restart, all 3 peerSubs to remote group members appeared
// healthy for ~35 seconds, then went silent. The per-peer reconnect
// loop from #2606 fired every 30s but every attempt timed out at 15s
// with "context deadline exceeded" — because every ConnectPK
// returned the same dead cached Conn, Subscribe hung on it, the
// outer 15s deadline fired. Forty-five minutes of silent stale
// peerSubs until manual halt+restart cleared the cache. This fix
// makes ConnectPK self-heal on the next reconnect tick.
//
// The closeq check catches the explicit-close case; the IsClosed()
// check catches the case where the underlying transport's close
// was observed but run loop hasn't yet propagated. Neither catches
// the truly-half-dead-but-not-yet-erroring case (e.g. a wedged
// network with no FIN/RST) — that path needs a separate liveness
// pulse, deferred. For typical "peer's dmsg server flipped /
// transport reset cleanly" failures this is sufficient.
func (d *DMSG) ConnectPK(remotePK cipher.PubKey) (*Conn, error) {
	d.n.Debugf(NewOutConnPin, "[dmsg:%s] connecting", remotePK.String())

	// Check if connection already exists AND is alive.
	if c := d.getConn(remotePK); c != nil {
		if connIsAlive(c) {
			return c, nil
		}
		// Stale cached conn: evict before re-dialing so the new Conn
		// can replace it cleanly. closeConn deletes from d.cs and
		// closes the underlying transport; safe to call on an already-
		// closing Conn.
		d.n.Debugf(NewOutConnPin, "[dmsg:%s] cached conn is stale; evicting before re-dial", remotePK.String())
		d.closeConn(c)
	}

	// Dial over DMSG
	fc, err := d.DMSGFactory.Connect(remotePK)
	if err != nil {
		return nil, err
	}

	// Init connection (handshake, etc.)
	c, err := d.n.initConn(fc, false)
	if err != nil {
		d.n.Errorf(err, "[dmsg:%s] failed to connect", remotePK.String())
		if !fc.IsClosed() {
			fc.Close() //nolint:errcheck,gosec
		}
		return nil, err
	}

	d.addConn(remotePK, c)
	return c, nil
}

func (d *DMSG) acceptConn(fc *transport.Connection) {
	d.n.Debugf(NewInConnPin, "[dmsg] accepting connection from %s", fc.GetRemoteAddr())

	c, err := d.n.initConn(fc, true)
	if err != nil {
		d.n.Errorf(err, "[dmsg] failed to accept from %s", fc.GetRemoteAddr())
		if !fc.IsClosed() {
			fc.Close() //nolint:errcheck,gosec
		}
		return
	}

	// Extract remote PK from connection.
	// c.PeerID() returns skycoin/src/cipher.PubKey; convert to skywire cipher.PubKey
	peerID := c.PeerID()
	var pk cipher.PubKey
	copy(pk[:], peerID[:])
	if pk != (cipher.PubKey{}) {
		d.addConn(pk, c)
	}
}

// closeConn closes the underlying transport.Connection and removes
// the cached entry that matches the given conn. Called from
// Conn.run() cleanup so a dead DMSG conn does not persist in d.cs
// (which would short-circuit subsequent ConnectPK calls) and so the
// read/write loops attached to the transport.Connection actually
// exit (closing c.Connection signals their `<-c.done` selects).
//
// Without the cache delete, when the *remote* CXO node restarts
// (and its old per-conn state goes away) the local side's dmsg
// session survives — but the cxo Conn on top is dead. ConnectPK's
// existing-conn cache hit then returns the dead conn, the
// publisher's AnnounceTo never re-handshakes, and from the remote
// aggregator's point of view that publisher is silent forever
// (until the publisher itself restarts).
//
// Without the Close, every dropped peer leaks the transport-level
// readLoop and writeLoop goroutines. Observed in production after
// ~7h uptime: 36,609 Connection.writeLoop goroutines parked in the
// `<-c.done` select forever (one per peer ever connected),
// dragging GC scanstack to 25% of CPU. TCP.closeConn and
// UDP.closeConn already do this; DMSG was missing it.
//
// Iterating to find c is fine: d.cs is bounded by peer count, and
// this fires only on conn teardown — not in the hot path.
func (d *DMSG) closeConn(c *Conn) {
	d.mx.Lock()
	defer d.mx.Unlock()
	for pk, cached := range d.cs {
		if cached == c {
			delete(d.cs, pk)
			break
		}
	}
	if !c.Connection.IsClosed() {
		c.Connection.Close()
	}
}

// connIsAlive returns false if c looks dead — either the run loop's
// closeq channel is closed (an error was observed but removeConn
// hasn't completed) or the underlying transport.Connection reports
// IsClosed(). Returns true otherwise. Used by ConnectPK to decide
// whether to return a cached Conn or evict and re-dial.
//
// Limitations: a truly half-dead conn (transport reads block forever
// without erroring — e.g. wedged network, dropped FIN/RST) escapes
// this check. For that case the caller still needs an outer deadline.
// What this catches: explicit close-in-progress (most common
// half-dead source — peer's dmsg session reset cleanly, but our
// removeConn is racing with the next reconnect tick).
func connIsAlive(c *Conn) bool {
	if c == nil {
		return false
	}
	select {
	case <-c.closeq:
		return false
	default:
	}
	if c.Connection != nil && c.Connection.IsClosed() {
		return false
	}
	return true
}

// CloseConn closes a connection by remote public key.
func (d *DMSG) CloseConn(pk cipher.PubKey) error {
	d.mx.Lock()
	defer d.mx.Unlock()

	c, ok := d.cs[pk]
	if !ok {
		return errors.New("connection not found")
	}

	if !c.Connection.IsClosed() {
		c.Connection.Close() //nolint:errcheck,gosec
	}

	delete(d.cs, pk)
	return nil
}

// Connections returns a list of connected remote public keys.
func (d *DMSG) Connections() []cipher.PubKey {
	d.mx.Lock()
	defer d.mx.Unlock()

	pks := make([]cipher.PubKey, 0, len(d.cs))
	for pk := range d.cs {
		pks = append(pks, pk)
	}
	return pks
}
