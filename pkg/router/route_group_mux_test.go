package router

import (
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
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
	// These helpers model already-established legs, so mark every leg
	// ready for selection. In live operation an aux leg becomes ready
	// only after inbound traffic proves the peer registered its rule.
	rg.mux.growLegs(len(mts))
	for i := range mts {
		rg.mux.markLegReady(i)
	}
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

// TestMuxSelectTransportSkipsNotReadyLeg verifies that an aux leg that has
// not yet been confirmed ready is never selected for sending, so the first
// writes can't be steered onto a route the peer hasn't registered (the
// mux>=2 "0 bytes / close code 0" bug). The primary leg (0) is always ready.
func TestMuxSelectTransportSkipsNotReadyLeg(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 2)

	// Reset readiness: primary ready, aux (leg 1) not ready.
	rg.mux.legMu.Lock()
	rg.mux.ready = []bool{true, false}
	rg.mux.legMu.Unlock()

	// Every selection must return the primary while leg 1 is not ready.
	for i := 0; i < 20; i++ {
		rg.mu.Lock()
		tp, _, idx, err := rg.mux.selectTransport(rg.tps, rg.fwd, nil)
		rg.mu.Unlock()
		require.NoError(t, err)
		require.Equal(t, 0, idx, "must not select the not-ready aux leg")
		require.Equal(t, mts[0], tp)
	}

	// Once leg 1 is marked ready, it becomes eligible.
	rg.mux.markLegReady(1)
	sawLeg1 := false
	for i := 0; i < 50 && !sawLeg1; i++ {
		rg.mu.Lock()
		_, _, idx, err := rg.mux.selectTransport(rg.tps, rg.fwd, nil)
		rg.mu.Unlock()
		require.NoError(t, err)
		if idx == 1 {
			sawLeg1 = true
		}
	}
	require.True(t, sawLeg1, "leg 1 should be selectable once marked ready")
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
	tp, _, _, err := rg.mux.selectTransport(rg.tps, rg.fwd, nil)
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
	_, _, _, err := rg.mux.selectTransport(rg.tps, rg.fwd, nil)
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

// TestRouteMuxRemoveLegs verifies removeLegs compacts legs[] and ready[] in
// lockstep so readiness/accounting stay attached to the right leg after a
// middle leg is removed (the bug: ready[] was never shrunk → wrong-leg
// readiness, the mux>=2 hang).
func TestRouteMuxRemoveLegs(t *testing.T) {
	m := &routeMux{}
	m.growLegs(5) // ready = [T,F,F,F,F]
	m.markLegReady(2)
	m.markLegReady(4) // ready = [T,F,T,F,T]
	for i := range m.legs {
		m.legs[i].sentBytes = uint64(i * 10) // tag identity: 0,10,20,30,40
	}

	m.removeLegs(1, 3) // drop middle legs (ascending, as pruneDeadTransports passes)

	require.Len(t, m.legs, 3, "legs not compacted")
	require.Len(t, m.ready, 3, "ready not compacted")
	// Survivors are original legs 0,2,4 in order.
	require.Equal(t, []bool{true, true, true}, m.ready, "ready bits must follow the surviving legs")
	require.Equal(t, uint64(0), m.legs[0].sentBytes)
	require.Equal(t, uint64(20), m.legs[1].sentBytes, "former leg 2's counters must move to index 1")
	require.Equal(t, uint64(40), m.legs[2].sentBytes, "former leg 4's counters must move to index 2")
	require.True(t, m.legReadyAt(1), "former leg 2 (ready) must read ready at its new index")
}

// TestRouteMuxRemoveLegsPromotesPrimary covers the pruneDeadTransports
// dead-primary case: removing leg 0 promotes the next leg, which must be
// re-marked always-ready.
func TestRouteMuxRemoveLegsPromotesPrimary(t *testing.T) {
	m := &routeMux{}
	m.growLegs(3)     // ready = [T,F,F]
	m.markLegReady(2) // ready = [T,F,T]
	m.removeLegs(0)   // drop the primary; former leg 1 becomes the new leg 0
	require.Len(t, m.ready, 2)
	require.True(t, m.ready[0], "promoted primary must be marked ready")
	require.True(t, m.ready[1], "former leg 2 keeps its ready bit")
}

// TestRouteMuxRemoveLegsEmpty is the no-op guard.
func TestRouteMuxRemoveLegsEmpty(t *testing.T) {
	m := &routeMux{}
	m.growLegs(2)
	m.removeLegs() // no indices → no change
	require.Len(t, m.legs, 2)
	require.Len(t, m.ready, 2)
}
