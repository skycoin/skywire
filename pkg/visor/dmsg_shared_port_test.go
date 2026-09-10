// Package visor pkg/visor/dmsg_shared_port_test.go c3-visor-core
package visor

import (
	"testing"

	"github.com/stretchr/testify/require"

	dmsgspec "github.com/skycoin/skywire/pkg/dmsg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// The in-process dmsg server shares the visor's transport TCP port by default,
// and steps aside the moment the operator says otherwise.
func TestDmsgServerSharesTransportPort(t *testing.T) {
	conf := func(s *dmsgspec.DmsgServerConfig) *visorconfig.V1 {
		return &visorconfig.V1{Dmsg: &dmsgspec.DmsgConfig{Server: s}}
	}

	require.True(t, dmsgServerSharesTransportPort(conf(&dmsgspec.DmsgServerConfig{Enabled: true})),
		"enabled on the visor key with no address of its own shares the port")

	require.False(t, dmsgServerSharesTransportPort(nil))
	require.False(t, dmsgServerSharesTransportPort(&visorconfig.V1{}), "no dmsg config")
	require.False(t, dmsgServerSharesTransportPort(conf(nil)), "no server block")
	require.False(t, dmsgServerSharesTransportPort(conf(&dmsgspec.DmsgServerConfig{})),
		"disabled server takes no branch, so none is installed for it")
	require.False(t, dmsgServerSharesTransportPort(conf(&dmsgspec.DmsgServerConfig{
		Enabled: true, LocalAddress: ":8081",
	})), "an operator-pinned address wins over the shared port")
	require.False(t, dmsgServerSharesTransportPort(conf(&dmsgspec.DmsgServerConfig{
		Enabled: true, ConfigPath: "/etc/skywire-dmsg.json",
	})), "a standalone config runs on its own key and its own addresses")
}
