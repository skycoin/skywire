package dmsg

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A visor holding relay sessions reported them as dmsg servers: 28 "servers"
// where discovery listed 9.
func TestConnectedServersPK_LeavesOutRelaySessions(t *testing.T) {
	serverPK, _ := cipher.GenerateKeyPair()
	relayPK, _ := cipher.GenerateKeyPair()
	ce := &Client{}
	ce.sessionsMx = new(sync.Mutex)
	ce.sessions = map[cipher.PubKey]*SessionCommon{
		serverPK: {rPK: serverPK, carrier: CarrierTCP},
		relayPK:  {rPK: relayPK, carrier: CarrierSkynet},
	}

	require.Equal(t, []string{serverPK.String()}, ce.ConnectedServersPK())
	require.Equal(t, []string{relayPK.String()}, ce.ConnectedRelaysPK())
}
