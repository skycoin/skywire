package node

import (
	"errors"
	"sync"

	"github.com/skycoin/skywire/pkg/cxo/node/transport"
)

// A TCP represents TCP transport
// of the Node. The TCP used to
// listen and connect
type TCP struct {
	*transport.TCPFactory // underlying transport

	// back reference
	n *Node

	//
	mx sync.Mutex

	address     string // listening address
	isListening bool

	cs map[string]*Conn // address -> conn
}

func newTCP(n *Node) (t *TCP) {

	t = new(TCP)

	t.TCPFactory = transport.NewTCPFactory()

	t.n = n
	t.AcceptedCallback = t.acceptConn
	t.cs = make(map[string]*Conn)

	return
}
func (t *TCP) addConn(c *Conn) {
	t.mx.Lock()
	defer t.mx.Unlock()

	t.cs[c.Address()] = c
}

func (t *TCP) getConn(address string) (c *Conn) {
	t.mx.Lock()
	defer t.mx.Unlock()

	return t.cs[address]
}

// Listen on given address. It's possible to listen
// only once
func (t *TCP) Listen(address string) (err error) {

	t.mx.Lock()
	defer t.mx.Unlock()

	if t.isListening == true { //nolint:staticcheck
		return ErrAlreadyListen
	}

	if err = t.TCPFactory.Listen(address); err != nil {
		return
	}

	t.isListening = true
	// Use actual listener address (important when port is 0)
	t.address = t.TCPFactory.ListenerAddress()
	return
}

// Address returns listening address as it
// passed to the Listen method. The address
// is blank string if the TCP is not listening
func (t *TCP) Address() string {
	t.mx.Lock()
	defer t.mx.Unlock()

	return t.address
}

// Connect to given TCP address. The method blocks. If connection
// with given address already exists, then the Connect returns this
// existing connection.
func (t *TCP) Connect(address string) (*Conn, error) {
	t.n.Debugf(NewOutConnPin, "[%s] connecting",
		connString(false, true, address))

	// Check if connections to/from address already exists.
	c := t.getConn(address)
	if c != nil {
		return c, nil
	}

	// Open new transport.Connection.
	fc, err := t.TCPFactory.Connect(address)
	if err != nil {
		return nil, err
	}

	// Init outgoing connection (handshake, check duplicate pubkey, etc.)
	if c, err = t.n.initConn(fc, false); err == nil {
		t.addConn(c)
	} else {
		t.n.Errorf(err, "[%s] failed to connect", factoryConnStr(fc, false))
		if !fc.IsClosed() {
			t.n.Debugf(CloseConnPin, "[%s] closing connection",
				factoryConnStr(fc, false))
			fc.Close() //nolint:errcheck,gosec
		}
	}

	return c, err
}

func (t *TCP) acceptConn(fc *transport.Connection) {
	t.n.Debugf(NewInConnPin, "[%s] accepting",
		factoryConnStr(fc, true))

	// Check if connections to/from address already exists.
	var (
		addr = fc.GetRemoteAddr().String()
		err  error
	)
	c := t.getConn(addr)
	if c != nil {
		err = errors.New("already have incoming connection from the address")
	} else {
		// Init incoming connection (handshake, check duplicate pubkey, etc.)
		c, err = t.n.initConn(fc, true)
	}

	if err == nil {
		t.addConn(c)
	} else {
		t.n.Errorf(err, "[%s] failed to accept", factoryConnStr(fc, true))
		if !fc.IsClosed() {
			t.n.Debugf(CloseConnPin, "[%s] closing connection",
				factoryConnStr(fc, true))
			fc.Close() //nolint:errcheck,gosec
		}
	}
}

// closeConn closes the connection and removes it from cache
func (t *TCP) closeConn(addr string) error { //nolint:errcheck,gosec
	t.mx.Lock()
	defer t.mx.Unlock()

	if c, ok := t.cs[addr]; ok {
		if !c.Connection.IsClosed() {
			t.n.Debugf(CloseConnPin, "[%s] closing connection", c.String())
			c.Connection.Close() //nolint:errcheck,gosec
		}
	} else {
		return errors.New("not found")
	}

	delete(t.cs, addr)

	return nil
}

// connections strings
func (t *TCP) connections() (cs []string) { //nolint:unused
	t.mx.Lock()
	defer t.mx.Unlock()

	cs = make([]string, 0, len(t.cs))

	for _, c := range t.cs {
		cs = append(cs, c.String())
	}

	return
}

