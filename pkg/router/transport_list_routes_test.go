// Package router pkg/router/transport_list_routes_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func tpEntry(a, b cipher.PubKey, typ tptypes.Type) *transport.Entry {
	e := transport.MakeEntry(a, b, typ, transport.LabelAutomatic)
	return &e
}

// src reaches A1 and A2. A1 lists A1-B1 and B1 lists B1-dst, so the route is
// src -> A1 -> B1 -> dst. A2 lists nothing that reaches dst's peers.
func TestCompute3HopRoutes(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	a1, _ := cipher.GenerateKeyPair()
	a2, _ := cipher.GenerateKeyPair()
	b1, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	local := []oracleLocalTp{mkLocal(src, a1, tptypes.STCPR), mkLocal(src, a2, tptypes.STCPR)}
	lists := map[cipher.PubKey][]*transport.Entry{
		a1: {tpEntry(a1, b1, tptypes.SUDPH), tpEntry(a1, src, tptypes.STCPR)},
		a2: {tpEntry(a2, other, tptypes.STCPR)},
	}
	dstEntries := []*transport.Entry{tpEntry(b1, dst, tptypes.STCPR)}

	legs, err := compute3HopRoutes(src, dst, local, lists, dstEntries, nil)
	require.NoError(t, err)
	require.Len(t, legs, 1)
	fwd := legs[0].Forward
	require.Len(t, fwd, 3)
	require.Equal(t, []cipher.PubKey{src, a1, b1}, []cipher.PubKey{fwd[0].From, fwd[1].From, fwd[2].From})
	require.Equal(t, dst, fwd[2].To)
	require.Equal(t, transport.MakeTransportID(a1, b1, tptypes.SUDPH), fwd[1].TpID)
	require.Equal(t, transport.MakeTransportID(b1, dst, tptypes.STCPR), fwd[2].TpID)
	require.Equal(t, dst, legs[0].Reverse[0].From)

	// An excluded intermediate in either middle position removes the route.
	_, err = compute3HopRoutes(src, dst, local, lists, dstEntries, &DialOptions{ExcludeIntermediatePKs: []cipher.PubKey{b1}})
	require.Error(t, err)
	_, err = compute3HopRoutes(src, dst, local, lists, dstEntries, &DialOptions{ExcludeIntermediatePKs: []cipher.PubKey{a1}})
	require.Error(t, err)

	// No neighbor reaches dst's peers.
	_, err = compute3HopRoutes(src, dst, local, map[cipher.PubKey][]*transport.Entry{a2: lists[a2]}, dstEntries, nil)
	require.Error(t, err)
}
