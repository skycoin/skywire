// Package router pkg/router/router_mux_append_order_test.go c2-net-routing
package router

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// blockFirstAdd parks the first "added" leg-change callback until released, so
// a test can run a second append inside the first one.
type blockFirstAdd struct {
	blocked atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (h *blockFirstAdd) OnLegChange(_ DialInfo, _ []LegInfo, c LegChange) DistributionConfig {
	if c.Event == "added" {
		if h.blocked.CompareAndSwap(false, true) {
			close(h.entered)
			<-h.release
		}
	}
	return DistributionConfig{}
}

// makeCapturingAuxRules is makeAuxRules over a transport that records writes.
func (r *router) makeCapturingAuxRules(desc routing.RouteDescriptor, i int) (routing.EdgeRules, *capturingTransport) {
	peer := desc.SrcPK()
	local := desc.DstPK()

	tpID := uuid.New()
	conn := newCapturingTransport()
	mt := transport.NewManagedTransportForTest(conn)
	mt.Entry = transport.Entry{ID: tpID, Type: "test"}
	r.tm.InjectTransportForTest(mt)

	fwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(1000+i), routing.RouteID(2000+i), tpID, local, peer, legTestDstPort, legTestSrcPort) //nolint:gosec // G115: bounded small test route IDs
	rvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(3000+i), local, peer, legTestSrcPort, legTestDstPort)                                //nolint:gosec // G115: bounded small test route IDs
	return routing.EdgeRules{Desc: desc, Forward: fwd, Reverse: rvs}, conn
}

func handshakeRouteIDs(conn *capturingTransport) []routing.RouteID {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	var out []routing.RouteID
	for _, p := range conn.written {
		if p.Type() == routing.HandshakePacket {
			out = append(out, p.RouteID())
		}
	}
	return out
}

// TestConcurrentAuxAppendsKeepLegsPaired runs one aux append inside another and
// checks every leg keeps its own reverse rule and gets its own handshake.
func TestConcurrentAuxAppendsKeepLegsPaired(t *testing.T) {
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)
	nrg := &NoiseRouteGroup{rg: rg}

	hook := &blockFirstAdd{entered: make(chan struct{}), release: make(chan struct{})}
	rg.legChangeHook = hook

	legB, connB := r.makeCapturingAuxRules(desc, 1)
	legC, connC := r.makeCapturingAuxRules(desc, 2)

	done := make(chan error, 1)
	go func() { done <- r.appendRouteAsymmetric(nrg, legB, true, true) }()
	select {
	case <-hook.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("leg B's append never reached the leg-change hook")
	}
	require.NoError(t, r.appendRouteAsymmetric(nrg, legC, true, true))
	close(hook.release)
	require.NoError(t, <-done)

	rg.mu.Lock()
	require.Len(t, rg.legs, 3)
	require.Len(t, testRvs(rg), 3)
	wantRvs := map[uuid.UUID]routing.RouteID{
		legB.Forward.NextTransportID(): legB.Reverse.KeyRouteID(),
		legC.Forward.NextTransportID(): legC.Reverse.KeyRouteID(),
	}
	for i := 1; i < len(rg.legs); i++ {
		id := rg.legs[i].tp.Entry.ID
		require.Equalf(t, wantRvs[id], rg.legs[i].rvs.KeyRouteID(),
			"leg %d (transport %s) holds another leg's reverse rule", i, id)
	}
	rg.mu.Unlock()

	require.Equal(t, []routing.RouteID{legB.Forward.NextRouteID()}, handshakeRouteIDs(connB),
		"leg B's handshake must go out once, on leg B")
	require.Equal(t, []routing.RouteID{legC.Forward.NextRouteID()}, handshakeRouteIDs(connC),
		"leg C's handshake must go out once, on leg C")
}

// TestAsymmetricLegsKeepReverseRulesAligned checks that a forward-only leg and
// a reverse-only rule do not shift the reverse rules of the paired legs.
func TestAsymmetricLegsKeepReverseRulesAligned(t *testing.T) {
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)
	nrg := &NoiseRouteGroup{rg: rg}

	fwdOnly, _ := r.makeCapturingAuxRules(desc, 1)
	revOnly, _ := r.makeCapturingAuxRules(desc, 2)
	paired, _ := r.makeCapturingAuxRules(desc, 3)

	require.NoError(t, r.appendRouteAsymmetric(nrg, fwdOnly, true, false))
	require.NoError(t, r.appendRouteAsymmetric(nrg, revOnly, false, true))
	require.NoError(t, r.appendRouteAsymmetric(nrg, paired, true, true))

	require.Equal(t, 2, consumeLegIdx(rg, paired.Reverse.KeyRouteID()),
		"the paired leg is leg 2, after the forward-only leg")
	require.Equal(t, -1, consumeLegIdx(rg, revOnly.Reverse.KeyRouteID()),
		"a reverse-only rule belongs to no leg")

	// Pruning the forward-only leg must leave the others paired and must keep
	// the reverse-only rule.
	require.Equal(t, 1, rg.pruneDeadLegs([]uuid.UUID{fwdOnly.Forward.NextTransportID()}, "test", "test"))
	require.Equal(t, 1, consumeLegIdx(rg, paired.Reverse.KeyRouteID()))
	_, err := r.rt.Rule(paired.Reverse.KeyRouteID())
	require.NoError(t, err, "the paired leg's reverse rule must survive the prune")
	_, err = r.rt.Rule(revOnly.Reverse.KeyRouteID())
	require.NoError(t, err, "the reverse-only rule must survive the prune")

	rg.mu.Lock()
	tracked := false
	for _, rv := range testRvs(rg) {
		if rv != nil && rv.KeyRouteID() == revOnly.Reverse.KeyRouteID() {
			tracked = true
		}
	}
	rg.mu.Unlock()
	require.True(t, tracked, "the group must still hold the reverse-only rule so its close deletes it")
}

// consumeLegIdx is the index of the leg routeID arrives on, or -1.
func consumeLegIdx(rg *RouteGroup, routeID routing.RouteID) int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	return rg.legByConsumeRuleLocked(routeID)
}
