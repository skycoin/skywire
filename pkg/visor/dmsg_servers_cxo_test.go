package visor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
)

func TestDecodeServerLeaf(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	body, err := json.Marshal(&dmsgdisc.Entry{Static: pk, Server: &dmsgdisc.Server{Address: "1.2.3.4:80"}})
	require.NoError(t, err)
	e := decodeServerLeaf(cxoutils.FrameGzip(serverLeafVersion, body))
	require.NotNil(t, e)
	require.Equal(t, pk, e.Static)
	require.Equal(t, "1.2.3.4:80", e.Server.Address)

	require.Nil(t, decodeServerLeaf(cxoutils.FrameGzip(serverLeafVersion+1, body)), "an unknown version is skipped")
	require.Nil(t, decodeServerLeaf(cxoutils.FrameGzip(serverLeafVersion, []byte(`[]`))), "a clients batch is not a server")
	require.Equal(t, "clients-by-server/servers/", serverLeafPrefix)
}
