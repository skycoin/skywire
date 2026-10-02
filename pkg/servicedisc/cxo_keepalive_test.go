// Package servicedisc pkg/servicedisc/cxo_keepalive_test.go: an unchanged
// heartbeat skips the HTTP POST while a KeepaliveSink is healthy on the epoch
// of the last registration, and never on a timer.
package servicedisc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeKeepaliveSink struct {
	healthy atomic.Bool
	epoch   atomic.Uint64
}

func (*fakeKeepaliveSink) PutEntry(Service) {}
func (*fakeKeepaliveSink) DelEntry(Service) {}
func (s *fakeKeepaliveSink) KeepaliveHealthy() (bool, uint64) {
	return s.healthy.Load(), s.epoch.Load()
}

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

	sink.epoch.Add(1)
	require.NoError(t, c.RegisterEntry(ctx))
	require.NoError(t, c.RegisterEntry(ctx))
	require.EqualValues(t, 4, posts.Load(), "new epoch: posts once")

	require.NoError(t, c.DeleteEntry(ctx))
	require.NoError(t, c.RegisterEntry(ctx))
	require.EqualValues(t, 5, posts.Load(), "after a delete: posts")
}
