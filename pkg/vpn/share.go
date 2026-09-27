// Package vpn pkg/vpn/share.go c4-app-vpn
//
// Sharing the tunnel with other devices — the phone's VPN hotspot.
//
// On a phone the tunnel is the TUN the system hands out, and the kernel routes
// into it only what apps ON the phone send. A device on the phone's hotspot
// never reaches it: tethered traffic is forwarded below VpnService, straight
// to the uplink, and no unprivileged app can change that. The one process that
// could proxy for such a device — this one — is excluded from its own tunnel,
// because its dmsg traffic is what carries the tunnel.
//
// So shared traffic enters the tunnel beside the kernel instead of through it:
// a userspace netstack (netstack_tun.go) that carries the SAME address the
// server assigned to this client. Its packets reach the server over the same
// route-group conn as the kernel's, and the server cannot tell them apart —
// which is what keeps its per-client rules applying to them (the secure mode's
// local-network block is keyed on that one address). Replies are told apart by
// destination port: the netstack takes its ephemeral ports from
// [shareFirstPort, shareLastPort], above the kernel's own range
// (32768–60999), so a reply to one of those ports is the netstack's and every
// other packet is the kernel's.
//
// The proxy in front of it speaks SOCKS5 and HTTP on one connection
// (pkg/proxyfront), resolves names through the tunnel too, and dials nothing
// but the netstack: with no tunnel up a shared connection fails, it never
// leaves by the phone's own uplink.
package vpn

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/armon/go-socks5"

	"github.com/skycoin/skywire/pkg/proxyfront"
)

const (
	// shareFirstPort is the bottom of the netstack's ephemeral range. Linux,
	// and so Android, hands out 32768–60999 unless told otherwise; the
	// netstack takes what lies above it.
	shareFirstPort = 61000
	shareLastPort  = 65535

	// shareDefaultDNS resolves shared names when vpn-client has no --dns: the
	// resolver the phone's own interface falls back to as well.
	shareDefaultDNS = "1.1.1.1"
)

var (
	errShareNoTunnel = errors.New("vpn: the tunnel is not up; shared connections wait for it")
	errShareLocal    = errors.New("vpn: shared connections cannot reach this device's own addresses")
)

// shareSession is one tunnel session as sharing sees it: the address the
// server assigned and the way to the server. The netstack itself is built by
// the first shared connection that needs it — most sessions never share, and
// those pay nothing but the port check in [shareDemux].
type shareSession struct {
	ip   [4]byte
	send func([]byte) (int, error)

	mu     sync.Mutex
	closed bool
	stack  atomic.Pointer[netstackTUN]
}

func newShareSession(ip net.IP, send func([]byte) (int, error)) *shareSession {
	s := &shareSession{send: send}
	copy(s.ip[:], ip.To4())
	return s
}

// netstack returns the session's stack, building it on first use.
func (s *shareSession) netstack() (*netstackTUN, error) {
	if st := s.stack.Load(); st != nil {
		return st, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, errShareNoTunnel
	}
	if st := s.stack.Load(); st != nil {
		return st, nil
	}

	st, err := newNetstackTUN()
	if err != nil {
		return nil, err
	}
	if err := st.stack.SetPortRange(shareFirstPort, shareLastPort); err != nil {
		_ = st.Close() //nolint:errcheck
		return nil, fmt.Errorf("vpn: share netstack port range: %s", err)
	}
	if err := st.configure(netip.AddrFrom4(s.ip).String() + TUNNetmaskCIDR); err != nil {
		_ = st.Close() //nolint:errcheck
		return nil, err
	}
	go s.pump(st)
	s.stack.Store(st)

	return st, nil
}

// pump carries the netstack's outbound packets to the server until the stack
// closes. A failed send ends it: the conn is gone, and the session with it.
func (s *shareSession) pump(st *netstackTUN) {
	buf := make([]byte, TUNMTU+4)
	for {
		n, err := st.Read(buf)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			continue
		}
		if _, err := s.send(buf[:n]); err != nil {
			return
		}
	}
}

// close tears the stack down, and with it every shared connection of the
// session. Later dials are refused rather than building a new stack.
func (s *shareSession) close() {
	s.mu.Lock()
	s.closed = true
	st := s.stack.Swap(nil)
	s.mu.Unlock()

	if st != nil {
		_ = st.Close() //nolint:errcheck
	}
}

// shareDemux is the conn→TUN writer while sharing is on: each Write is one
// packet from the server, as io.Copy hands them over from the conn, and each
// goes to the netstack when it is a reply to one of its connections.
type shareDemux struct {
	tun  io.Writer
	sess *shareSession
}

