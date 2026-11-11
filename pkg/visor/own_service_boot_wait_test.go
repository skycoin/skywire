// Package visor pkg/visor/own_service_boot_wait_test.go c3-vis-core
package visor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A service the visor runs under a key of its own is one it hosts. The boot
// does not wait for such a service, which is reachable only once it is up.
func TestHostsOwnKeyService(t *testing.T) {
	visorPK, visorSK := cipher.GenerateKeyPair()
	svcPK, svcSK := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()
	raw, err := json.Marshal(map[string]string{"type": "test-own-key", "secret_key": svcSK.Hex()})
	require.NoError(t, err)
	var b services.Block
	require.NoError(t, json.Unmarshal(raw, &b))

	common := &visorconfig.Common{PK: visorPK, SK: visorSK}
	common.SetLogger(logging.NewMasterLogger())
	v := &Visor{conf: &visorconfig.V1{Common: common, EmbeddedServices: []services.Block{b}}}

	require.True(t, v.hostsOwnKeyService(svcPK))
	require.False(t, v.hostsOwnKeyService(otherPK))
	require.False(t, v.hostsOwnKeyService(visorPK))
	require.False(t, v.hostsOwnKeyService(cipher.PubKey{}))
}

// The dmsg server a visor runs from a config file has that file's key. When it
// is the only server listed, the client cannot be ready before it starts.
func TestOwnFileServerOnly(t *testing.T) {
	srvPK, srvSK := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()
	path := filepath.Join(t.TempDir(), "dmsg-server.json")
	raw, err := json.Marshal(map[string]string{"public_key": srvPK.Hex(), "secret_key": srvSK.Hex()})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	entry := func(pk cipher.PubKey) *disc.Entry {
		return &disc.Entry{Static: pk, Server: &disc.Server{Address: "127.0.0.1:1"}}
	}
	fromFile := &dmsgc.DmsgServerConfig{Enabled: true, ConfigPath: path}

	require.True(t, ownFileServerOnly(&dmsgc.DmsgConfig{Servers: []*disc.Entry{entry(srvPK)}, Server: fromFile}))
	require.False(t, ownFileServerOnly(&dmsgc.DmsgConfig{Servers: []*disc.Entry{entry(srvPK), entry(otherPK)}, Server: fromFile}),
		"another server can make the client ready")
	require.False(t, ownFileServerOnly(&dmsgc.DmsgConfig{Server: fromFile}), "no server listed")
	require.False(t, ownFileServerOnly(&dmsgc.DmsgConfig{Servers: []*disc.Entry{entry(srvPK)}}), "no server run here")
	require.False(t, ownFileServerOnly(&dmsgc.DmsgConfig{Servers: []*disc.Entry{entry(srvPK)},
		Server: &dmsgc.DmsgServerConfig{Enabled: true, ConfigPath: filepath.Join(t.TempDir(), "missing.json")}}))
}
