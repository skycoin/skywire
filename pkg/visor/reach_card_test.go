package visor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func reachTestVisor(t *testing.T, arLimit int) *Visor {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	v := &Visor{
		conf:   &visorconfig.V1{Common: &visorconfig.Common{PK: pk, SK: sk}, Transport: &visorconfig.Transport{ARTransportLimit: arLimit}},
		arSelf: newARSelfState(),
	}
	v.arSelf.set("stcpr", addrresolver.VisorData{RemoteAddr: "203.0.113.7:7773"})
	v.arSelf.stamp()
	return v
}

// The card is served exactly while the visor registers with the address
// resolver, and carries its own records signed by it.
func TestReachCardFollowsTheARLever(t *testing.T) {
	v := reachTestVisor(t, 0)
	body, err := v.ReachCardBody()
	require.NoError(t, err)
	var card addrresolver.ReachCard
	require.NoError(t, json.Unmarshal(body, &card))
	recs, err := card.Verify(v.conf.PK)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.7:7773", recs[types.STCPR].RemoteAddr)

	v.conf.Transport.ARTransportLimit = -1
	_, err = v.ReachCardBody()
	require.ErrorIs(t, err, logserver.ErrNotReachable)
	require.False(t, v.reachCardStats().Advertised)
}

// A held card answers its types; a peer that served none is not asked again
// while the miss is fresh, and the visor never asks itself.
func TestReachCardRecordUsesHeldCards(t *testing.T) {
	v := reachTestVisor(t, 0)
	ctx := context.Background()
	peer, _ := cipher.GenerateKeyPair()
	none, _ := cipher.GenerateKeyPair()
	v.reach.peers = map[cipher.PubKey]reachCardEntry{
		peer: {records: map[types.Type]addrresolver.VisorData{types.QUIC: {RemoteAddr: "198.51.100.2:9083"}}, at: time.Now()},
		none: {at: time.Now()},
	}
	d, ok := v.reachCardRecord(ctx, types.QUICLegacy, peer)
	require.True(t, ok)
	require.Equal(t, "198.51.100.2:9083", d.RemoteAddr)
	_, ok = v.reachCardRecord(ctx, types.STCPR, peer)
	require.False(t, ok, "a type the card lacks falls through to the resolver")
	_, ok = v.reachCardRecord(ctx, types.STCPR, none)
	require.False(t, ok)
	_, ok = v.reachCardRecord(ctx, types.STCPR, v.conf.PK)
	require.False(t, ok)
	require.Zero(t, v.reach.fetches.Load(), "nothing above needed a fetch")
	st := v.reachCardStats()
	require.Equal(t, 1, st.Held)
	require.EqualValues(t, 1, st.Hits)
}

// A bind whose addresses changed drops the visor's own card and records, so
// the next card is rebuilt; an unchanged re-bind keeps them.
func TestReachBindChangeRebuildsTheCard(t *testing.T) {
	v := reachTestVisor(t, 0)
	home := addrresolver.LocalAddresses{Port: "7773", Addresses: []string{"192.168.1.5"}}
	v.noteReachBind("stcpr", home)
	_, err := v.ReachCardBody()
	require.NoError(t, err)
	v.arSelf.stamp()

	v.noteReachBind("stcpr", home)
	require.NotNil(t, v.reach.own, "an unchanged re-bind keeps the card")
	require.Less(t, v.arSelf.age(), time.Minute)

	v.noteReachBind("stcpr", addrresolver.LocalAddresses{Port: "7773", Addresses: []string{"10.0.0.9"}})
	require.Nil(t, v.reach.own, "a new network drops the card")
	require.Greater(t, v.arSelf.age(), arSelfMaxAge, "and the records refresh before the next card")
}
