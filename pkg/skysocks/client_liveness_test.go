// Package skysocks client liveness-probe tests.
package skysocks

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
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
