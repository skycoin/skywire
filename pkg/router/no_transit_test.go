// Package router pkg/router/no_transit_test.go c2-net-router
package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/testhelpers"
)

// A visor with no_transit set refuses to be an intermediate hop, and says so
// through the setup node's own failure channel rather than silently dropping.
func TestRPCGateway_NoTransitRefusesIntermediaryRules(t *testing.T) {
	mlog := logging.NewMasterLogger()
	r := &MockRouter{}
	gw := NewRPCGateway(r, mlog, true)

	var ok bool
	err := gw.AddIntermediaryRules([]routing.Rule{{0, 0, 0}}, &ok)
	require.Error(t, err)
	require.False(t, ok)
	var f routing.Failure
	require.ErrorAs(t, err, &f)
	require.Equal(t, routing.FailureAddRules, f.Code)
	r.AssertNotCalled(t, "SaveRoutingRules")
}

// Without it, nothing changes: the rules are saved as before.
func TestRPCGateway_TransitAllowedByDefault(t *testing.T) {
	mlog := logging.NewMasterLogger()
	rule := routing.Rule{0, 0, 0}
	r := &MockRouter{}
	r.On("SaveRoutingRules", rule).Return(testhelpers.NoErr)
	gw := NewRPCGateway(r, mlog, false)

	var ok bool
	require.NoError(t, gw.AddIntermediaryRules([]routing.Rule{rule}, &ok))
	require.True(t, ok)
	r.AssertCalled(t, "SaveRoutingRules", rule)
}

// Refusing transit must not stop a route that ENDS here: edge rules arrive by
// a different RPC and are untouched.
func TestRPCGateway_NoTransitStillAcceptsEdgeRules(t *testing.T) {
	mlog := logging.NewMasterLogger()
	r := &MockRouter{}
	edge := routing.EdgeRules{Forward: routing.Rule{0, 0, 0}, Reverse: routing.Rule{1, 1, 1}}
	r.On("IntroduceRules", edge).Return(testhelpers.NoErr)
	gw := NewRPCGateway(r, mlog, true)

	var ok bool
	require.NoError(t, gw.AddEdgeRules(edge, &ok))
	require.True(t, ok)
	r.AssertCalled(t, "IntroduceRules", edge)
}

// The cascade path reaches a transit hop over the wire, so the same refusal
// applies there. A cascade handler is refusing when noTransit is set and the
// install is not an edge install; ProcessLocalOrigin (this visor's own rules
// as the SOURCE of its own route) shares processInstall and must keep working.
func TestCascadeHandler_RefusesTransitButNotEdges(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	transit := &routing.CascadeSetup{}
	edge := &routing.CascadeSetup{EdgeDesc: routing.NewRouteDescriptor(src, dst, 100, 110)}
	require.False(t, transit.IsEdge())
	require.True(t, edge.IsEdge())

	ch := &CascadeHandler{}
	ch.SetNoTransit(false)
	require.False(t, ch.refusesTransit(transit), "transit is allowed by default")
	require.False(t, ch.refusesTransit(edge))

	ch.SetNoTransit(true)
	require.True(t, ch.refusesTransit(transit), "a wire install that is not an edge is transit")
	require.False(t, ch.refusesTransit(edge), "a route that ENDS here is not transit")
}