// A UDP represents UDP transport
// of the Node. The UDP used to
// listen and connect
type UDP struct {
	*transport.UDPFactory // underlying transport

	// back reference
	n *Node

	mx sync.Mutex

	address     string
	isListening bool

	cs map[string]*Conn // connections
}

func newUDP(n *Node) (u *UDP) {

	u = new(UDP)

	u.UDPFactory = transport.NewUDPFactory()

	u.n = n
	u.AcceptedCallback = u.acceptConn
	u.cs = make(map[string]*Conn)

	return
}

func (u *UDP) addConn(c *Conn) {
	u.mx.Lock()
	defer u.mx.Unlock()

	u.cs[c.Address()] = c
}

func (u *UDP) delConn(c *Conn) { //nolint:unused
	u.mx.Lock()
	defer u.mx.Unlock()

	delete(u.cs, c.Address())
}

func (u *UDP) getConn(address string) (c *Conn) {
	u.mx.Lock()
	defer u.mx.Unlock()

	return u.cs[address]
}

// Listen on given address. It's possible to listen
// only once
func (u *UDP) Listen(address string) (err error) {
	u.mx.Lock()
	defer u.mx.Unlock()

	if u.isListening == true { //nolint:staticcheck
		return ErrAlreadyListen
	}

	if err = u.UDPFactory.Listen(address); err != nil {
		return
	}

	u.address = address
	u.isListening = true
	return
}

// Address returns listening address as it
// passed to the Listen method. The address
// is blank string if the UDP is not listening
func (u *UDP) Address() string {
	u.mx.Lock()
	defer u.mx.Unlock()

	return u.address
}

// Connect to given UDP address. If connection with given
// address already exists, then the Connect returns this
// existing connection.
func (u *UDP) Connect(address string) (*Conn, error) {
	u.n.Debugf(NewOutConnPin, "[%s] connecting",
		connString(false, false, address))

	// Check if connections to/from address already exists.
	c := u.getConn(address)
	if c != nil {
		return c, nil
	}

	// Open new transport.Connection.
	fc, err := u.UDPFactory.Connect(address)
	if err != nil {
		return nil, err
	}

	// Init outgoing connection (handshake, check duplicate pubkey, etc.)
	if c, err = u.n.initConn(fc, false); err == nil {
		u.addConn(c)
	} else {
		u.n.Errorf(err, "[%s] failed to connect", factoryConnStr(fc, false))
		if !fc.IsClosed() {
			u.n.Debugf(CloseConnPin, "[%s] closing connection",
				factoryConnStr(fc, false))
			fc.Close() //nolint:errcheck,gosec
		}
	}

	return c, err
}

func (u *UDP) acceptConn(fc *transport.Connection) {
	u.n.Debugf(NewInConnPin, "[%s] accepting",
		factoryConnStr(fc, true))

	// Check if connections to/from address already exists.
	var (
		addr = fc.GetRemoteAddr().String()
		err  error
	)
	c := u.getConn(addr)
	if c != nil {
		err = errors.New("already have incoming connection from the address")
	} else {
		// Init incoming connection (handshake, check duplicate pubkey, etc.)
		c, err = u.n.initConn(fc, true)
	}

	if err == nil {
		u.addConn(c)
	} else {
		u.n.Errorf(err, "[%s] failed to accept", factoryConnStr(fc, true))
		if !fc.IsClosed() {
			u.n.Debugf(CloseConnPin, "[%s] closing connection",
				factoryConnStr(fc, true))
			fc.Close() //nolint:errcheck,gosec
		}
	}
}

// closeConn closes the connection and removes it from cache
func (u *UDP) closeConn(addr string) error { //nolint:errcheck,gosec
	u.mx.Lock()
	defer u.mx.Unlock()

	if c, ok := u.cs[addr]; ok {
		if !c.Connection.IsClosed() {
			u.n.Debugf(CloseConnPin, "[%s] closing connection", c.String())
			c.Connection.Close() //nolint:errcheck,gosec
		}
	} else {
		return errors.New("not found")
	}

	delete(u.cs, addr)

	return nil
}

// connections strings
func (u *UDP) connections() (cs []string) { //nolint:unused
	u.mx.Lock()
	defer u.mx.Unlock()

	cs = make([]string, 0, len(u.cs))

	for _, c := range u.cs {
		cs = append(cs, c.String())
	}

	return
}

func factoryConnStr(fc *transport.Connection, isIncoming bool) string {
	return connString(isIncoming, fc.IsTCP(), fc.GetRemoteAddr().String())
}
