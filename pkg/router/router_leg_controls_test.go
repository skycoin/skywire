//go:build !tinygo || (js && wasm)

// Package router pkg/router/router_leg_controls_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// legControlRouter wires a mux'd route group into a bare router, the way the
// existing mux-ops tests do, so the per-leg controls can be exercised without a
// setup node.
func legControlRouter(t *testing.T, legs int) (*router, *RouteGroup, []*transport.ManagedTransport) {
	t.Helper()
	rg, mts, _ := createMuxRouteGroup(t, legs)
	r := &router{
		logger: logging.MustGetLogger("leg_controls_test"),
		rt:     rg.rt,
		rgsNs:  make(map[routing.RouteDescriptor]*NoiseRouteGroup),
	}
	r.rgsNs[rg.desc] = &NoiseRouteGroup{rg: rg, Conn: rg}
	return r, rg, mts
}

// The weights an operator gives ARE the spread: named legs get their share,
// unnamed legs get nothing, and the scheduler switches to the explicit mode.
func TestSetMuxExplicitWeights(t *testing.T) {
	r, rg, mts := legControlRouter(t, 3)

	view, err := r.SetMuxExplicitWeights(rg.desc, map[uuid.UUID]float64{
		mts[0].Entry.ID: 3,
		mts[1].Entry.ID: 1,
	})
	require.NoError(t, err)
	require.Equal(t, "weighted", view.Mode, "setting weights installs WeightModeExplicit")
	require.Equal(t, WeightModeExplicit, rg.mux.tpSelector.Mode())

	byTp := make(map[string]float64, len(view.Weights))
	for _, w := range view.Weights {
		byTp[w.TransportID] = w.Weight
	}
	require.Equal(t, 3.0, byTp[mts[0].Entry.ID.String()])
	require.Equal(t, 1.0, byTp[mts[1].Entry.ID.String()])
	require.Equal(t, 0.0, byTp[mts[2].Entry.ID.String()], "a leg the operator did not name gets no share of its own")

	// The schedule carries the stated 3:1 ratio, and the unnamed leg keeps the
	// selector's one-slot anti-starvation floor rather than vanishing.
	slots := map[int]int{}
	for _, idx := range rg.mux.tpSelector.schedule {
		slots[idx]++
	}
	require.Equal(t, 3, slots[0])
	require.Equal(t, 1, slots[1])
	require.Equal(t, 1, slots[2], "no live leg is ever fully starved; it must stay measured")
}

// A typo'd transport id is refused rather than silently weighting nothing, and
// an all-zero list is refused because it would leave the scheduler nothing.
func TestSetMuxExplicitWeightsRefusals(t *testing.T) {
	r, rg, mts := legControlRouter(t, 2)

	_, err := r.SetMuxExplicitWeights(rg.desc, map[uuid.UUID]float64{uuid.New(): 1})
	require.ErrorContains(t, err, "not legs of this route group")

	_, err = r.SetMuxExplicitWeights(rg.desc, map[uuid.UUID]float64{mts[0].Entry.ID: 0})
	require.ErrorContains(t, err, "sum to zero")

	_, err = r.SetMuxExplicitWeights(rg.desc, map[uuid.UUID]float64{mts[0].Entry.ID: -1})
	require.ErrorContains(t, err, "negative")

	_, err = r.SetMuxExplicitWeights(rg.desc, nil)
	require.ErrorContains(t, err, "no weights given")

	// An unknown descriptor is named, not ignored.
	var other routing.RouteDescriptor
	_, err = r.SetMuxExplicitWeights(other, map[uuid.UUID]float64{mts[0].Entry.ID: 1})
	require.ErrorContains(t, err, "no active route group")

	require.Equal(t, WeightModeECF, rg.mux.tpSelector.Mode(), "a refused call must not change the mode")
}

// --auto releases the pin and returns the group to ECF, the mode every mux is
// built with.
func TestClearMuxExplicitWeightsReturnsToECF(t *testing.T) {
	r, rg, mts := legControlRouter(t, 2)

	_, err := r.SetMuxExplicitWeights(rg.desc, map[uuid.UUID]float64{mts[0].Entry.ID: 1, mts[1].Entry.ID: 1})
	require.NoError(t, err)
	require.Equal(t, WeightModeExplicit, rg.mux.tpSelector.Mode())

	view, err := r.ClearMuxExplicitWeights(rg.desc)
	require.NoError(t, err)
	require.Equal(t, "ecf", view.Mode)
	require.Equal(t, WeightModeECF, rg.mux.tpSelector.Mode())
}

// Reading the weights back without changing them reports the live mode.
func TestMuxWeightsForReadsWithoutChanging(t *testing.T) {
	r, rg, _ := legControlRouter(t, 2)

	view, err := r.MuxWeightsFor(rg.desc)
	require.NoError(t, err)
	require.Equal(t, "ecf", view.Mode)
	require.Empty(t, view.Weights, "no explicit weights installed yet")
	require.Equal(t, uint16(rg.desc.DstPort()), view.DstPort) //nolint:gosec // test value
	require.Equal(t, WeightModeECF, rg.mux.tpSelector.Mode(), "a read must not change the mode")
}

// The forward-only add goes through the SAME validation chokepoint as the
// full-duplex one — the asymmetry is only in which rules are kept — so the
// gates that protect the mux invariants protect it too.
func TestAddMuxRouteByHopsForwardSharesTheGates(t *testing.T) {
	r, rg, _ := legControlRouter(t, 2)

	lPK := rg.desc.DstPK()
	rPK := rg.desc.SrcPK()
	good := []routing.Hop{{From: lPK, To: rPK, TpID: uuid.New()}}

	require.ErrorContains(t, r.AddMuxRouteByHopsForward(rg.desc, nil, good),
		"empty forward or reverse path")
	require.ErrorContains(t, r.AddMuxRouteByHopsForward(rg.desc, good, nil),
		"empty forward or reverse path")

	wrongStart := []routing.Hop{{From: cipher.PubKey{9}, To: rPK, TpID: uuid.New()}}
	require.ErrorContains(t, r.AddMuxRouteByHopsForward(rg.desc, wrongStart, good),
		"forward path must start at this visor")

	wrongEnd := []routing.Hop{{From: lPK, To: cipher.PubKey{9}, TpID: uuid.New()}}
	require.ErrorContains(t, r.AddMuxRouteByHopsForward(rg.desc, wrongEnd, good),
		"forward path must end at peer")

	var other routing.RouteDescriptor
	require.ErrorContains(t, r.AddMuxRouteByHopsForward(other, good, good),
		"no active route group")
}

// The negotiated view reports the caps the group carries and the send-window
// shape in force, per group.
func TestMuxNegotiatedForApp(t *testing.T) {
	r, rg, _ := legControlRouter(t, 2)
	rg.SetAppName("skysocks-client")

	out := r.MuxNegotiatedForApp("skysocks-client")
	require.Len(t, out, 1)
	n := out[0]
	require.True(t, n.MuxEnabled)
	require.Equal(t, 2, n.Legs)
	require.Equal(t, "ecf", n.Distribution)
	require.Equal(t, EcfMaxWindowBytes(), n.EcfMaxWindowBytes)
	require.Equal(t, EcfMinWindowBytes(), n.EcfMinWindowBytes)
	require.Equal(t, EcfWindowMargin(), n.EcfWindowMargin)
	require.Equal(t, SendWindowWaitMax().String(), n.SendWindowWaitMax)
	require.Len(t, n.PerLegWindowBytes, 2)

	require.Empty(t, r.MuxNegotiatedForApp("some-other-app"))
}
