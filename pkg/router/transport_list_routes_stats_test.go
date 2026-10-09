package router

import (
	"context"
	"errors"
	"testing"

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
