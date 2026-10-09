package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

type mapListFetcher map[cipher.PubKey][]cipher.PubKey

func (m mapListFetcher) FetchTransportList(_ context.Context, pk cipher.PubKey) (*transport.SignedList, error) {
	remotes, ok := m[pk]
	if !ok {
		return nil, errors.New("no list")
	}
	l := &transport.SignedList{PK: pk}
	for _, r := range remotes {
		l.Entries = append(l.Entries, transport.CompactEntry{Remote: r, Type: tptypes.STCPR})
	}
	return l, nil
}

// Each 3-hop list miss is counted under its reason, and a hit under
// list_routes.
func TestListRoutes3HopCountsReasons(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	local := []oracleLocalTp{{id: uuid.New(), remotePK: a, tpType: tptypes.STCPR}}
	log := logging.MustGetLogger("test")
	r := &router{logger: log}
	run := func(local []oracleLocalTp, f mapListFetcher) error {
		_, _, err := r.listRoutes3HopFrom(context.Background(), log, f, src, dst, local, nil)
		return err
	}

	require.Error(t, run(nil, mapListFetcher{}))
	require.Error(t, run(local, mapListFetcher{}))
	require.Error(t, run(local, mapListFetcher{dst: {b}}))
	require.Error(t, run(local, mapListFetcher{dst: {b}, a: {other}}))
	require.NoError(t, run(local, mapListFetcher{dst: {b}, a: {b}}))

	st := r.RouteSourceStats()
	require.Equal(t, int64(1), st.ListNoFirstHop)
	require.Equal(t, int64(1), st.ListNoDstList)
	require.Equal(t, int64(1), st.ListNoNeighborList)
	require.Equal(t, int64(1), st.ListNoPath)
	require.Equal(t, int64(1), st.ListRoutes)
	require.Equal(t, int64(3), st.ListFetches)
	require.Equal(t, int64(1), st.ListFetchFails)
}

// A 3-hop candidate whose route just died young gives way to the next one,
// as the route finder's candidates do; with every candidate dead the dial
// still gets one rather than failing.
func TestListRoutes3HopSkipsDeadRoute(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	a1, _ := cipher.GenerateKeyPair()
	a2, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	local := []oracleLocalTp{
		{id: uuid.New(), remotePK: a1, tpType: tptypes.STCPR},
		{id: uuid.New(), remotePK: a2, tpType: tptypes.STCPR},
	}
	f := mapListFetcher{dst: {b}, a1: {b}, a2: {b}}
	lists := map[cipher.PubKey][]*transport.Entry{}
	for _, pk := range []cipher.PubKey{a1, a2} {
		l, err := f.FetchTransportList(context.Background(), pk)
		require.NoError(t, err)
		lists[pk] = l.Transports()
	}
	dl, err := f.FetchTransportList(context.Background(), dst)
	require.NoError(t, err)
	legs, err := compute3HopRoutes(src, dst, local, lists, dl.Transports(), nil)
	require.NoError(t, err)
	require.Len(t, legs, 2)

	log := logging.MustGetLogger("test")
	r := &router{logger: log, deadRoutes: newDeadRouteCache(time.Minute, time.Minute)}
	require.True(t, r.deadRoutes.mark(legs[0].Forward, time.Second, time.Now()))

	opts := &DialOptions{}
	fwd, _, err := r.listRoutes3HopFrom(context.Background(), log, f, src, dst, local, opts)
	require.NoError(t, err)
	require.Equal(t, legs[1].Forward[0].TpID, fwd[0].TpID, "the dead route's sibling is picked")

	require.True(t, r.deadRoutes.mark(legs[1].Forward, time.Second, time.Now()))
	_, _, err = r.listRoutes3HopFrom(context.Background(), log, f, src, dst, local, opts)
	require.NoError(t, err, "with every candidate dead the dial keeps one")
}
