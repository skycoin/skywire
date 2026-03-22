package router

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// blockingTransport implements network.Transport with a Write that blocks forever,
// simulating a dead transport connection.
type blockingTransport struct {
	writeCh  chan struct{}
	pk1, pk2 cipher.PubKey
}

func newBlockingTransport() *blockingTransport {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	return &blockingTransport{
		writeCh: make(chan struct{}),
		pk1:     pk1,
		pk2:     pk2,
	}
}

func (bt *blockingTransport) Write([]byte) (int, error) {
	select {
	case <-bt.writeCh:
	default:
		close(bt.writeCh)
	}
	select {} // block forever
}

func (bt *blockingTransport) Read([]byte) (int, error)         { select {} }
func (bt *blockingTransport) Close() error                     { return nil }
func (bt *blockingTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (bt *blockingTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (bt *blockingTransport) SetDeadline(time.Time) error      { return nil }
func (bt *blockingTransport) SetReadDeadline(time.Time) error  { return nil }
func (bt *blockingTransport) SetWriteDeadline(time.Time) error { return nil }
func (bt *blockingTransport) LocalPK() cipher.PubKey           { return bt.pk1 }
func (bt *blockingTransport) RemotePK() cipher.PubKey          { return bt.pk2 }
func (bt *blockingTransport) LocalPort() uint16                { return 0 }
func (bt *blockingTransport) RemotePort() uint16               { return 0 }
func (bt *blockingTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (bt *blockingTransport) RemoteRawAddr() net.Addr          { return &net.TCPAddr{} }
func (bt *blockingTransport) Network() types.Type              { return "test" }

func createTestRouteGroupWithBlockingTransport(t *testing.T) (*RouteGroup, *blockingTransport) {
	t.Helper()

	l := logging.NewMasterLogger()
	rt := routing.NewTable(l.PackageLogger("rgt"))

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	desc := routing.NewRouteDescriptor(pk1, pk2, 1, 2)

	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, desc, l)

	tpID := uuid.New()
	fwdRule := routing.ForwardRule(DefaultRouteKeepAlive, 1, 2, tpID, pk2, pk1, 1, 2)
	rt.SaveRule(fwdRule) //nolint:errcheck

	conn := newBlockingTransport()
	mt := transport.NewManagedTransportForTest(conn)
	mt.Entry = transport.Entry{
		ID:   tpID,
		Type: "test",
	}

	rg.mu.Lock()
	rg.tps = []*transport.ManagedTransport{mt}
	rg.fwd = []routing.Rule{fwdRule}
	rg.mu.Unlock()

	return rg, conn
}

// TestRouteGroupCloseDeadTransport verifies that Close() on a route group
// with a dead (blocking) transport completes within a bounded time instead
// of deadlocking. Regression test for GC deadlock where broadcastClosePackets
// blocked forever on context.Background(), holding rg.mu.
func TestRouteGroupCloseDeadTransport(t *testing.T) {
	rg, _ := createTestRouteGroupWithBlockingTransport(t)

	done := make(chan error, 1)
	go func() {
		done <- rg.Close()
	}()

	select {
	case err := <-done:
		t.Logf("Close completed (err=%v)", err)
	case <-time.After(10 * time.Second):
		t.Fatal("Close() deadlocked on dead transport — did not complete within 10 seconds")
	}
}

// TestRouteGroupCloseNoGoroutineLeak verifies that after closing a route group
// with a dead transport, the waitForCloseRouteGroup goroutine is cleaned up.
func TestRouteGroupCloseNoGoroutineLeak(t *testing.T) {
	rg, _ := createTestRouteGroupWithBlockingTransport(t)

	rg.Close() //nolint:errcheck

	time.Sleep(200 * time.Millisecond)

	waitDone := make(chan struct{})
	go func() {
		rg.closeDone.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		// WaitGroup at zero — no leak
	case <-time.After(2 * time.Second):
		t.Fatal("closeDone WaitGroup not drained after Close() timeout — goroutine leak")
	}
}

// TestGCDoesNotDeadlockOnStuckClose simulates the GC calling Close() while
// another Close() is already stuck on a dead transport. The second Close()
// must complete within a bounded time.
func TestGCDoesNotDeadlockOnStuckClose(t *testing.T) {
	rg, conn := createTestRouteGroupWithBlockingTransport(t)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rg.Close() //nolint:errcheck
	}()

	// Wait for write to actually block
	<-conn.writeCh

	gcDone := make(chan struct{})
	go func() {
		rg.Close() //nolint:errcheck
		close(gcDone)
	}()

	select {
	case <-gcDone:
		t.Log("GC close completed without deadlock")
	case <-time.After(15 * time.Second):
		t.Fatal("GC deadlocked trying to close route group")
	}

	wg.Wait()
}
