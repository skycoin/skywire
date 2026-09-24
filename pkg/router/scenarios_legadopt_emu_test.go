// Package router pkg/router/scenarios_legadopt_emu_test.go c2-net-routing
//
// The LEG ADOPTION gate: a leg split out of an active tunnel (a leg reserve)
// becomes a stream-level tunnel again. Unlike the other shape-axis scenarios
// this one runs two REAL routers over emulated links, because what adoption has
// to prove is the part the emulated hosts skip: the chain goes through
// saveRouteGroupRules at both ends — the noise handshake, the accept path — and
// comes out as an ordinary net.Conn on each side.
package router

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/emu"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

const (
	adoptClientPort  routing.Port = 49170 // the active tunnel's local port
	adoptServicePort routing.Port = 3     // skysocks' port at the exit
	adoptNewPort     routing.Port = 49171 // the adopting dial's fresh local port
)

// adoptRig is two real routers joined by two emulated legs, holding one active
// two-leg tunnel: A dialed it, B is the exit.
type adoptRig struct {
	a, b       *router
	aPK, bPK   cipher.PubKey
	activeA    *RouteGroup
	activeB    *RouteGroup
	connsA     []*emu.Conn
	connsB     []*emu.Conn
	stop       chan struct{}
	reserveA   *RouteGroup
	reservePrt routing.Port
}

func newAdoptRouter(t *testing.T, pk cipher.PubKey, sk cipher.SecKey) *router {
	t.Helper()
	tm, err := transport.NewManager(nil, nil, nil, &transport.ManagerConfig{
		PubKey: pk, SecKey: sk, DiscoveryClient: transport.NewDiscoveryMock(),
	}, network.ClientFactory{})
	require.NoError(t, err)
	return &router{
		logger:        logging.MustGetLogger("adopt_test"),
		mLogger:       logging.NewMasterLogger(),
		conf:          &Config{PubKey: pk, SecKey: sk, MinHops: 1},
		tm:            tm,
		rt:            routing.NewTable(logging.MustGetLogger("adopt_rt")),
		rgsNs:         make(map[routing.RouteDescriptor]*NoiseRouteGroup),
		rgsRaw:        make(map[routing.RouteDescriptor]*RouteGroup),
		rgsDatagrams:  make(map[routing.RouteDescriptor]*DatagramRouteGroup),
		datagramPorts: make(map[routing.Port]struct{}),
		pending:       newPendingPackets(),
		pendingLegs:   newPendingLegs(),
		accept:        make(chan routing.EdgeRules, acceptSize),
		done:          make(chan struct{}),
	}
}

// newAdoptRig builds the rig and splits the tunnel's second leg out into a
// leg reserve, which is where every adoption starts.
func newAdoptRig(t *testing.T) *adoptRig {
	t.Helper()
	sbdOffForEmu(t)
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, bSK := cipher.GenerateKeyPair()
	rig := &adoptRig{a: newAdoptRouter(t, aPK, aSK), b: newAdoptRouter(t, bPK, bSK), aPK: aPK, bPK: bPK, stop: make(chan struct{})}
	t.Cleanup(func() {
		close(rig.stop)
		for i := range rig.connsA {
			_ = rig.connsA[i].Close() //nolint:errcheck
			_ = rig.connsB[i].Close() //nolint:errcheck
		}
	})

	// The descriptors the setup node hands each edge: Dst is the local visor.
	descA := routing.NewRouteDescriptor(bPK, aPK, adoptServicePort, adoptClientPort)
	descB := routing.NewRouteDescriptor(aPK, bPK, adoptClientPort, adoptServicePort)
	var tpsA, tpsB []*transport.ManagedTransport
	var fwdA, rvsA, fwdB, rvsB []routing.Rule
	for i := 0; i < 2; i++ {
		link := emu.LinkConfig{Delay: 20 * time.Millisecond, RateBps: 2 * emuMB, QueueBytes: 2 * emuMB}
		connA, connB := emu.NewPair(emu.PairConfig{Name: "adopt-leg", AtoB: link, BtoA: link, APK: aPK, BPK: bPK, Type: "emu"})
		id := uuid.New()
		mtA, mtB := transport.NewManagedTransportForTest(connA), transport.NewManagedTransportForTest(connB)
		mtA.Entry, mtB.Entry = transport.Entry{ID: id, Type: "emu"}, transport.Entry{ID: id, Type: "emu"}
		setRemoteForTest(mtA, bPK)
		setRemoteForTest(mtB, aPK)
		rig.a.tm.InjectTransportForTest(mtA)
		rig.b.tm.InjectTransportForTest(mtB)
		rig.connsA, rig.connsB = append(rig.connsA, connA), append(rig.connsB, connB)
		tpsA, tpsB = append(tpsA, mtA), append(tpsB, mtB)

		aF, aC := routing.RouteID(10+i), routing.RouteID(20+i) //nolint:gosec
		bF, bC := routing.RouteID(30+i), routing.RouteID(40+i) //nolint:gosec
		fwdA = append(fwdA, routing.ForwardRule(DefaultRouteKeepAlive, aF, bC, id, bPK, aPK, adoptServicePort, adoptClientPort))
		rvsA = append(rvsA, routing.ConsumeRule(DefaultRouteKeepAlive, aC, bPK, aPK, adoptServicePort, adoptClientPort))
		fwdB = append(fwdB, routing.ForwardRule(DefaultRouteKeepAlive, bF, aC, id, aPK, bPK, adoptClientPort, adoptServicePort))
		rvsB = append(rvsB, routing.ConsumeRule(DefaultRouteKeepAlive, bC, aPK, bPK, adoptClientPort, adoptServicePort))
	}
	rig.activeA = rig.wire(rig.a, descA, tpsA, fwdA, rvsA, true)
	rig.activeB = rig.wire(rig.b, descB, tpsB, fwdB, rvsB, false)
	for i := range rig.connsA {
		go rig.serve(rig.a, rig.connsA[i])
		go rig.serve(rig.b, rig.connsB[i])
	}

	reserve, err := rig.activeA.splitLeg(1, "test: release the second leg")
	require.NoError(t, err, "the split that makes the leg reserve")
	require.True(t, reserve.legReserve)
	rig.reserveA, rig.reservePrt = reserve, reserve.farEndPort()
	require.Eventually(t, func() bool { return rig.b.anyLegReserve() != nil }, 5*time.Second, 20*time.Millisecond,
		"the exit must hold its half of the reserve")
	return rig
}

