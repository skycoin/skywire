// Package vpn pkg/vpn/tun_settings_test.go c4-app-vpn
package vpn

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTunSettingsTrackUpAndDown(t *testing.T) {
	start := CurrentTunSettings().Seq
	up := requestTun("192.168.255.6/29", "192.168.255.5", 1500, "1.1.1.1")
	require.True(t, up.Up)
	require.Equal(t, start+1, up.Seq)
	require.Equal(t, up, CurrentTunSettings())

	down, changed := requestTunDown()
	require.True(t, changed)
	require.False(t, down.Up)
	require.Empty(t, down.Addr)
	require.Equal(t, start+2, down.Seq)

	// The client drops both default halves: the second changes nothing.
	again, changed := requestTunDown()
	require.False(t, changed)
	require.Equal(t, down, again)
}
