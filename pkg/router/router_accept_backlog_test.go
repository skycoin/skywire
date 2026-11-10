package router

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestSilentSetupsNeverBlockOrdinaryAccepts: setups whose initiators never
// handshake are queued ahead of a live one, which must still be accepted at once.
func TestSilentSetupsNeverBlockOrdinaryAccepts(t *testing.T) {
	rig := newAdoptRig(t)
	tp := legTpID(t, rig.activeA, 0)
	edge := func(port routing.Port, id routing.RouteID) (a, b routing.EdgeRules) {
		aF, aC, bF, bC := id, id+1, id+2, id+3
		a = routing.EdgeRules{Desc: routing.NewRouteDescriptor(rig.bPK, rig.aPK, adoptServicePort, port),
			Forward: routing.ForwardRule(DefaultRouteKeepAlive, aF, bC, tp, rig.bPK, rig.aPK, adoptServicePort, port),
			Reverse: routing.ConsumeRule(DefaultRouteKeepAlive, aC, rig.bPK, rig.aPK, adoptServicePort, port)}
		b = routing.EdgeRules{Desc: routing.NewRouteDescriptor(rig.aPK, rig.bPK, port, adoptServicePort),
			Forward: routing.ForwardRule(DefaultRouteKeepAlive, bF, aC, tp, rig.aPK, rig.bPK, port, adoptServicePort),
			Reverse: routing.ConsumeRule(DefaultRouteKeepAlive, bC, rig.aPK, rig.bPK, port, adoptServicePort)}
		return a, b
	}

	// Three setups nobody will ever handshake for, queued first.
	for i := range 3 {
		_, dead := edge(routing.Port(51000+i), routing.RouteID(200+10*i)) //nolint:gosec
		rig.b.accept <- acceptEntry{rules: dead, at: time.Now()}
	}
	rulesA, rulesB := edge(50000, 100)
	require.NoError(t, rig.a.SaveRoutingRules(rulesA.Forward, rulesA.Reverse))
	rig.b.accept <- acceptEntry{rules: rulesB, at: time.Now()}

	ctx, cancel := context.WithTimeout(context.Background(), 4*handshakeAwaitTimeout)
	defer cancel()
	dialed := make(chan error, 1)
	go func() {
		_, err := rig.a.saveRouteGroupRules(ctx, rulesA, noise.Config{
			LocalPK: rig.a.conf.PubKey, LocalSK: rig.a.conf.SecKey, RemotePK: rig.bPK, Initiator: true,
		}, "", "", false)
		dialed <- err
	}()

	start := time.Now()
	conn, err := rig.b.AcceptRoutes(ctx)
	require.NoError(t, err, "the live route must be the first thing accepted")
	require.Less(t, time.Since(start), handshakeAwaitTimeout/2,
		"the live accept must not wait behind setups that never handshake")
	nrg, ok := conn.(*NoiseRouteGroup)
	require.True(t, ok)
	require.Equal(t, rulesB.Desc, nrg.rg.desc)
	require.NoError(t, <-dialed, "the live route's initiator completes its handshake")
}

// TestStaleSetupIsDropped: a setup queued for longer than its initiator waits
// is discarded without saving its rules or waiting for a handshake.
func TestStaleSetupIsDropped(t *testing.T) {
	r := newLegTestRouter(t)
	peer, local := legTestKeys()
	stale := r.makeAuxRules(t, routing.NewRouteDescriptor(peer, local, legTestSrcPort, legTestDstPort), 1)
	r.accept <- acceptEntry{rules: stale, at: time.Now().Add(-2 * handshakeAwaitTimeout)}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.AcceptRoutes(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded, "nothing is left to accept")
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, uint64(1), r.acceptStale.Load())
	_, err = r.rt.Rule(stale.Reverse.KeyRouteID())
	require.Error(t, err, "a dropped setup's rules are never saved")
	require.Zero(t, r.initializingCount())
}