func (d shareDemux) Write(p []byte) (int, error) {
	if st := d.sess.stack.Load(); st != nil && isSharedPacket(p, d.sess.ip) {
		return st.Write(p)
	}
	return d.tun.Write(p)
}

// isSharedPacket reports whether p is an IPv4 TCP or UDP packet to ip whose
// destination port is in the netstack's range. A later fragment carries no
// port, so it is the kernel's; the netstack's own traffic is small enough
// never to be fragmented on the way back.
func isSharedPacket(p []byte, ip [4]byte) bool {
	if len(p) < 20 || p[0]>>4 != 4 {
		return false
	}
	ihl := int(p[0]&0x0f) * 4
	if ihl < 20 || len(p) < ihl+4 {
		return false
	}
	if [4]byte(p[16:20]) != ip {
		return false
	}
	if binary.BigEndian.Uint16(p[6:8])&0x1fff != 0 {
		return false
	}
	if proto := p[9]; proto != 6 && proto != 17 {
		return false
	}
	return binary.BigEndian.Uint16(p[ihl+2:ihl+4]) >= shareFirstPort
}

// lockedWriter serializes whole-packet writes: the kernel's packets and the
// netstack's share one conn, and a frame must go out in one piece.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// shareDial opens a connection through the tunnel for a shared client.
func (c *Client) shareDial(ctx context.Context, network, address string) (net.Conn, error) {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil, fmt.Errorf("vpn: shared dial wants ip:port, got %q: %w", address, err)
	}
	if a := ap.Addr().Unmap(); a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() {
		return nil, errShareLocal
	}
	sess := c.share.Load()
	if sess == nil {
		return nil, errShareNoTunnel
	}
	st, err := sess.netstack()
	if err != nil {
		return nil, err
	}
	return st.dial(ctx, network, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()).String())
}

// shareResolver resolves shared clients' names through the tunnel, at the
// resolver vpn-client was given (--dns) or the phone's default.
func (c *Client) shareResolver() *net.Resolver {
	dns := shareDefaultDNS
	if ip := net.ParseIP(c.cfg.DNSAddr); ip != nil && ip.To4() != nil {
		dns = ip.String()
	}
	server := net.JoinHostPort(dns, "53")
	return &net.Resolver{
		PreferGo: true,
		// The address is the one in the host's resolv.conf, if there even is
		// one; the network is honored, so a truncated answer still retries
		// over TCP.
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return c.shareDial(ctx, network, server)
		},
	}
}

// shareNames is the SOCKS5 server's view of [Client.shareResolver]. The tunnel
// is IPv4, so only A records are asked for.
type shareNames struct{ r *net.Resolver }

func (n shareNames) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	ips, err := n.r.LookupNetIP(ctx, "ip4", name)
	if err != nil {
		return ctx, nil, err
	}
	if len(ips) == 0 {
		return ctx, nil, fmt.Errorf("vpn: no IPv4 address for %s", name)
	}
	return ctx, net.IP(ips[0].Unmap().AsSlice()), nil
}

// serveShared answers shared clients arriving on l until it closes: SOCKS5 and
// HTTP proxy requests on the same connections, every one dialed through the
// tunnel. Each accepted connection is served on its own goroutine.
func (c *Client) serveShared(l net.Listener) error {
	names := shareNames{r: c.shareResolver()}
	srv, err := socks5.New(&socks5.Config{
		Resolver: names,
		Dial:     c.shareDial,
		Logger:   log.New(io.Discard, "", 0),
	})
	if err != nil {
		return err
	}
	httpDial := proxyfront.SOCKSDial(names, c.shareDial)
	return srv.Serve(proxyfront.Split(l, func(conn net.Conn) {
		proxyfront.ServeHTTP(context.Background(), conn, httpDial)
	}))
}

// connQueue is a net.Listener fed by hand: on the phone the app accepts shared
// clients itself — it knows which interface is the hotspot — and passes each
// connection over (share_android.go).
type connQueue struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newConnQueue() *connQueue {
	return &connQueue{conns: make(chan net.Conn), done: make(chan struct{})}
}

// push hands conn to Accept, closing it instead when the queue has closed.
func (q *connQueue) push(conn net.Conn) bool {
	select {
	case q.conns <- conn:
		return true
	case <-q.done:
		_ = conn.Close() //nolint:errcheck
		return false
	}
}

func (q *connQueue) Accept() (net.Conn, error) {
	select {
	case conn := <-q.conns:
		return conn, nil
	case <-q.done:
		return nil, net.ErrClosed
	}
}

func (q *connQueue) Close() error {
	q.once.Do(func() { close(q.done) })
	return nil
}

func (q *connQueue) Addr() net.Addr { return shareAddr{} }

type shareAddr struct{}

func (shareAddr) Network() string { return "share" }
func (shareAddr) String() string  { return "hotspot" }
