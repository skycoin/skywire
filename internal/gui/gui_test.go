//+build systray

package gui

import (
	"testing"

	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestGetAvailPublicVPNServers(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	common := &visorconfig.Common{
		Version: "v1.1.0",
		SK:      sk,
		PK:      pk,
	}
	config := visorconfig.MakeBaseConfig(common)
	servers := GetAvailPublicVPNServers(config)
	require.NotEqual(t, nil, servers)
	require.NotEqual(t, []string{}, servers)
	t.Logf("Servers: %v", servers)
}
