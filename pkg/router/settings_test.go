// Package router pkg/router/settings_test.go
package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Each runtime knob's default must be the constant the use site used to read:
// a visor that never calls `route settings` must behave exactly as it did.
func TestRouterSettingsDefaultsMatchConstants(t *testing.T) {
	t.Cleanup(resetRouterSettings)
	require.Equal(t, float64(ecfWindowMarginDefault), EcfWindowMargin())
	require.EqualValues(t, ecfMinWindowBytesDefault, EcfMinWindowBytes())
	require.EqualValues(t, ecfMaxWindowBytesDefault, EcfMaxWindowBytes())
	require.Equal(t, sendWindowWaitMaxDefault, SendWindowWaitMax())
	require.Equal(t, legParkMinHoldDefault, LegParkMinHold())
	require.Equal(t, deadRouteTTLDefault, DeadRouteHold())
	require.Equal(t, deadRouteMaxTTLDefault, DeadRouteHoldMax())
}

// The dead-route cache the router builds carries no window of its own, so it
// follows the knob on every death — that is what makes the hold live.
func TestDeadRouteHoldIsLive(t *testing.T) {
	t.Cleanup(resetRouterSettings)
	c := newDeadRouteCache(0, 0)
	ttl, max := c.hold()
	require.Equal(t, deadRouteTTLDefault, ttl)
	require.Equal(t, deadRouteMaxTTLDefault, max)

	require.True(t, SetDeadRouteHold(5*time.Second))
	require.True(t, SetDeadRouteHoldMax(30*time.Second))
	ttl, max = c.hold()
	require.Equal(t, 5*time.Second, ttl)
	require.Equal(t, 30*time.Second, max)

	// An explicit window (the tests own caches) ignores the knob.
	fixed := newDeadRouteCache(time.Minute, 4*time.Minute)
	ttl, max = fixed.hold()
	require.Equal(t, time.Minute, ttl)
	require.Equal(t, 4*time.Minute, max)

	require.False(t, SetDeadRouteHold(0))
	require.False(t, SetDeadRouteHoldMax(-time.Second))
	require.Equal(t, 5*time.Second, DeadRouteHold(), "a refused set leaves the value alone")
}

func TestRouterSettingsSetAndRefuseNonPositive(t *testing.T) {
	t.Cleanup(resetRouterSettings)

	require.True(t, SetEcfMaxWindowBytes(16<<20))
	require.EqualValues(t, 16<<20, EcfMaxWindowBytes())
	require.True(t, SetEcfMinWindowBytes(64<<10))
	require.EqualValues(t, 64<<10, EcfMinWindowBytes())
	require.True(t, SetEcfWindowMargin(1.5))
	require.Equal(t, 1.5, EcfWindowMargin())
	require.True(t, SetSendWindowWaitMax(100*time.Millisecond))
	require.Equal(t, 100*time.Millisecond, SendWindowWaitMax())
	require.True(t, SetLegParkMinHold(10*time.Second))
	require.Equal(t, 10*time.Second, LegParkMinHold())

	// A zero or negative knob would disable the clamp it belongs to, so it is
	// refused rather than stored.
	require.False(t, SetEcfMaxWindowBytes(0))
	require.False(t, SetEcfMinWindowBytes(-1))
	require.False(t, SetEcfWindowMargin(0))
	require.False(t, SetSendWindowWaitMax(0))
	require.False(t, SetLegParkMinHold(-time.Second))
	require.EqualValues(t, 16<<20, EcfMaxWindowBytes(), "a refused set leaves the value alone")
}

func resetRouterSettings() {
	SetEcfWindowMargin(ecfWindowMarginDefault)
	SetEcfMinWindowBytes(ecfMinWindowBytesDefault)
	SetEcfMaxWindowBytes(ecfMaxWindowBytesDefault)
	SetSendWindowWaitMax(sendWindowWaitMaxDefault)
	SetLegParkMinHold(legParkMinHoldDefault)
	SetDeadRouteHold(deadRouteTTLDefault)
	SetDeadRouteHoldMax(deadRouteMaxTTLDefault)
}
