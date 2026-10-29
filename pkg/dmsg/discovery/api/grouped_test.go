package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestClientsGroupedByServerReused(t *testing.T) {
	serverPK, _ := cipher.GenerateKeyPair()
	db := newTestStore(t)
	entry, _ := signedClientEntry(t, serverPK)
	require.NoError(t, db.SetEntry(context.Background(), entry, 0))
	api := newAPI(db)

	first := do(api, http.MethodGet, "/dmsg-discovery/servers/clients", nil).Body.String()
	second := do(api, http.MethodGet, "/dmsg-discovery/servers/clients", nil).Body.String()
	require.Equal(t, first, second)
	require.Contains(t, first, serverPK.Hex())
}

func TestClientsGroupedByServerCachedAllocs(t *testing.T) {
	serverPK, _ := cipher.GenerateKeyPair()
	db := newTestStore(t)
	for i := 0; i < 50; i++ {
		e, _ := signedClientEntry(t, serverPK)
		require.NoError(t, db.SetEntry(context.Background(), e, 0))
	}
	api := newAPI(db)
	entries, err := api.getCachedAllClientEntries(context.Background())
	require.NoError(t, err)
	rebuild := testing.AllocsPerRun(20, func() { api.byServerCache = nil; api.clientsGroupedByServer(entries) })
	cached := testing.AllocsPerRun(20, func() { api.clientsGroupedByServer(entries) })
	t.Logf("allocs per call: rebuild %.0f, cached %.0f", rebuild, cached)
	require.Zero(t, cached)
}
