package cliconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	tspec "github.com/skycoin/skywire/pkg/transport/spec"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A regen keeps the transports "tp add --persist" saved. A WebSocket one comes
// from WSPEERS and is kept only while WSPEERS still names it.
func TestRegenKeepsPersistentTransports(t *testing.T) {
	restoreRegen, restoreOld, restoreConf, restoreWS := isRegen, oldConfCache, conf, wsPeers
	t.Cleanup(func() { isRegen, oldConfCache, conf, wsPeers = restoreRegen, restoreOld, restoreConf, restoreWS })

	stcpr, _ := cipher.GenerateKeyPair()
	gone, _ := cipher.GenerateKeyPair()
	kept, _ := cipher.GenerateKeyPair()
	isRegen = true
	oldConfCache = &visorconfig.V1{PersistentTransports: []tspec.PersistentTransports{
		{PK: stcpr, NetType: tptypes.STCPR},
		{PK: gone, NetType: tptypes.WS},
		{PK: kept, NetType: tptypes.WS},
	}}
	conf = new(visorconfig.V1)
	wsPeers = kept.Hex() + "@ws://node.local:8000/tp/ws"

	keepPersistentTransports()
	configureWSPeers(logging.MustGetLogger("test"))
	require.Equal(t, []tspec.PersistentTransports{
		{PK: stcpr, NetType: tptypes.STCPR},
		{PK: kept, NetType: tptypes.WS},
	}, conf.PersistentTransports)
}
