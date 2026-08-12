package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestPruneLegByConsumeRule verifies the endpoint side of a remote leg-retire
// (routing.CloseLegRetired): the leg whose consume rule the close arrived on is
// dropped and its forward+consume rules reclaimed, while the route group and
// its other legs stay live. This is the Phase 1a reclamation path — it lets a
// rotated/retired mux leg free its rules immediately instead of waiting out the
// idle-rule GC.
func TestPruneLegByConsumeRule(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 3)

	// createMuxRouteGroup assigns leg i the consume rule RouteID(i+100).
	// Retire the middle leg.
	const retiredConsumeID = routing.RouteID(1 + 100)

	rg.mu.Lock()
	before := rg.rt.Count()
	rg.mu.Unlock()
	require.Equal(t, 6, before, "3 legs => 3 forward + 3 consume rules")

	ok := rg.pruneLegByConsumeRule(retiredConsumeID)
	require.True(t, ok, "a non-last leg must be prunable")

	rg.mu.Lock()
	remaining := len(rg.tps)
	fwdN, rvsN := len(rg.fwd), len(rg.rvs)
	after := rg.rt.Count()
	rg.mu.Unlock()

	require.Equal(t, 2, remaining, "one leg retired, two remain")
	require.Equal(t, 2, fwdN, "forward rules compacted in lockstep")
	require.Equal(t, 2, rvsN, "consume rules compacted in lockstep")
	require.Equal(t, 4, after, "retired leg's forward+consume rules reclaimed")
	require.False(t, rg.isClosed(), "route group stays live after a single leg retire")

	// The retired consume rule must be gone; a surviving leg's rule must remain.
	_, err := rg.rt.Rule(retiredConsumeID)
	require.Error(t, err, "retired leg's consume rule should be deleted")
	_, err = rg.rt.Rule(routing.RouteID(0 + 100))
	require.NoError(t, err, "surviving leg's consume rule should remain")
}

// TestPruneLegByConsumeRuleLastLegFallsBack verifies that retiring the last
// remaining leg is refused (returns false) so the caller falls back to a normal
// whole-group close rather than leaving a legless route group.
func TestPruneLegByConsumeRuleLastLegFallsBack(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 1)

	ok := rg.pruneLegByConsumeRule(routing.RouteID(0 + 100))
	require.False(t, ok, "the last leg must not be prunable — caller does a full close")

	rg.mu.Lock()
	remaining := len(rg.tps)
	rg.mu.Unlock()
	require.Equal(t, 1, remaining, "last leg preserved")
	require.False(t, rg.isClosed())
}

// TestPruneLegByConsumeRuleUnknownRuleNoop verifies that a consume-rule ID that
// isn't part of this group is a no-op (returns false, group untouched) — a
// stray/duplicate close must not disturb live legs.
func TestPruneLegByConsumeRuleUnknownRuleNoop(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 3)

	ok := rg.pruneLegByConsumeRule(routing.RouteID(9999))
	require.False(t, ok)

	rg.mu.Lock()
	remaining := len(rg.tps)
	rg.mu.Unlock()
	require.Equal(t, 3, remaining, "unknown rule must not prune any leg")
}
