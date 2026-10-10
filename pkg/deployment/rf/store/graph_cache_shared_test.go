package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// A route finder in the same process as TPD builds its graph from TPD's
// live transport set, not from its own (here empty) store.
func TestGraphCacheSharesCoHostedTPD(t *testing.T) {
	url := os.Getenv("SKYWIRE_TEST_REDIS")
	if url == "" {
		t.Skip("SKYWIRE_TEST_REDIS unset; no redis to test against")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logging.MustGetLogger("test")
	tpd, err := tpdstore.New(ctx, storeconfig.Config{Type: storeconfig.Redis, URL: url}, time.Minute, log)
	require.NoError(t, err)
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	require.NoError(t, tpd.RegisterTransportsBatch(ctx, a, []*transport.SignedEntry{{Entry: e}}))
	shareKey := url + "#" + uuid.NewString()
	require.NoError(t, tpd.(interface {
		EnableLiveSet(context.Context, string) error
	}).EnableLiveSet(ctx, shareKey))

	own, err := tpdstore.New(ctx, storeconfig.Config{Type: storeconfig.Memory}, time.Minute, log)
	require.NoError(t, err)
	c := NewGraphCache(own, time.Hour, nil)

	g, err := c.Rebuild(ctx)
	require.NoError(t, err)
	require.Empty(t, g.graph, "not shared: its own store is empty")

	c.ShareFrom(shareKey)
	g, err = c.Rebuild(ctx)
	require.NoError(t, err)
	require.Contains(t, g.graph, a, "TPD's transport, from its live set")
	require.Contains(t, g.graph, b)
}

// The same with an in-memory TPD store, as a deployment without redis runs.
func TestGraphCacheSharesCoHostedMemoryTPD(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logging.MustGetLogger("test")
	mem := storeconfig.Config{Type: storeconfig.Memory}
	tpd, err := tpdstore.New(ctx, mem, time.Minute, log)
	require.NoError(t, err)
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	require.NoError(t, tpd.RegisterTransportsBatch(ctx, a, []*transport.SignedEntry{{Entry: e}}))
	shareKey := "redis://localhost:6379#" + uuid.NewString()
	tpdstore.ShareStore(ctx, shareKey, tpd)

	own, err := tpdstore.New(ctx, mem, time.Minute, log)
	require.NoError(t, err)
	c := NewGraphCache(own, time.Hour, nil)
	c.ShareFrom(shareKey)
	g, err := c.Rebuild(ctx)
	require.NoError(t, err)
	require.Contains(t, g.graph, a, "TPD's transport, from its shared memory store")
	require.Contains(t, g.graph, b)
}
