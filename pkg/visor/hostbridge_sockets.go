// Package visor pkg/visor/hostbridge_sockets.go c3-vis-wasm
package visor

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/hostbridge"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/vpn"
)

// hostBridgeSockets gives the transport carriers sockets on the host bridge's
// host (network.Sockets). Its listeners and packet conns outlive a session:
// each reconnect rebinds them at the same host address, so a carrier and its
// address resolver entry never see the bridge go away.
type hostBridgeSockets struct {
	mu     sync.Mutex
	client *hostbridge.Client
	lis    map[*durableListener]struct{}
	pcs    map[*durablePacketConn]struct{}
	// changed is closed and replaced after each attach, so readers waiting
	// for a new session wake up.
	changed chan struct{}
	log     *logging.Logger
}

func (s *hostBridgeSockets) init() {
	if s.changed == nil {
		s.changed = make(chan struct{})
		s.lis = map[*durableListener]struct{}{}
		s.pcs = map[*durablePacketConn]struct{}{}
	}
}

// attach rebinds every socket on c, or marks them down when c is nil.
func (s *hostBridgeSockets) attach(c *hostbridge.Client) {
	s.mu.Lock()
	s.init()
	s.client = c
	lis := make([]*durableListener, 0, len(s.lis))
	for l := range s.lis {
		lis = append(lis, l)
	}
	pcs := make([]*durablePacketConn, 0, len(s.pcs))
	for p := range s.pcs {
		pcs = append(pcs, p)
	}
	s.mu.Unlock()
	if c != nil {
		for _, l := range lis {
			_ = l.bind(c) //nolint:errcheck // keepBound retries and logs
		}
		for _, p := range pcs {
			if err := p.rebind(c); err != nil && s.log != nil {
				s.log.WithError(err).WithField("addr", p.addr).Warn("Could not bind UDP again on the bridge's host")
			}
		}
	}
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *hostBridgeSockets) current() (*hostbridge.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, errHostBridgeNotConnected
	}
	return s.client, nil
}

// next returns a channel closed after the next attach.
func (s *hostBridgeSockets) next() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	return s.changed
}

// Listen implements network.Sockets. It needs a session, so a carrier learns
// its port at once.
func (s *hostBridgeSockets) Listen(network, addr string) (net.Listener, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	l := s.newListener(network, addr)
	if err := l.bind(c); err != nil {
		return nil, err
	}
	s.register(l)
	return l, nil
}

// listenDeferred listens on the bridge's host whenever a session is up, so
// an app can bind a host address before the bridge connects. Its Addr is the
// one asked for until a bind reports the port.
func (s *hostBridgeSockets) listenDeferred(network, addr string) (net.Listener, error) {
	asked, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	l := s.newListener(network, addr)
	l.bound = asked
	s.register(l)
	if c, err := s.current(); err == nil {
		_ = l.bind(c) //nolint:errcheck // keepBound retries
	}
	return l, nil
}

func (s *hostBridgeSockets) newListener(network, addr string) *durableListener {
	return &durableListener{s: s, network: network, addr: addr, conns: make(chan net.Conn), done: make(chan struct{})}
}

// register adds l to the listeners each attach rebinds, and keeps it bound.
func (s *hostBridgeSockets) register(l *durableListener) {
	s.mu.Lock()
	s.init()
	s.lis[l] = struct{}{}
	s.mu.Unlock()
	go l.keepBound()
}

// ListenPacket implements network.Sockets.
func (s *hostBridgeSockets) ListenPacket(network, addr string) (net.PacketConn, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	p := &durablePacketConn{s: s, network: network, addr: addr, done: make(chan struct{})}
	if err := p.rebind(c); err != nil {
		return nil, err
	}
	p.addr = rebindAddr(addr, p.bound)
	s.mu.Lock()
	s.pcs[p] = struct{}{}
	s.mu.Unlock()
	return p, nil
}

// DialContext implements network.Sockets.
func (s *hostBridgeSockets) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	return c.DialContext(ctx, network, addr)
}

