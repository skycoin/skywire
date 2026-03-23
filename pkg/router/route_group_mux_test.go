package router

import (
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// workingTransport implements network.Transport with working Write/Read.
type workingTransport struct {
	closed   chan struct{}
	pk1, pk2 cipher.PubKey
}

func newWorkingTransport() *workingTransport {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	return &workingTransport{
		closed: make(chan struct{}),
		pk1:    pk1,
		pk2:    pk2,
	}
}

func (t *workingTransport) Write(b []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, net.ErrClosed
	default:
		return len(b), nil // Succeed immediately
	}
}

func (t *workingTransport) Read([]byte) (int, error) { <-t.closed; return 0, net.ErrClosed }
func (t *workingTransport) Close() error {
	select {
	case <-t.closed:
	default:
		close(t.closed)
	}
	return nil
}
func (t *workingTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (t *workingTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (t *workingTransport) SetDeadline(time.Time) error      { return nil }
func (t *workingTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *workingTransport) SetWriteDeadline(time.Time) error { return nil }
func (t *workingTransport) LocalPK() cipher.PubKey           { return t.pk1 }
func (t *workingTransport) RemotePK() cipher.PubKey          { return t.pk2 }
func (t *workingTransport) LocalPort() uint16                { return 0 }
func (t *workingTransport) RemotePort() uint16               { return 0 }
func (t *workingTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (t *workingTransport) RemoteRawAddr() net.Addr          { return &net.TCPAddr{} }
func (t *workingTransport) Network() types.Type              { return "test" }

func createMuxRouteGroup(t *testing.T, nTransports int) (*RouteGroup, []*transport.ManagedTransport, []*workingTransport) {
	t.Helper()

	l := logging.NewMasterLogger()
	rt := routing.NewTable(l.PackageLogger("rgt"))

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	desc := routing.NewRouteDescriptor(pk1, pk2, 1, 2)

	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, desc, l)
	rg.mux = newRouteMux(l.PackageLogger("mux"), false)

	mts := make([]*transport.ManagedTransport, 0, nTransports)
	conns := make([]*workingTransport, 0, nTransports)
	fwds := make([]routing.Rule, 0, nTransports)
	rvss := make([]routing.Rule, 0, nTransports)

	for i := 0; i < nTransports; i++ {
		tpID := uuid.New()
		fwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(i+1), routing.RouteID(i+100), tpID, pk2, pk1, 1, 2) //nolint:gosec
		rvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(i+100), pk1, pk2, 2, 1)                             //nolint:gosec
		rt.SaveRule(fwd)                                                                                                      //nolint:errcheck,gosec
		rt.SaveRule(rvs)                                                                                                      //nolint:errcheck,gosec

		conn := newWorkingTransport()
		mt := transport.NewManagedTransportForTest(conn)
		mt.Entry = transport.Entry{ID: tpID, Type: "test"}

		mts = append(mts, mt)
		conns = append(conns, conn)
		fwds = append(fwds, fwd)
		rvss = append(rvss, rvs)
	}

	rg.mu.Lock()
	rg.tps = mts
	rg.fwd = fwds
	rg.rvs = rvss
	rg.mux.rebuildWeights(mts)
	rg.mu.Unlock()

	return rg, mts, conns
}

// closeManagedTransport closes a ManagedTransport by first closing the
// underlying connection (which causes future writes to fail) and then
// triggering the ManagedTransport's close (which sets IsClosed=true).
func closeManagedTransport(mt *transport.ManagedTransport, conn *workingTransport) {
	conn.Close() //nolint:errcheck,gosec
	mt.CloseForTest()
}

// TestMuxKeepaliveSkipsDeadTransport verifies that keepalive succeeds when
// one of multiple mux transports is dead, instead of failing the whole group.
func TestMuxKeepaliveSkipsDeadTransport(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)

	// Kill one transport
	closeManagedTransport(mts[1], conns[1])

	// Keepalive should succeed — 2 of 3 still work
	err := rg.sendKeepAlive()
	require.NoError(t, err)
}

// TestMuxKeepaliveFailsAllDead verifies that keepalive fails when
// all mux transports are dead.
func TestMuxKeepaliveFailsAllDead(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 2)

	// Kill all transports
	for i := range conns {
		closeManagedTransport(mts[i], conns[i])
	}

	// Keepalive should fail
	err := rg.sendKeepAlive()
	require.Error(t, err)
}

// TestPruneDeadTransports verifies that dead transports are removed from
// the mux group during keepalive, and rules are cleaned up.
func TestPruneDeadTransports(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)

	// Kill one transport
	closeManagedTransport(mts[0], conns[0])

	// Prune
	rg.mu.Lock()
	rg.pruneDeadTransports()
	remaining := len(rg.tps)
	rg.mu.Unlock()

	require.Equal(t, 2, remaining, "should have pruned 1 dead transport")
}

// TestPrunePreservesLastTransport verifies that pruning never removes
// the last transport, even if it's dead.
func TestPrunePreservesLastTransport(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 1)

	// Kill the only transport
	closeManagedTransport(mts[0], conns[0])

	// Prune should not remove it
	rg.mu.Lock()
	rg.pruneDeadTransports()
	remaining := len(rg.tps)
	rg.mu.Unlock()

	require.Equal(t, 1, remaining, "should not prune the last transport")
}

// TestMuxSelectTransportSkipsDead verifies that the mux transport selector
// skips dead transports and uses healthy ones.
func TestMuxSelectTransportSkipsDead(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)

	// Kill transport 0 and 1
	closeManagedTransport(mts[0], conns[0])
	closeManagedTransport(mts[1], conns[1])

	// Select should return transport 2 (the only alive one)
	rg.mu.Lock()
	tp, _, err := rg.mux.selectTransport(rg.tps, rg.fwd)
	rg.mu.Unlock()

	require.NoError(t, err)
	require.Equal(t, mts[2], tp, "should select the only alive transport")
}

// TestMuxSelectTransportFailsAllDead verifies that selectTransport
// returns an error when all transports are dead.
func TestMuxSelectTransportFailsAllDead(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 2)

	for i := range conns {
		closeManagedTransport(mts[i], conns[i])
	}

	rg.mu.Lock()
	_, _, err := rg.mux.selectTransport(rg.tps, rg.fwd)
	rg.mu.Unlock()

	require.ErrorIs(t, err, ErrNoSuitableTransport)
}

// TestConsecutiveWriteFailuresOnlyOnTotalFailure verifies that the
// consecutiveWriteFailures counter only increments when ALL mux
// transports fail keepalive, not when just one fails.
func TestConsecutiveWriteFailuresOnlyOnTotalFailure(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 2)

	// Kill one — keepalive should succeed, counter stays at 0
	closeManagedTransport(mts[0], conns[0])

	rg.keepAliveServiceFn(0) // force keepalive immediately
	require.Equal(t, int32(0), rg.consecutiveWriteFailures)

	// Kill the other — keepalive should fail, counter increments
	closeManagedTransport(mts[1], conns[1])

	rg.keepAliveServiceFn(0)
	require.Equal(t, int32(1), rg.consecutiveWriteFailures)
}
