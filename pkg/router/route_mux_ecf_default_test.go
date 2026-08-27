package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestNewRouteMuxDefaultsToECF locks in that every mux defaults to the ECF
// scheduler, which only spills onto a slower leg once the fast leg is saturated
// — so multi-leg never over-assigns a slow leg and collapses below single-leg
// rate (the old latency-weighted default's failure mode).
func TestNewRouteMuxDefaultsToECF(t *testing.T) {
	m := newRouteMux(logging.NewMasterLogger().PackageLogger("mux"), true)
	require.Equal(t, WeightModeECF, m.tpSelector.Mode())
	// Cold ECF state (no SetECFState yet) must not panic and must fall back to
	// the schedule (leg 0) rather than erroring.
	require.Equal(t, 0, m.tpSelector.SelectECF(1200))
}