// durableListener accepts from a listener on the bridge's host, replaced by
// a new one at the same address on each reconnect.
type durableListener struct {
	s       *hostBridgeSockets
	network string
	conns   chan net.Conn
	once    sync.Once
	done    chan struct{}

	// bindMu serializes binds, which wait on the bridge, so mu stays short.
	bindMu sync.Mutex
	mu     sync.Mutex
	addr   string
	bound  net.Addr
	pinned bool
	inner  net.Listener
	client *hostbridge.Client
}

// bind listens on c unless l already does. The first bind pins the port, so
// later sessions rebind the same one at the host asked for.
func (l *durableListener) bind(c *hostbridge.Client) error {
	l.bindMu.Lock()
	defer l.bindMu.Unlock()
	l.mu.Lock()
	addr, same := l.addr, l.inner != nil && l.client == c
	l.mu.Unlock()
	if same {
		return nil
	}
	inner, err := c.Listen(l.network, addr)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	select {
	case <-l.done:
		_ = inner.Close() //nolint:errcheck
		return net.ErrClosed
	default:
	}
	if l.inner != nil {
		_ = l.inner.Close() //nolint:errcheck
	}
	l.inner, l.client = inner, c
	if !l.pinned {
		l.pinned = true
		l.bound = inner.Addr()
		l.addr = rebindAddr(l.addr, l.bound)
	}
	go l.pump(inner)
	return nil
}

func (l *durableListener) pump(inner net.Listener) {
	defer func() {
		_ = inner.Close() //nolint:errcheck
		l.mu.Lock()
		if l.inner == inner {
			l.inner = nil
		}
		l.mu.Unlock()
	}()
	for {
		conn, err := inner.Accept()
		if err != nil {
			return
		}
		select {
		case l.conns <- conn:
		case <-l.done:
			_ = conn.Close() //nolint:errcheck
			return
		}
	}
}

// keepBound binds l again whenever a session is up and l is not listening
// on it, as when its port was still held at the last attempt. It logs only
// when that changes.
func (l *durableListener) keepBound() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var failing error
	for {
		select {
		case <-l.done:
			return
		case <-t.C:
		}
		c, err := l.s.current()
		if err != nil {
			continue
		}
		err = l.bind(c)
		if log := l.s.log; log != nil {
			switch {
			case err != nil && failing == nil:
				log.WithError(err).WithField("addr", l.Addr()).Warn("Could not listen on the bridge's host, retrying")
			case err == nil && failing != nil:
				log.WithField("addr", l.Addr()).Info("Listening on the bridge's host again")
			}
		}
		failing = err
	}
}

func (l *durableListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *durableListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.mu.Lock()
		if l.inner != nil {
			_ = l.inner.Close() //nolint:errcheck
		}
		l.mu.Unlock()
		l.s.mu.Lock()
		delete(l.s.lis, l)
		l.s.mu.Unlock()
	})
	return nil
}

func (l *durableListener) Addr() net.Addr {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bound
}

// durablePacketConn is a UDP socket on the bridge's host, bound again at the
// same address on each reconnect. Datagrams sent while the bridge is away are
// dropped, as a lossy network would.
type durablePacketConn struct {
	s       *hostBridgeSockets
	network string
	addr    string
	bound   net.Addr

	mu            sync.Mutex
	inner         net.PacketConn
	readDeadline  time.Time
	writeDeadline time.Time
	once          sync.Once
	done          chan struct{}
}

func (p *durablePacketConn) rebind(c *hostbridge.Client) error {
	inner, err := c.ListenPacket(p.network, p.addr)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		_ = inner.Close() //nolint:errcheck
		return net.ErrClosed
	default:
	}
	if p.bound == nil {
		p.bound = inner.LocalAddr()
	}
	if p.inner != nil {
		_ = p.inner.Close() //nolint:errcheck
	}
	_ = inner.SetReadDeadline(p.readDeadline)   //nolint:errcheck
	_ = inner.SetWriteDeadline(p.writeDeadline) //nolint:errcheck
	p.inner = inner
	return nil
}

func (p *durablePacketConn) cur() (net.PacketConn, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inner, p.readDeadline
}

