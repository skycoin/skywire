// Package hostbridge pkg/hostbridge/client.go c3-vis-wasm
package hostbridge

import (
	"bufio"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/0magnet/yamux"
)

// Client is the tab end of the bridge. Its Dial, Listen and ListenPacket
// return the standard net types, backed by sockets on the bridge's host.
type Client struct {
	sess *yamux.Session
	mu   sync.Mutex
	lis  map[string]*listener
}

// NewClient runs the tab end over conn, the tab's WebSocket to the bridge.
func NewClient(conn net.Conn) (*Client, error) {
	sess, err := yamux.Client(conn, muxConfig())
	if err != nil {
		return nil, err
	}
	c := &Client{sess: sess, lis: map[string]*listener{}}
	go c.acceptLoop()
	return c, nil
}

// Done is closed when the session to the bridge ends.
func (c *Client) Done() <-chan struct{} { return c.sess.CloseChan() }

// Close ends the session and every socket opened through it.
func (c *Client) Close() error { return c.sess.Close() }

func (c *Client) request(op byte, network, addr string) (net.Conn, *bufio.Reader, string, string, error) {
	if !validNetwork(op, network) {
		return nil, nil, "", "", errors.New("hostbridge: unsupported network " + network)
	}
	st, err := c.sess.Open()
	if err != nil {
		return nil, nil, "", "", err
	}
	if err := writeMsg(st, op, network, addr); err != nil {
		_ = st.Close() //nolint:errcheck
		return nil, nil, "", "", err
	}
	br := bufio.NewReader(st)
	a, b, err := readReply(br)
	if err != nil {
		_ = st.Close() //nolint:errcheck
		return nil, nil, "", "", err
	}
	return st, br, a, b, nil
}

// Dial opens a TCP connection from the bridge's host.
func (c *Client) Dial(network, addr string) (net.Conn, error) {
	st, br, local, remote, err := c.request(opDial, network, addr)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: st, br: br, local: tcpAddr(local), remote: tcpAddr(remote)}, nil
}

// Listen listens on TCP on the bridge's host. Closing the listener closes
// the host socket.
func (c *Client) Listen(network, addr string) (net.Listener, error) {
	st, _, bound, id, err := c.request(opListen, network, addr)
	if err != nil {
		return nil, err
	}
	l := &listener{c: c, ctl: st, id: id, addr: tcpAddr(bound), ch: make(chan net.Conn, 16), done: make(chan struct{})}
	c.mu.Lock()
	c.lis[id] = l
	c.mu.Unlock()
	go func() {
		select {
		case <-c.sess.CloseChan():
			_ = l.Close() //nolint:errcheck
		case <-l.done:
		}
	}()
	return l, nil
}

// ListenPacket binds UDP on the bridge's host.
func (c *Client) ListenPacket(network, addr string) (net.PacketConn, error) {
	st, br, bound, _, err := c.request(opUDP, network, addr)
	if err != nil {
		return nil, err
	}
	ap, err := netip.ParseAddrPort(bound)
	if err != nil {
		_ = st.Close() //nolint:errcheck
		return nil, err
	}
	return &packetConn{st: st, br: br, local: net.UDPAddrFromAddrPort(ap)}, nil
}

func (c *Client) acceptLoop() {
	for {
		st, err := c.sess.Accept()
		if err != nil {
			return
		}
		go c.route(st)
	}
}

func (c *Client) route(st net.Conn) {
	br := bufio.NewReader(st)
	op, f, err := readMsg(br, 2)
	if err != nil || op != opAccepted {
		_ = st.Close() //nolint:errcheck
		return
	}
	c.mu.Lock()
	l := c.lis[f[0]]
	c.mu.Unlock()
	if l == nil {
		_ = st.Close() //nolint:errcheck
		return
	}
	cn := &conn{Conn: st, br: br, local: l.addr, remote: tcpAddr(f[1])}
	select {
	case l.ch <- cn:
	case <-l.done:
		_ = st.Close() //nolint:errcheck
	}
}

func tcpAddr(s string) net.Addr {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return &net.TCPAddr{}
	}
	return net.TCPAddrFromAddrPort(ap)
}

// conn reports the host's addresses, so code that checks a peer's IP sees
// the real one.
type conn struct {
	net.Conn
	br            *bufio.Reader
	local, remote net.Addr
}

func (c *conn) Read(p []byte) (int, error) { return c.br.Read(p) }
func (c *conn) LocalAddr() net.Addr        { return c.local }
func (c *conn) RemoteAddr() net.Addr       { return c.remote }

type listener struct {
	c    *Client
	ctl  net.Conn
	id   string
	addr net.Addr
	ch   chan net.Conn
	once sync.Once
	done chan struct{}
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case cn := <-l.ch:
		return cn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.c.mu.Lock()
		delete(l.c.lis, l.id)
		l.c.mu.Unlock()
		_ = l.ctl.Close() //nolint:errcheck
	})
	return nil
}

func (l *listener) Addr() net.Addr { return l.addr }

type packetConn struct {
	st    net.Conn
	br    *bufio.Reader
	rmu   sync.Mutex
	wmu   sync.Mutex
	local net.Addr
}

func (p *packetConn) ReadFrom(b []byte) (int, net.Addr, error) {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	n, ap, err := readDatagram(p.br, b)
	if err != nil {
		return 0, nil, err
	}
	return n, net.UDPAddrFromAddrPort(ap), nil
}

func (p *packetConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if len(b) > maxDatagram {
		return 0, errFieldTooLong
	}
	ap, ok := udpAddrPort(addr)
	if !ok {
		return 0, errors.New("hostbridge: not a UDP address: " + addr.String())
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if err := writeDatagram(p.st, ap, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (p *packetConn) Close() error                       { return p.st.Close() }
func (p *packetConn) LocalAddr() net.Addr                { return p.local }
func (p *packetConn) SetDeadline(t time.Time) error      { return p.st.SetDeadline(t) }
func (p *packetConn) SetReadDeadline(t time.Time) error  { return p.st.SetReadDeadline(t) }
func (p *packetConn) SetWriteDeadline(t time.Time) error { return p.st.SetWriteDeadline(t) }
