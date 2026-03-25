// Package node provides CXO node transport adapters.
// transport_dmsg.go implements the DMSG transport for CXO nodes,
// allowing CXO peer connections over skywire's DMSG network.
package node

import (
	"errors"
	"sync"

	"github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
// If a connection already exists, returns the existing one.
func (d *DMSG) ConnectPK(remotePK cipher.PubKey) (*Conn, error) {
	d.n.Debugf(NewOutConnPin, "[dmsg:%s] connecting", remotePK.String()[:8])

	// Check if connection already exists
	if c := d.getConn(remotePK); c != nil {
		return c, nil
	}

	// Dial over DMSG
	fc, err := d.DMSGFactory.Connect(remotePK)
	if err != nil {
		return nil, err
	}

	// Init connection (handshake, etc.)
	c, err := d.n.initConn(fc, false)
	if err != nil {
		d.n.Errorf(err, "[dmsg:%s] failed to connect", remotePK.String()[:8])
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