func (p *durablePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		// Take next before reading, so an attach during the read is not missed.
		next := p.s.next()
		inner, deadline := p.cur()
		if inner != nil {
			n, addr, err := inner.ReadFrom(b)
			if err == nil || isTimeout(err) {
				return n, addr, err
			}
			// The session ended. Wait for the next one unless closed.
			p.mu.Lock()
			if p.inner == inner {
				p.inner = nil
			}
			p.mu.Unlock()
		}
		var timeout <-chan time.Time
		var t *time.Timer
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}
			t = time.NewTimer(d)
			timeout = t.C
		}
		select {
		case <-p.done:
			stopTimer(t)
			return 0, nil, net.ErrClosed
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case <-next:
			stopTimer(t)
		}
	}
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

func (p *durablePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	p.mu.Lock()
	inner := p.inner
	p.mu.Unlock()
	select {
	case <-p.done:
		return 0, net.ErrClosed
	default:
	}
	if inner == nil {
		return len(b), nil
	}
	n, err := inner.WriteTo(b, addr)
	if err != nil && !isTimeout(err) {
		return len(b), nil
	}
	return n, err
}

func (p *durablePacketConn) Close() error {
	p.once.Do(func() {
		close(p.done)
		p.mu.Lock()
		if p.inner != nil {
			_ = p.inner.Close() //nolint:errcheck
			p.inner = nil
		}
		p.mu.Unlock()
		p.s.mu.Lock()
		delete(p.s.pcs, p)
		p.s.mu.Unlock()
	})
	return nil
}

func (p *durablePacketConn) LocalAddr() net.Addr { return p.bound }

func (p *durablePacketConn) SetDeadline(t time.Time) error {
	_ = p.SetReadDeadline(t) //nolint:errcheck
	return p.SetWriteDeadline(t)
}

func (p *durablePacketConn) SetReadDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readDeadline = t
	if p.inner != nil {
		return p.inner.SetReadDeadline(t)
	}
	return nil
}

func (p *durablePacketConn) SetWriteDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeDeadline = t
	if p.inner != nil {
		return p.inner.SetWriteDeadline(t)
	}
	return nil
}

// rebindAddr keeps the host of the address asked for, usually empty for
// every interface, with the port the first bind got. The bridge reports a
// wildcard bind as [::], which a bridge without IPv6 sockets cannot bind.
func rebindAddr(asked string, bound net.Addr) string {
	host, _, err := net.SplitHostPort(asked)
	if err != nil {
		host = ""
	}
	_, port, err := net.SplitHostPort(bound.String())
	if err != nil {
		return bound.String()
	}
	return net.JoinHostPort(host, port)
}

// hostBridgeHost is the "host:" namespace of bottle vnet in a browser visor:
// an app given a host address listens or dials on the bridge's host.
type hostBridgeHost struct{ s *hostBridgeSockets }

// Listen binds before the bridge connects too, so an app's host address
// works whenever a bridge is up.
func (h hostBridgeHost) Listen(network, addr string) (net.Listener, error) {
	return h.s.listenDeferred(network, addr)
}

func (h hostBridgeHost) DialTimeout(network, addr string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return h.s.DialContext(ctx, network, addr)
}

// hostBridgeVPN lends a browser visor's vpn-client a TUN and the routing
// table of the bridge's host (vpn.HostNetwork).
type hostBridgeVPN struct{ s *hostBridgeSockets }

func (h hostBridgeVPN) TUN(ctx context.Context) (vpn.TUNDevice, error) {
	c, err := h.s.current()
	if err != nil {
		return nil, err
	}
	t, err := c.TUN(ctx)
	if err != nil {
		return nil, err
	}
	return &hostTUN{TUN: t}, nil
}

func (h hostBridgeVPN) Net(ctx context.Context, cmd string, args ...string) (string, error) {
	c, err := h.s.current()
	if err != nil {
		return "", err
	}
	return c.Net(ctx, cmd, args...)
}

// hostTUN is dead once its session ends, so the client opens another.
type hostTUN struct {
	*hostbridge.TUN
	dead atomic.Bool
}

func (t *hostTUN) Read(p []byte) (int, error) {
	n, err := t.TUN.Read(p)
	if err != nil {
		t.dead.Store(true)
	}
	return n, err
}

func (t *hostTUN) Write(p []byte) (int, error) {
	n, err := t.TUN.Write(p)
	if err != nil {
		t.dead.Store(true)
	}
	return n, err
}

func (t *hostTUN) Dead() bool { return t.dead.Load() }
