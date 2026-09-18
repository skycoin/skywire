// Package router pkg/router/router_forward_test.go
//
// The regression test for the all-paths blackout of 2026-09-18: a transit peer
// that stops draining must cost its own frames and nothing else.
package router

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/emu"
	rs "github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// wedgedLeg is an emulated transport whose peer never drains: one frame fits
// in the egress queue and the link serves it at a byte per second, so every
// following write parks until its deadline. That is the live shape — a peer
// whose socket buffer is full — and NOT emu's Cut(), which accepts writes and
// black-holes them (a cut link would never have stalled anything).
func wedgedLeg(t *testing.T, name string) (near network.Transport, far *emu.Conn) {
	t.Helper()
	lpk, _ := cipher.GenerateKeyPair()
	rpk, _ := cipher.GenerateKeyPair()
	a, b := emu.NewPair(emu.PairConfig{
		Name: name,
		AtoB: emu.LinkConfig{RateBps: 1, QueueBytes: 1},
		BtoA: emu.LinkConfig{},
		APK:  lpk, BPK: rpk, Type: types.STCPR,
	})
	t.Cleanup(func() { _ = a.Close() }) //nolint:errcheck
	return a, b
}

// healthyLeg is the same thing with no impairment at all.
func healthyLeg(t *testing.T, name string) (near network.Transport, far *emu.Conn) {
	t.Helper()
	lpk, _ := cipher.GenerateKeyPair()
	rpk, _ := cipher.GenerateKeyPair()
	a, b := emu.NewPair(emu.PairConfig{Name: name, APK: lpk, BPK: rpk, Type: types.STCPR})
	t.Cleanup(func() { _ = a.Close() }) //nolint:errcheck
	return a, b
}

func newForwardTestRouter(t *testing.T) (*router, *transport.Manager) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	tm, err := transport.NewManager(nil, nil, nil, &transport.ManagerConfig{
		PubKey: pk, SecKey: sk, DiscoveryClient: transport.NewDiscoveryMock(),
	}, network.ClientFactory{})
	require.NoError(t, err)
	r := &router{
		logger:        logging.MustGetLogger("test_forward"),
		tm:            tm,
		rt:            routing.NewTable(logging.MustGetLogger("test_forward_rt")),
		rgsDatagrams:  make(map[routing.RouteDescriptor]*DatagramRouteGroup),
		datagramPorts: make(map[routing.Port]struct{}),
		done:          make(chan struct{}),
	}
	t.Cleanup(func() { close(r.done) })
	return r, tm
}

func injectTransport(t *testing.T, tm *transport.Manager, conn network.Transport, id uuid.UUID) {
	t.Helper()
	mt := transport.NewManagedTransportForTest(conn)
	mt.Entry.ID = id
	mt.Entry.Type = types.STCPR
	tm.InjectTransportForTest(mt)
}

