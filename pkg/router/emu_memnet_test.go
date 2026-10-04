package router_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/skycoin/skywire/pkg/loadtest"
)

// memNet is the stream bench's network: listeners and dials that never touch
// a socket, so a cell can run in a synctest bubble on a fake clock. A real
// loopback socket is not something a bubble can wait on.
type memNet struct {
	mu sync.Mutex
	ls map[string]*memListener
}

type memListener struct {
	addr memAddr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
	n    *memNet
}

type memAddr string

func (a memAddr) Network() string { return "mem" }
func (a memAddr) String() string  { return string(a) }

func newMemNet() *memNet { return &memNet{ls: map[string]*memListener{}} }

// Listen serves addr on this network.
func (n *memNet) Listen(addr string) *memListener {
	l := &memListener{addr: memAddr(addr), ch: make(chan net.Conn), done: make(chan struct{}), n: n}
	n.mu.Lock()
	n.ls[addr] = l
	n.mu.Unlock()
	return l
}

// Dial connects to the listener at addr through an in-memory pipe.
func (n *memNet) Dial(ctx context.Context, _, addr string) (net.Conn, error) {
	n.mu.Lock()
	l := n.ls[addr]
	n.mu.Unlock()
	if l == nil {
		return nil, &net.OpError{Op: "dial", Net: "mem", Err: errors.New("connection refused: " + addr)}
	}
	p1, p2 := net.Pipe()
	c, s := tcpConn{p1, memTCPAddr("127.0.0.1:1"), memTCPAddr(addr)}, tcpConn{p2, memTCPAddr(addr), memTCPAddr("127.0.0.1:1")}
	select {
	case l.ch <- s:
		return c, nil
	case <-l.done:
	case <-ctx.Done():
	}
	_ = c.Close() //nolint:errcheck
	_ = s.Close() //nolint:errcheck
	return nil, &net.OpError{Op: "dial", Net: "mem", Err: net.ErrClosed}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.n.mu.Lock()
		if l.n.ls[string(l.addr)] == l {
			delete(l.n.ls, string(l.addr))
		}
		l.n.mu.Unlock()
	})
	return nil
}

func (l *memListener) Addr() net.Addr { return l.addr }

// benchNet is the current cell's network. Cells run one at a time, and each
// builds its own inside its bubble.
var benchNet *memNet

// The sink answers at this address on every cell's network.
const (
	sinkAddr = "127.0.0.1:18080"
	sinkPort = 18080
)

// benchCell runs one bench cell as a subtest. By default the cell runs in a
// synctest bubble, so its timings are those of the emulated links alone and
// do not depend on how busy the machine is. EMUSTREAM_REALTIME=1 runs it on
// the wall clock instead, for measurements that should include CPU cost.
func benchCell(t *testing.T, name string, f func(t *testing.T)) {
	t.Run(name, func(t *testing.T) {
		run := func(t *testing.T) {
			benchNet = newMemNet()
			srv := &http.Server{Handler: loadtest.Handler()} //nolint:gosec // in-memory, test only
			l := benchNet.Listen(sinkAddr)
			go func() { _ = srv.Serve(l) }()      //nolint:errcheck // Close ends it
			t.Cleanup(func() { _ = srv.Close() }) //nolint:errcheck
			f(t)
		}
		if os.Getenv("EMUSTREAM_REALTIME") == "1" {
			run(t)
			return
		}
		synctest.Test(t, run)
	})
}

// tcpConn reports TCP addresses: go-socks5 asserts the exit's outbound conn
// is a *net.TCPAddr to fill in its reply.
type tcpConn struct {
	net.Conn
	local, remote *net.TCPAddr
}

func (c tcpConn) LocalAddr() net.Addr  { return c.local }
func (c tcpConn) RemoteAddr() net.Addr { return c.remote }

// memTCPAddr never resolves a name. Go's resolver keeps channels that must not
// cross synctest bubbles, and a name like proxy-1 would reach real DNS.
func memTCPAddr(hostport string) *net.TCPAddr {
	a := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return a
	}
	if ip := net.ParseIP(host); ip != nil {
		a.IP = ip
	}
	a.Port, _ = strconv.Atoi(port) //nolint:errcheck
	return a
}

// literalResolver answers the exit's SOCKS5 name lookups without DNS, for the
// same reason as memTCPAddr.
type literalResolver struct{}

func (literalResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	if ip := net.ParseIP(name); ip != nil {
		return ctx, ip, nil
	}
	return ctx, nil, fmt.Errorf("memnet: %q is not an address", name)
}
