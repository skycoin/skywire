// Package addrresolver pkg/transport/network/addrresolver/client_cxo_keepalive_test.go:
// an unchanged re-bind skips the HTTP POST while the CXO keepalive is healthy
// on the epoch of the last accepted bind, and never on a timer.
package addrresolver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/httputil"
)

func TestBindSkipsUnchangedWhileCXOHealthy(t *testing.T) {
	var posts atomic.Int32
	mux := httputil.NewRouter()
	mux.Post("/bind/quic", func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()

	c := newReadyClient(t, srv)
	c.SetPublicIP("", "")
	ctx := context.Background()
	var healthy atomic.Bool
	var epoch atomic.Uint64
	c.SetCXOKeepaliveHealthyFunc(func() (bool, uint64) { return healthy.Load(), epoch.Load() })

	require.NoError(t, c.BindQUIC(ctx, "30300"))
	require.NoError(t, c.BindQUIC(ctx, "30300"))
	require.EqualValues(t, 2, posts.Load(), "unhealthy: every bind posts")

	healthy.Store(true)
	for i := 0; i < 3; i++ {
		require.NoError(t, c.BindQUIC(ctx, "30300"))
	}
	require.EqualValues(t, 2, posts.Load(), "healthy + unchanged: skipped")

	require.NoError(t, c.BindQUIC(ctx, "30301"))
	require.EqualValues(t, 3, posts.Load(), "changed payload: posts")

	epoch.Add(1)
	require.NoError(t, c.BindQUIC(ctx, "30301"))
	require.NoError(t, c.BindQUIC(ctx, "30301"))
	require.EqualValues(t, 4, posts.Load(), "new epoch: posts once")

	c.forgetBound("squicr")
	require.NoError(t, c.BindQUIC(ctx, "30301"))
	require.EqualValues(t, 5, posts.Load(), "forgotten: posts")
}
