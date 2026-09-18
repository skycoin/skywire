// Package visor pkg/visor/api_mux_controls_test.go c3-vis-core
package visor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
)

// restoreDialKnobs puts every dial knob back when the test ends, so moving one
// here cannot leak into another package's tests running in the same binary.
func restoreDialKnobs(t *testing.T) {
	t.Helper()
	v := &Visor{log: logging.MustGetLogger("dial_settings_restore")}
	before, err := v.GetRouterDialSettings()
	require.NoError(t, err)
	t.Cleanup(func() {
		before.TunnelLegsSet = true
		before.TypePriorScaleSet = true
		before.ThroughputPriorScaleSet = true
		before.ClearPreferPKs = len(before.PreferPKs) == 0
		require.NoError(t, v.SetRouterDialSettings(before))
	})
}

func TestRouterDialSettingsRoundTrip(t *testing.T) {
	restoreDialKnobs(t)
	v := &Visor{log: logging.MustGetLogger("dial_settings_test")}

	cur, err := v.GetRouterDialSettings()
	require.NoError(t, err)
	require.Equal(t, router.DialRouteCandidates(), cur.RouteCandidates)
	require.Equal(t, router.DialUnknownLatencyCostMs(), cur.UnknownLatencyCostMs)
	require.Empty(t, cur.PreferPKs)

	pk := "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
	require.NoError(t, v.SetRouterDialSettings(RouterDialSettings{
		RouteCandidates:      7,
		MuxRouteHeadroom:     5,
		UnknownLatencyCostMs: 250,
		TunnelLegs:           3,
		TunnelLegsSet:        true,
		PreferPKs:            []string{pk},
	}))

	got, err := v.GetRouterDialSettings()
	require.NoError(t, err)
	require.Equal(t, 7, got.RouteCandidates)
	require.Equal(t, 5, got.MuxRouteHeadroom)
	require.Equal(t, 250.0, got.UnknownLatencyCostMs)
	require.Equal(t, 3, got.TunnelLegs)
	require.Equal(t, []string{pk}, got.PreferPKs)
	require.Equal(t, cur.WarmPlanBucketCap, got.WarmPlanBucketCap, "an unset field is left alone")

	// 0 is a meaningful value for the two scales, so it needs the explicit bit.
	require.NoError(t, v.SetRouterDialSettings(RouterDialSettings{TypePriorScale: 0, TypePriorScaleSet: true}))
	got, err = v.GetRouterDialSettings()
	require.NoError(t, err)
	require.Equal(t, 0.0, got.TypePriorScale)

	// And the prefer list can be cleared.
	require.NoError(t, v.SetRouterDialSettings(RouterDialSettings{ClearPreferPKs: true}))
	got, err = v.GetRouterDialSettings()
	require.NoError(t, err)
	require.Empty(t, got.PreferPKs)
}

func TestRouterDialSettingsRejectsBadValues(t *testing.T) {
	restoreDialKnobs(t)
	v := &Visor{log: logging.MustGetLogger("dial_settings_reject_test")}

	require.ErrorContains(t, v.SetRouterDialSettings(RouterDialSettings{
		TypePriorScale: -1, TypePriorScaleSet: true,
	}), "dial-type-prior-scale")

	require.ErrorContains(t, v.SetRouterDialSettings(RouterDialSettings{
		TunnelLegs: -5, TunnelLegsSet: true,
	}), "dial-tunnel-legs")

	require.ErrorContains(t, v.SetRouterDialSettings(RouterDialSettings{
		PreferPKs: []string{"not-a-public-key"},
	}), "invalid public key")
}

// Without a router, the per-leg controls say so rather than nil-panicking.
func TestMuxLegControlsNeedARouter(t *testing.T) {
	v := &Visor{log: logging.MustGetLogger("leg_controls_norouter_test")}
	_, err := v.MuxWeights("skysocks-client", 0)
	require.ErrorContains(t, err, "router not available")
	_, err = v.RouteGroupMuxNegotiated("skysocks-client")
	require.ErrorContains(t, err, "router not available")
	require.ErrorContains(t, v.AddMuxRouteForward("skysocks-client", nil, nil, 0), "router not available")
}
