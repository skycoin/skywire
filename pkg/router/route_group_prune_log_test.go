// Package router pkg/router/route_group_prune_log_test.go c2-net-routing
package router

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestPruneDeadTransportsCountsLegs checks the prune log counts legs. A
// forward-only leg holds one rule and is still one leg.
func TestPruneDeadTransportsCountsLegs(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)
	var out bytes.Buffer
	ml := logging.NewMasterLogger()
	ml.SetOutput(&out)
	rg.logger = ml.PackageLogger("prune")
	closeManagedTransport(mts[1], conns[1])

	rg.mu.Lock()
	rg.legs[1].rvs = nil
	dropped := rg.pruneDeadTransports()
	rg.mu.Unlock()

	require.Equal(t, []int{1}, dropped)
	require.Contains(t, out.String(), "2 alive, 1 removed")
}
