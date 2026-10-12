package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
)

func TestCollectCharts(t *testing.T) {
	ctx := context.Background()
	db := store.NewMock()
	a := newAPI(db)

	srvA, _ := cipher.GenerateKeyPair()
	srvB, _ := cipher.GenerateKeyPair()
	lanSrv, _ := cipher.GenerateKeyPair()
	require.NoError(t, db.SetEntry(ctx, &disc.Entry{Static: srvA, Server: &disc.Server{Address: "1.1.1.1:8081", AvailableSessions: 10}}, 0))
	require.NoError(t, db.SetEntry(ctx, &disc.Entry{Static: srvB, Server: &disc.Server{Address: "2.2.2.2:8081"}}, 0))
	for i, typ := range []string{"visor", "visor", ""} {
		pk, _ := cipher.GenerateKeyPair()
		delegated := []cipher.PubKey{srvA}
		if i == 0 {
			delegated = append(delegated, srvB)
		}
		if i == 2 {
			delegated = append(delegated, lanSrv)
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
	require.Equal(t, 1.0, v[chartServerClients+lanSrv.Hex()])
	require.Equal(t, 10.0, v[chartServerFree+srvA.Hex()])
	require.Equal(t, 0.0, v[chartServerFree+srvB.Hex()])
	require.Equal(t, 1.0, v[chartLANServers])
	require.Equal(t, 1.0, v[chartLANClients])
	_, hasLANFree := v[chartServerFree+lanSrv.Hex()]
	require.False(t, hasLANFree)
}

// Each server and LAN server lists the version from its discovery entry.
func TestBuildChartsVersions(t *testing.T) {
	ctx := context.Background()
	db := store.NewMock()
	a := newAPI(db)
	srv, _ := cipher.GenerateKeyPair()
	lanSrv, lanSK := cipher.GenerateKeyPair()
	client, _ := cipher.GenerateKeyPair()
	require.NoError(t, db.SetEntry(ctx, &disc.Entry{Static: srv, Version: "v1.3.100", Server: &disc.Server{Address: "1.1.1.1:8081"}}, 0))
	lanEntry := &disc.Entry{Static: lanSrv, Version: "v1.3.99", ClientType: "visor", Client: &disc.Client{}}
	require.NoError(t, lanEntry.Sign(lanSK))
	require.NoError(t, db.SetEntry(ctx, lanEntry, 0))
	require.NoError(t, db.SetEntry(ctx, &disc.Entry{Static: client, ClientType: "visor", Client: &disc.Client{DelegatedServers: []cipher.PubKey{srv, lanSrv}}}, 0))

	v, err := a.collectCharts(ctx)
	require.NoError(t, err)
	st := charts.NewMemoryStore()
	now := time.Now().UTC()
	require.NoError(t, st.Add(ctx, charts.Sample{At: now.Add(-time.Minute), V: v}))
	out, err := a.buildCharts(ctx, st, charts.Ranges[0], now)
	require.NoError(t, err)

	var found bool
	for _, c := range out.Charts {
		if c.Title == "Clients per registered dmsg server" {
			require.Equal(t, "Version", c.Legend[1])
			require.Equal(t, srv.Hex(), c.Series[0].Name)
			require.Equal(t, "v1.3.100", c.Series[0].Cells[0])
			found = true
		}
	}
	require.True(t, found)
	lan := out.Tables[len(out.Tables)-1]
	require.Equal(t, []string{"Public key", "Version", "Clients"}, lan.Head)
	require.Equal(t, []string{lanSrv.Hex(), "v1.3.99", "1"}, lan.Rows[0])
}
