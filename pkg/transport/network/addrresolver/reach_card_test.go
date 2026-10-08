package addrresolver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestReachCardVerifies(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	recs := map[types.Type]VisorData{
		types.STCPR:    {RemoteAddr: "203.0.113.7:7773", LocalAddresses: LocalAddresses{Port: "7773", Addresses: []string{"192.168.1.5"}}},
		types.WTLegacy: {RemoteAddr: "203.0.113.7:7774", LocalAddresses: LocalAddresses{CertHash: "ab"}},
	}
	card, err := NewReachCard(pk, sk, time.Now(), recs)
	require.NoError(t, err)
	b, err := json.Marshal(card)
	require.NoError(t, err)

	var got ReachCard
	require.NoError(t, json.Unmarshal(b, &got))
	out, err := got.Verify(pk)
	require.NoError(t, err)
	require.Equal(t, recs[types.STCPR], out[types.STCPR])
	require.Equal(t, "ab", out[types.WT].CertHash, "legacy type names come back normalized")

	other, _ := cipher.GenerateKeyPair()
	_, err = got.Verify(other)
	require.ErrorIs(t, err, ErrReachCardSig, "a card is only good for its owner")

	got.Records = json.RawMessage(`{"stcpr":{"remote_addr":"198.51.100.1:1"}}`)
	_, err = got.Verify(pk)
	require.ErrorIs(t, err, ErrReachCardSig, "an altered record fails the signature")
}