// TestTransitWriteDoesNotBlockTheServeLoop is the reproduction.
//
// Live on 2026-09-18 the visor's single inbound packet loop sat in
// managed_transport.writeTo for a TRANSIT peer (a stranger routing through
// us) with a context that never cancels and a one-minute deadline, and every
// transport on the visor logged "Dropping packet: readCh full for 30s" while
// it did. handleTransportPacket IS that loop's body, so the assertion is
// simply that it keeps returning: the packets for a second, healthy route are
// delivered while the first route's next hop is wedged.
//
// Before the fix this test does not fail, it HANGS — the first transit frame
// parks for the transport's whole write deadline.
func TestTransitWriteDoesNotBlockTheServeLoop(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.NoError(t, rs.Set(rs.ForwardWriteTimeout.Name(), "200ms"))
	require.NoError(t, rs.Set(rs.ForwardQueueDepth.Name(), "8"))

	r, tm := newForwardTestRouter(t)
	wedged, _ := wedgedLeg(t, "transit")
	wedgedID := uuid.New()
	injectTransport(t, tm, wedged, wedgedID)

	// Route 1: a stranger's route that only transits this visor, whose next
	// hop has stopped draining.
	const transitKey = routing.RouteID(10)
	require.NoError(t, r.rt.SaveRule(routing.IntermediaryForwardRule(time.Hour, transitKey, routing.RouteID(11), wedgedID)))

	// Route 2: a route that ENDS here, with a live consumer. This is the
	// traffic the blackout took down as collateral.
	lPK, _ := cipher.GenerateKeyPair()
	rPK, _ := cipher.GenerateKeyPair()
	const localKey = routing.RouteID(42)
	consume := routing.ConsumeRule(time.Hour, localKey, lPK, rPK, 100, 200)
	require.NoError(t, r.rt.SaveRule(consume))
	dg := NewDatagramRouteGroup(nil, nil, consume.RouteDescriptor(), nil)
	t.Cleanup(func() { _ = dg.Close() }) //nolint:errcheck
	r.setDatagramRouteGroup(consume.RouteDescriptor(), dg)

	transit, err := routing.MakeDataPacket(transitKey, make([]byte, 1024))
	require.NoError(t, err)
	local, err := routing.MakeDatagramPacket(localKey, []byte("the traffic that must not stop"))
	require.NoError(t, err)

	const rounds = 200
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < rounds; i++ {
		// A transit frame for the wedged peer, then one for the local group:
		// exactly the interleaving the single loop sees.
		require.NoError(t, r.handleTransportPacket(ctx, transit))
		require.NoError(t, r.handleTransportPacket(ctx, local))
	}
	elapsed := time.Since(start)

	// The whole interleaved run must cost far less than ONE old synchronous
	// write to a wedged peer (writeTimeout, 1 minute) — and in practice less
	// than one forward.write_timeout, since the loop never waits on the write
	// at all.
	require.Less(t, elapsed, 5*time.Second,
		"the serve loop was parked by a transit peer that stopped draining")
	require.Equal(t, rounds, drainDatagrams(dg),
		"packets for a healthy route group were lost while a transit peer was wedged")

	// The wedged peer's own frames are dropped, counted and named — and the
	// loss is on the record as ONE mux event, not one per frame.
	require.Eventually(t, func() bool {
		dropped := false
		for _, q := range r.IntakeStats().ForwardQueues {
			if q.TpID == wedgedID && q.DropsQueueFull > 0 {
				dropped = true
			}
		}
		events := 0
		for _, e := range r.MuxEvents() {
			if e.Event == MuxEventForwardDrops && e.TpID == wedgedID {
				events++
			}
		}
		return dropped && events == 1
	}, 5*time.Second, 20*time.Millisecond,
		"expected forward_drop_queue_full counters and exactly one forward_drops event for the wedged next hop")
}

// drainDatagrams counts what the group has already handed out, so the
// delivered total is queue depth plus reads.
func drainDatagrams(dg *DatagramRouteGroup) int {
	n := 0
	for {
		select {
		case <-dg.readCh:
			n++
		default:
			return n
		}
	}
}

// A healthy next hop must still relay everything, in order, off the queue —
// the write moved to a goroutine, it did not become lossy or unordered.
func TestTransitWriteStillRelaysInOrder(t *testing.T) {
	t.Cleanup(rs.Reset)
	r, tm := newForwardTestRouter(t)
	near, far := healthyLeg(t, "relay")
	tpID := uuid.New()
	injectTransport(t, tm, near, tpID)

	const key = routing.RouteID(7)
	const next = routing.RouteID(8)
	require.NoError(t, r.rt.SaveRule(routing.IntermediaryForwardRule(time.Hour, key, next, tpID)))

	const frames = 64
	ctx := context.Background()
	for i := 0; i < frames; i++ {
		p, err := routing.MakeDataPacket(key, []byte{byte(i)})
		require.NoError(t, err)
		require.NoError(t, r.handleTransportPacket(ctx, p))
	}

	buf := make([]byte, 1024)
	for i := 0; i < frames; i++ {
		require.NoError(t, far.SetReadDeadline(time.Now().Add(5*time.Second)))
		n, err := far.Read(buf)
		require.NoError(t, err)
		got := routing.Packet(buf[:n])
		require.Equal(t, next, got.RouteID(), "the next-hop route ID must be re-stamped")
		require.Equal(t, []byte{byte(i)}, got.Payload(), "frame %d arrived out of order", i)
	}

	// The last frame is on the wire before its counter is; the read loop above
	// can observe it first, so settle rather than sample.
	require.Eventually(t, func() bool {
		stats := r.IntakeStats().ForwardQueues
		return len(stats) == 1 && stats[0].Sent == frames
	}, 5*time.Second, 10*time.Millisecond, "not every relayed frame was counted as sent")
	stats := r.IntakeStats().ForwardQueues
	require.Zero(t, stats[0].DropsQueueFull+stats[0].DropsWriteTimeout+stats[0].DropsWriteError)
}
