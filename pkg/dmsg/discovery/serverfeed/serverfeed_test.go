package serverfeed

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func TestEncodeDecode(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	body, err := Encode(&disc.Entry{Static: pk, Server: &disc.Server{Address: "1.2.3.4:80"}})
	require.NoError(t, err)
	e := Decode(body)
	require.NotNil(t, e)
	require.Equal(t, pk, e.Static)
	require.Equal(t, "1.2.3.4:80", e.Server.Address)

	require.Nil(t, Decode(cxoutils.FrameGzip(Version+1, []byte(`{}`))), "an unknown version is skipped")
	require.Nil(t, Decode(cxoutils.FrameGzip(Version, []byte(`[]`))), "a clients batch is not a server")
	require.Equal(t, "clients-by-server/servers/"+pk.Hex(), LeafPath(pk))
}

func TestWithoutSnapshot(t *testing.T) {
	_, ok := Servers(nil)
	require.False(t, ok)
	require.Empty(t, Delegated(nil))
}