// wire builds one end's active group the way a completed dial leaves it.
func (rig *adoptRig) wire(r *router, desc routing.RouteDescriptor, tps []*transport.ManagedTransport,
	fwd, rvs []routing.Rule, initiator bool) *RouteGroup {
	for i := range fwd {
		if err := r.rt.SaveRule(fwd[i]); err != nil {
			panic(err)
		}
		if err := r.rt.SaveRule(rvs[i]); err != nil {
			panic(err)
		}
	}
	rg := NewRouteGroup(DefaultRouteGroupConfig(), r.rt, desc, r.mLogger)
	rg.initiator = initiator
	rg.localPK = r.conf.PubKey
	rg.rehomeHost = r
	rg.muxEvents = &muxEventRing{}
	rg.SetAppName("skysocks-client")
	(&emuRig{}).wireEnd(&emuEnd{rg: rg, tps: tps}, fwd, rvs, emuOpts{LegRehome: true}, initiator, desc.SrcPK(), desc.DstPK())
	r.mx.Lock()
	r.rgsNs[desc] = &NoiseRouteGroup{rg: rg, Conn: rg}
	r.mx.Unlock()
	return rg
}

// serve is one router's read loop for one leg, as serveTransportManager's.
func (rig *adoptRig) serve(r *router, conn *emu.Conn) {
	buf := make([]byte, 1<<16+routing.PacketHeaderSize)
	for {
		select {
		case <-rig.stop:
			return
		default:
		}
		if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			return
		}
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue
			}
			return
		}
		if n < routing.PacketHeaderSize {
			continue
		}
		pkt := make(routing.Packet, n)
		copy(pkt, buf[:n])
		_ = r.handleTransportPacket(context.Background(), pkt) //nolint:errcheck // a dropped frame is the SACK layer's problem
	}
}

// anyLegReserve is the router's one live leg reserve, if it holds one.
func (r *router) anyLegReserve() *RouteGroup {
	r.mx.Lock()
	defer r.mx.Unlock()
	for _, nrg := range r.rgsNs {
		if nrg != nil && nrg.rg != nil && nrg.rg.legReserve && !nrg.rg.isClosed() {
			return nrg.rg
		}
	}
	return nil
}

// echo proves bytes cross from one conn to the other.
func echo(t *testing.T, from, to net.Conn, msg []byte) {
	t.Helper()
	_, err := from.Write(msg)
	require.NoError(t, err)
	require.NoError(t, to.SetReadDeadline(time.Now().Add(10*time.Second)))
	got := make([]byte, len(msg))
	_, err = io.ReadFull(to, got)
	require.NoError(t, err)
	require.True(t, bytes.Equal(msg, got), "the payload must arrive intact")
}

