// Package skysocks client liveness-probe tests.
package skysocks

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/0magnet/yamux"
)

// blackholeConn is a net.Conn whose writes are silently discarded and
// whose reads block until Close. It models a route group torn down
// router-side WITHOUT delivering EOF — the silent-collapse case that
// session.IsClosed() misses and sessionAlive must catch via ping timeout.
type blackholeConn struct {
	closed chan struct{}
	once   sync.Once
}

func newBlackholeConn() *blackholeConn { return &blackholeConn{closed: make(chan struct{})} }

func (b *blackholeConn) Read(_ []byte) (int, error) {
	<-b.closed
	return 0, io.EOF
}
func (b *blackholeConn) Write(p []byte) (int, error) { return len(p), nil }
func (b *blackholeConn) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}
func (b *blackholeConn) LocalAddr() net.Addr                { return testAddr{} }
func (b *blackholeConn) RemoteAddr() net.Addr               { return testAddr{} }
func (b *blackholeConn) SetDeadline(_ time.Time) error      { return nil }
func (b *blackholeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (b *blackholeConn) SetWriteDeadline(_ time.Time) error { return nil }

type testAddr struct{}

func (testAddr) Network() string { return "test" }
func (testAddr) String() string  { return "test" }

// TestSessionAlive_Healthy: a live yamux session (peer pongs) reports alive.
func TestSessionAlive_Healthy(t *testing.T) {
	p1, p2 := net.Pipe()
	defer p1.Close() //nolint:errcheck
	defer p2.Close() //nolint:errcheck

	server, err := yamux.Server(p2, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux server: %v", err)
	}
	defer server.Close() //nolint:errcheck

	c, err := NewClient(p1, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close() //nolint:errcheck

	if !c.sessionAlive(2 * time.Second) {
		t.Fatal("expected a healthy session to report alive")
	}
}

// TestSessionAlive_SilentCollapse: a conn that accepts writes but never
// pongs (silent rg teardown) reports NOT alive within the timeout — this
// is what makes the keepalive loop close the client so --reconnect fires.
func TestSessionAlive_SilentCollapse(t *testing.T) {
	c, err := NewClient(newBlackholeConn(), nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close() //nolint:errcheck

	if c.sessionAlive(500 * time.Millisecond) {
		t.Fatal("expected a silently-dead session to report not-alive")
	}
}

// gatedConn wraps a conn and can pause its Read path, modeling a reorder WEDGE:
// the pong (which arrives on the read/download direction) is dammed until the
// gate reopens, exactly as a missing sequence dams later packets on the ordered
// RouteGroup. Writes (the ping, upload direction) pass through unaffected.
type gatedConn struct {
	net.Conn
	mu     sync.Mutex
	open   chan struct{} // closed == reads allowed
	closed chan struct{} // closed on Close so a parked Read unblocks (mirrors a real RouteGroup)
	once   sync.Once
}

func newGatedConn(c net.Conn) *gatedConn {
	g := &gatedConn{Conn: c, open: make(chan struct{}), closed: make(chan struct{})}
	close(g.open) // start open
	return g
}

func (g *gatedConn) Close() error {
	g.once.Do(func() { close(g.closed) })
	return g.Conn.Close()
}

func (g *gatedConn) pause() {
	g.mu.Lock()
	select {
	case <-g.open: // currently open — install a fresh closed gate
		g.open = make(chan struct{})
	default: // already paused
	}
	g.mu.Unlock()
}

func (g *gatedConn) resume() {
	g.mu.Lock()
	select {
	case <-g.open: // already open
	default:
		close(g.open)
	}
	g.mu.Unlock()
}

func (g *gatedConn) Read(p []byte) (int, error) {
	g.mu.Lock()
	ch := g.open
	g.mu.Unlock()
	select {
	case <-ch: // gate open — pong flows
		return g.Conn.Read(p)
	case <-g.closed: // conn closed while parked — unblock like a real RouteGroup
		return 0, io.EOF
	}
}

// clientClosed reports whether the keepalive loop tore the whole client down.
func clientClosed(c *Client) bool {
	select {
	case <-c.closeC:
		return true
	default:
		return false
	}
}

// newLoopHarness wires a client whose sole tunnel talks to a real yamux server
// through a gatedConn, and shrinks the loop cadence so a wedge can be simulated
// in milliseconds. Returns the client and the gate.
func newLoopHarness(t *testing.T) (*Client, *gatedConn) {
	t.Helper()
	oldInterval, oldWindow := livenessProbeInterval, sessionHardDeadWindow
	livenessProbeInterval = 15 * time.Millisecond
	sessionHardDeadWindow = 90 * time.Millisecond
	t.Cleanup(func() { livenessProbeInterval, sessionHardDeadWindow = oldInterval, oldWindow })

	p1, p2 := net.Pipe()
	server, err := yamux.Server(p2, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux server: %v", err)
	}
	t.Cleanup(func() {
		server.Close() //nolint:errcheck,gosec
	})

	gate := newGatedConn(p1)
	c, err := NewClient(gate, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() {
		c.Close() //nolint:errcheck,gosec
	})
	return c, gate
}

// TestKeepAlive_HealthySurvives: with pongs always flowing, the loop never
// retires the tunnel across many probe windows.
func TestKeepAlive_HealthySurvives(t *testing.T) {
	c, _ := newLoopHarness(t)
	time.Sleep(6 * sessionHardDeadWindow)
	if clientClosed(c) {
		t.Fatal("healthy tunnel was wrongly torn down")
	}
}

// TestKeepAlive_WedgeSurvives is the regression test for the false-teardown
// collapse: the pong is dammed for stretches shorter than the hard-dead window
// (a reorder wedge), but arrives late whenever the gate reopens. The loop must
// keep the tunnel alive across repeated wedges instead of mistaking them for a
// dead conn and closing the whole client.
func TestKeepAlive_WedgeSurvives(t *testing.T) {
	c, gate := newLoopHarness(t)
	// Several wedge cycles: pause ~4 intervals (stalls multiple probes) then a
	// clear window during which the late pong flows and refreshes liveness. Each
	// pause is shorter than the hard-dead window, so a correct loop survives.
	for i := 0; i < 4; i++ {
		gate.pause()
		time.Sleep(sessionHardDeadWindow * 3 / 4)
		gate.resume()
		time.Sleep(sessionHardDeadWindow * 3 / 4)
		if clientClosed(c) {
			t.Fatalf("wedged-but-live tunnel wrongly torn down on cycle %d", i)
		}
	}
}

// TestKeepAlive_TrueSilenceRetires: a conn that never pongs (gate paused and
// never reopened) is still retired after the hard-dead window, so a genuinely
// dead exit reconnects.
func TestKeepAlive_TrueSilenceRetires(t *testing.T) {
	c, gate := newLoopHarness(t)
	gate.pause() // never resume — no pong ever arrives
	deadline := time.Now().Add(8 * sessionHardDeadWindow)
	for time.Now().Before(deadline) {
		if clientClosed(c) {
			return // retired as expected
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("silently-dead tunnel was never retired")
}
