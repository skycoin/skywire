// router_dial_diversify_candidates_test.go: how many routes a dial asks the
// route finder for. A mux dial sizes that off its leg count; a DIVERSIFY dial —
// a standby-pool fill — cannot, because it asks for one leg thirty-two times
// while excluding what it already holds, and the finder answers rank-ordered.
// Three candidates is then the same three candidates every time.
package router

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindRouteNumFor_DiversifyDialAsksForAWindow(t *testing.T) {
	t.Cleanup(func() { SetDialDiversifyCandidates(20) })

	// Mux off is the finder's own default for a PLAIN dial — but a diversify
	// dial wants alternatives whether or not it is muxing, so it still asks for
	// the window.
	require.Equal(t, uint16(0), findRouteNum(0), "plain dial: the finder's own default")
	require.Equal(t, uint16(20), findRouteNumFor(0, &DialOptions{
		DiversifyTransports:     true,
		RequireDisjointFirstHop: true,
	}), "a non-mux pool fill still needs alternatives to choose from")

	// The pool's own shape: one leg, both diversify flags set.
	pool := &DialOptions{DiversifyTransports: true, RequireDisjointFirstHop: true}
	require.Equal(t, uint16(baseRouteCandidates), findRouteNum(1),
		"the plain dial is unchanged")
	require.Equal(t, uint16(20), findRouteNumFor(1, pool),
		"a pool fill asks for the whole window, not the rank-ordered top 3")

	// Live knob.
	require.True(t, SetDialDiversifyCandidates(40))
	require.Equal(t, uint16(40), findRouteNumFor(1, pool))

	// A mux degree that already asks for more keeps its own number.
	require.True(t, SetDialDiversifyCandidates(5))
	require.Equal(t, uint16(12), findRouteNumFor(10, pool),
		"mux 10 + headroom 2 beats a 5-route window")
}

// Only a dial that set BOTH diversify flags gets the window: DiversifyTransports
// alone is a preference, and widening the request for every mux dial would put
// the extra route-finder work on dials that cannot use it.
func TestFindRouteNumFor_PlainDialsUnchanged(t *testing.T) {
	require.Equal(t, findRouteNum(1), findRouteNumFor(1, nil))
	require.Equal(t, uint16(0), findRouteNumFor(0, nil), "no opts: the sentinel survives")
	require.Equal(t, findRouteNum(1), findRouteNumFor(1, &DialOptions{}))
	require.Equal(t, findRouteNum(1), findRouteNumFor(1, &DialOptions{DiversifyTransports: true}),
		"diversify without RequireDisjointFirstHop is not a pool fill")
	require.Equal(t, findRouteNum(1), findRouteNumFor(1, &DialOptions{RequireDisjointFirstHop: true}))
}
