// Package addrresolver pkg/transport/network/addrresolver/client_cxo_keepalive_test.go:
// an unchanged re-bind skips the HTTP POST only while the CXO keepalive is
// healthy and the last accepted bind is recent.
package addrresolver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestBindSkipsUnchangedWhileCXOHealthy(t *testing.T) {
	var posts atomic.Int32
	mux := chi.NewRouter()
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
	c.SetCXOKeepaliveHealthyFunc(healthy.Load)

	require.NoError(t, c.BindQUIC(ctx, "30300"))
	require.NoError(t, c.BindQUIC(ctx, "30300"))
	require.EqualValues(t, 2, posts.Load(), "unhealthy: every bind posts")

	healthy.Store(true)
	require.NoError(t, c.BindQUIC(ctx, "30300"))
	require.EqualValues(t, 2, posts.Load(), "healthy + unchanged: skipped")

	require.NoError(t, c.BindQUIC(ctx, "30301"))
	require.EqualValues(t, 3, posts.Load(), "changed payload: posts")

	c.boundMu.Lock()
	b := c.bound["squicr"]
	b.at = time.Now().Add(-cxoHealthyRefreshInterval)
	c.bound["squicr"] = b
	c.boundMu.Unlock()
	require.NoError(t, c.BindQUIC(ctx, "30301"))
	require.EqualValues(t, 4, posts.Load(), "refresh interval elapsed: posts")

	c.forgetBound("squicr")
	require.NoError(t, c.BindQUIC(ctx, "30301"))
	require.EqualValues(t, 5, posts.Load(), "forgotten: posts")
}
