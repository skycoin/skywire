package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// newSweepTestRouter builds a minimal router with just the fields the GC
// sweep touches. It does NOT start any goroutines (we call rulesGC directly).
func newSweepTestRouter(t *testing.T) *router {
	t.Helper()
	l := logging.NewMasterLogger()
	return &router{
		logger: l.PackageLogger("router-gc-test"),
		conf:   &Config{RulesGCInterval: DefaultRulesGCInterval},
		rt:     routing.NewTable(l.PackageLogger("rt")),
		rgsNs:  make(map[routing.RouteDescriptor]*NoiseRouteGroup),
		rgsRaw: make(map[routing.RouteDescriptor]*RouteGroup),
		done:   make(chan struct{}),
	}
}

func newClosedRG(t *testing.T, rt routing.Table) *RouteGroup {
	t.Helper()
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	desc := routing.NewRouteDescriptor(pk1, pk2, 1, 2)
	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, desc, logging.NewMasterLogger())
	// Mark closed the same way the self-close paths ultimately do.
	rg.closedOnce.Do(func() { close(rg.closed) })
	return rg
}

// TestRulesGC_SweepsSelfClosedRouteGroups is a regression test for the rgsNs
// map leak: a route group that closes ITSELF (e.g. the keep-alive
// maxConsecutiveWriteFailures path) deletes its own rules from the routing
// table, so CollectGarbage never returns them and removeRouteGroupOfRule never
// pops the map entry. Before the sweep, the orphaned entry lived forever and
// accumulated under setup/teardown/failure churn. rulesGC must now drop it.
func TestRulesGC_SweepsSelfClosedRouteGroups(t *testing.T) {
	r := newSweepTestRouter(t)

	closedRG := newClosedRG(t, r.rt)
	r.rgsNs[closedRG.desc] = &NoiseRouteGroup{rg: closedRG, Conn: closedRG}

	// A still-alive rg must be retained.
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	aliveDesc := routing.NewRouteDescriptor(pk1, pk2, 3, 4)
	aliveRG := NewRouteGroup(DefaultRouteGroupConfig(), r.rt, aliveDesc, logging.NewMasterLogger())
	r.rgsNs[aliveDesc] = &NoiseRouteGroup{rg: aliveRG, Conn: aliveRG}

	// A self-closed raw (pre-noise-wrap) rg must also be swept.
	rawClosed := newClosedRG(t, r.rt)
	r.rgsRaw[rawClosed.desc] = rawClosed

	require.Len(t, r.rgsNs, 2)
	require.Len(t, r.rgsRaw, 1)

	r.rulesGC()

	require.Len(t, r.rgsNs, 1, "self-closed noise rg should be swept; alive rg retained")
	_, aliveStillThere := r.rgsNs[aliveDesc]
	require.True(t, aliveStillThere, "alive rg must not be swept")
	require.Len(t, r.rgsRaw, 0, "self-closed raw rg should be swept")
}
