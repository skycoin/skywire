package visor

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// A peer that already has a sudph transport must still be a target for the
// stcpr and squicr phases; only a transport of the SAME type removes it.
func TestFilterDuplicatesOfType(t *testing.T) {
	me, _ := cipher.GenerateKeyPair()
	withSUDPH, _ := cipher.GenerateKeyPair()
	withSTCPR, _ := cipher.GenerateKeyPair()
	bare, _ := cipher.GenerateKeyPair()

	mk := func(remote cipher.PubKey, ty tptypes.Type) *transport.ManagedTransport {
		mt := transport.NewManagedTransportForTest(nil)
		mt.Entry = transport.Entry{ID: uuid.New(), Type: ty, Edges: [2]cipher.PubKey{me, remote}}
		return mt
	}
	trs := []*transport.ManagedTransport{mk(withSUDPH, tptypes.SUDPH), mk(withSTCPR, tptypes.STCPR)}
	pks := []cipher.PubKey{withSUDPH, withSTCPR, bare}
	a := &autoconnector{}

	require.ElementsMatch(t, []cipher.PubKey{withSUDPH, bare}, a.filterDuplicatesOfType(pks, tptypes.STCPR, trs))
	require.ElementsMatch(t, []cipher.PubKey{withSTCPR, bare}, a.filterDuplicatesOfType(pks, tptypes.SUDPH, trs))
	require.ElementsMatch(t, pks, a.filterDuplicatesOfType(pks, tptypes.QUIC, trs))
	require.ElementsMatch(t, []cipher.PubKey{bare}, a.filterDuplicates(pks, trs))
}
