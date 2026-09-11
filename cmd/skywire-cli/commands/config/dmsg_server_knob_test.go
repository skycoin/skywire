// Package config cmd/skywire-cli/commands/config/dmsg_server_knob_test.go c0-cli
package cliconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsgc"
)

// applyDmsgServer mirrors the generator's decision so the two modes can be
// checked without building a whole config.
func applyDmsgServer(confPath string, ownKey bool, public string) *dmsgc.DmsgServerConfig {
	if confPath != "" {
		return &dmsgc.DmsgServerConfig{Enabled: true, ConfigPath: confPath}
	}
	if ownKey {
		return &dmsgc.DmsgServerConfig{Enabled: true, PublicAddress: public}
	}
	return nil
}

// The two modes are distinct and must not be confused: a config-path server
// keeps its own key and its own ports, while the own-key server shares the
// visor's identity and, with no local address, its transport port.
func TestDmsgServerModes(t *testing.T) {
	require.Nil(t, applyDmsgServer("", false, ""), "off by default")

	own := applyDmsgServer("", true, "1.2.3.4:30084")
	require.NotNil(t, own)
	require.True(t, own.Enabled)
	require.Empty(t, own.ConfigPath, "the own-key server has no standalone config")
	require.Empty(t, own.LocalAddress, "empty local address is what shares the transport port")
	require.Equal(t, "1.2.3.4:30084", own.PublicAddress)

	sep := applyDmsgServer("/etc/skywire-dmsg.json", false, "")
	require.NotNil(t, sep)
	require.Equal(t, "/etc/skywire-dmsg.json", sep.ConfigPath)

	// A config path wins, so an operator who sets both does not silently get a
	// second server on a different key.
	both := applyDmsgServer("/etc/skywire-dmsg.json", true, "1.2.3.4:30084")
	require.Equal(t, "/etc/skywire-dmsg.json", both.ConfigPath)
	require.Empty(t, both.PublicAddress)
}
