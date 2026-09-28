// Package servicedisc pkg/servicedisc/cxo_keepalive_test.go: an unchanged
// heartbeat skips the HTTP POST only while a KeepaliveSink is healthy.
package servicedisc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeKeepaliveSink struct{ healthy atomic.Bool }

func (*fakeKeepaliveSink) PutEntry(Service)         {}
func (*fakeKeepaliveSink) DelEntry(Service)         {}
func (s *fakeKeepaliveSink) KeepaliveHealthy() bool { return s.healthy.Load() }

func TestRegisterEntrySkipsUnchangedWhileCXOHealthy(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(nonceHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`)) //nolint
			return
		}
		posts.Add(1)
		var in Service
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		in.Version = "v1"
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(&in))
	}))
	defer srv.Close()

	c := authClientFor(srv.URL, srv.Client())
	sink := &fakeKeepaliveSink{}
	c.conf.Sink = sink
	ctx := context.Background()

	require.NoError(t, c.RegisterEntry(ctx))
	require.NoError(t, c.RegisterEntry(ctx))
	require.EqualValues(t, 2, posts.Load(), "unhealthy: every heartbeat posts")

	sink.healthy.Store(true)
	require.NoError(t, c.RegisterEntry(ctx))
	require.EqualValues(t, 2, posts.Load(), "healthy + unchanged: skipped")

	c.SetCoinInfo(&CoinInfo{BlockchainPubKey: "x"})
	require.NoError(t, c.RegisterEntry(ctx))
	require.EqualValues(t, 3, posts.Load(), "changed entry: posts")

	c.postedAt = time.Now().Add(-cxoHealthyRefreshInterval)
	require.NoError(t, c.RegisterEntry(ctx))
	require.EqualValues(t, 4, posts.Load(), "refresh interval elapsed: posts")

	require.NoError(t, c.DeleteEntry(ctx))
	require.NoError(t, c.RegisterEntry(ctx))
	require.EqualValues(t, 5, posts.Load(), "after a delete: posts")
}
