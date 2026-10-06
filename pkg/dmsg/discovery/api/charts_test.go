package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
)

func TestCollectCharts(t *testing.T) {
	ctx := context.Background()
	db := store.NewMock()
	a := newAPI(db)

	srvA, _ := cipher.GenerateKeyPair()
	srvB, _ := cipher.GenerateKeyPair()
	require.NoError(t, db.SetEntry(ctx, &disc.Entry{Static: srvA, Server: &disc.Server{Address: "1.1.1.1:8081", AvailableSessions: 10}}, 0))
	require.NoError(t, db.SetEntry(ctx, &disc.Entry{Static: srvB, Server: &disc.Server{Address: "2.2.2.2:8081"}}, 0))
	for i, typ := range []string{"visor", "visor", ""} {
		pk, _ := cipher.GenerateKeyPair()
		delegated := []cipher.PubKey{srvA}
		if i == 0 {
			delegated = append(delegated, srvB)
		}
		require.NoError(t, db.SetEntry(ctx, &disc.Entry{Static: pk, ClientType: typ, Client: &disc.Client{DelegatedServers: delegated}}, 0))
	}

	v, err := a.collectCharts(ctx)
	require.NoError(t, err)
	require.Equal(t, 2.0, v[chartServers])
	require.Equal(t, 1.0, v[chartServersAvail])
	require.Equal(t, 3.0, v[chartClients])
	require.Equal(t, 2.0, v[chartVisorClients])
	require.Equal(t, 3.0, v[chartServerClients+srvA.Hex()])
	require.Equal(t, 1.0, v[chartServerClients+srvB.Hex()])
}