// TestAdoptLegReserveAsTunnel is the gate: the reserve's chain becomes a new
// tunnel on the dial's own descriptor, handshaked end to end and carrying data
// both ways, the reserve is gone at both edges, and the tunnel it was split out
// of never notices.
func TestAdoptLegReserveAsTunnel(t *testing.T) {
	rig := newAdoptRig(t)
	tpID := legTpID(t, rig.reserveA, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := rig.b.AcceptRoutes(ctx)
		if err != nil {
			t.Errorf("exit accept: %v", err)
		}
		accepted <- conn
	}()

	conn, err := rig.a.DialRoutes(ctx, rig.bPK, adoptNewPort, adoptServicePort, &DialOptions{
		AdoptReservePort: rig.reservePrt, TunnelRole: tunnelRoleActive, AppName: "skysocks-client", MuxRoutes: 1,
	})
	require.NoError(t, err, "adopting the leg reserve")
	var exit net.Conn
	select {
	case exit = <-accepted:
	case <-ctx.Done():
		t.Fatal("the exit never accepted the adopted tunnel")
	}
	require.NotNil(t, exit)

	nrg, ok := conn.(*NoiseRouteGroup)
	require.True(t, ok, "an adoption returns the same conn type a dial does")
	require.True(t, nrg.rg.encrypt, "the adopted tunnel must have completed the noise handshake")
	require.False(t, nrg.rg.legReserve, "the adopted group is a tunnel, not a reserve")
	require.Equal(t, adoptNewPort, nrg.rg.desc.DstPort(), "the tunnel lives on the dial's fresh local port")
	require.Equal(t, tpID, legTpID(t, nrg.rg, 0), "the tunnel rides the reserve's own chain, not a new one")
	exitNrg, ok := exit.(*NoiseRouteGroup)
	require.True(t, ok)
	require.Equal(t, adoptServicePort, exitNrg.rg.desc.DstPort(), "the exit delivers it on the service port")
	require.Equal(t, tpID, legTpID(t, exitNrg.rg, 0))

	echo(t, conn, exit, []byte("up over the adopted tunnel"))
	echo(t, exit, conn, []byte("down over the adopted tunnel"))

	require.Nil(t, rig.a.anyLegReserve(), "the reserve must be gone at the initiator")
	require.Nil(t, rig.b.anyLegReserve(), "the reserve must be gone at the exit")
	require.True(t, rig.reserveA.isClosed() || legCount(rig.reserveA) == 0, "the emptied reserve holds nothing")

	// The tunnel the reserve was split out of carries on, untouched.
	require.Equal(t, 1, legCount(rig.activeA))
	require.False(t, rig.activeA.isClosed())
	echo(t, rig.activeA, rig.activeB, []byte("the old tunnel still carries"))
}

// TestAdoptLegReserveNoAckLeavesItIntact is the fallback: an exit that never
// answers (a cut chain, an old build dropping the frame) must leave both the
// reserve and the tunnel exactly as they were, and the dial must fail so the app
// dials the usual way.
func TestAdoptLegReserveNoAckLeavesItIntact(t *testing.T) {
	require.NoError(t, routersettings.SetValue(routersettings.LegRehomeAckTimeout.Name(), int64(300*time.Millisecond)))
	t.Cleanup(func() {
		require.NoError(t, routersettings.SetValue(routersettings.LegRehomeAckTimeout.Name(), int64(5*time.Second)))
	})
	rig := newAdoptRig(t)
	reserveB := rig.b.anyLegReserve()
	rig.connsA[1].Egress().Cut()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := rig.a.DialRoutes(ctx, rig.bPK, adoptNewPort, adoptServicePort, &DialOptions{
		AdoptReservePort: rig.reservePrt, TunnelRole: tunnelRoleActive, MuxRoutes: 1,
	})
	require.ErrorIs(t, err, ErrRehomeNoAck)

	require.Same(t, rig.reserveA, rig.a.anyLegReserve(), "the initiator keeps its reserve")
	require.Same(t, reserveB, rig.b.anyLegReserve(), "the exit keeps its reserve")
	require.Equal(t, 1, legCount(rig.reserveA))
	require.Equal(t, 1, legCount(reserveB))
	rule, err := rig.a.rt.Rule(rig.reserveA.rvs[0].KeyRouteID())
	require.NoError(t, err)
	require.Equal(t, rig.reserveA.desc, rule.RouteDescriptor(), "the reserve's consume rule is not rewritten")
	require.Nil(t, rig.a.rehomeGroupFor(rig.reserveA.adoptDesc(adoptNewPort, adoptServicePort)), "no half-built tunnel")

	echo(t, rig.activeA, rig.activeB, []byte("the old tunnel still carries"))
}

// TestAdoptRequiresALegReserve: a dial naming a port no reserve holds fails
// fast, before anything is sent.
func TestAdoptRequiresALegReserve(t *testing.T) {
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, _ := cipher.GenerateKeyPair()
	r := newAdoptRouter(t, aPK, aSK)
	_, err := r.DialRoutes(context.Background(), bPK, adoptNewPort, adoptServicePort, &DialOptions{AdoptReservePort: 20000})
	require.ErrorIs(t, err, ErrAdoptUnsupported)
}
