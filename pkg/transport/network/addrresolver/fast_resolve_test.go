package addrresolver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
)

func TestResolveFastPath(t *testing.T) {
	target, _ := cipher.GenerateKeyPair()
	var httpCalls atomic.Int64
	mux := httputil.NewRouter()
	for _, typ := range []string{"stcpr", "sudph", "squicr"} {
		mux.Get("/resolve/"+typ+"/{pk}", func(w http.ResponseWriter, _ *http.Request) {
			httpCalls.Add(1)
			_ = json.NewEncoder(w).Encode(VisorData{RemoteAddr: "9.9.9.9:1"}) //nolint:errcheck
		})
	}
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()
	c := newReadyClient(t, srv)
	c.SetPublicIP("5.5.5.5", "")

	var asked []string
	c.SetResolveFastPath(func(_ context.Context, tType string, _ cipher.PubKey) (VisorData, bool) {
		asked = append(asked, tType)
		if tType == "squicr" {
			return VisorData{}, false
		}
		return VisorData{RemoteAddr: "1.2.3.4:5000"}, true
	})

	vd, err := c.Resolve(context.Background(), "stcpr", target)
	require.NoError(t, err)
	require.Equal(t, "1.2.3.4:5000", vd.RemoteAddr, "answered from the fast path")
	require.Zero(t, httpCalls.Load())

	vd, err = c.Resolve(context.Background(), "squicr", target)
	require.NoError(t, err)
	require.Equal(t, "9.9.9.9:1", vd.RemoteAddr, "a fast-path miss falls through to HTTP")

	_, err = c.Resolve(context.Background(), "sudph", target)
	require.NoError(t, err)
	require.Equal(t, int64(2), httpCalls.Load())
	require.Equal(t, []string{"stcpr", "squicr"}, asked, "sudph is never asked of the fast path")

	c.SetResolveFastPath(nil)
	_, err = c.Resolve(context.Background(), "stcpr", target)
	require.NoError(t, err)
	require.Equal(t, int64(3), httpCalls.Load())
}

func TestAskResolverSkipsFastPathAndNotFound(t *testing.T) {
	target, _ := cipher.GenerateKeyPair()
	var calls atomic.Int64
	var bound atomic.Bool
	mux := httputil.NewRouter()
	mux.Get("/resolve/stcpr/{pk}", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if !bound.Load() {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(VisorData{RemoteAddr: "9.9.9.9:2"}) //nolint:errcheck
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()
	c := newReadyClient(t, srv)

	_, err := c.Resolve(context.Background(), "stcpr", target)
	require.ErrorIs(t, err, ErrNoEntry, "the peer has not bound yet")
	bound.Store(true)
	_, err = c.Resolve(context.Background(), "stcpr", target)
	require.ErrorIs(t, err, ErrNoEntry, "the not-found is remembered")
	require.Equal(t, int64(1), calls.Load())

	c.SetResolveFastPath(func(context.Context, string, cipher.PubKey) (VisorData, bool) {
		return VisorData{RemoteAddr: "1.2.3.4:1"}, true
	})
	vd, err := c.Resolve(AskResolver(context.Background()), "stcpr", target)
	require.NoError(t, err)
	require.Equal(t, "9.9.9.9:2", vd.RemoteAddr, "the resolver answers, not the fast path")
	require.Equal(t, int64(2), calls.Load())
}

func TestSameHostMirrorsTheResolver(t *testing.T) {
	require.True(t, sameHost("1.2.3.4:1", "1.2.3.4:2"))
	require.False(t, sameHost("1.2.3.4:1", "1.2.3.4"), "a bare IP never matches, as on the resolver")
	require.False(t, sameHost("1.2.3.4:1", "5.6.7.8:1"))
}

func TestResolveRemembersNotFound(t *testing.T) {
	target, _ := cipher.GenerateKeyPair()
	var calls atomic.Int64
	mux := httputil.NewRouter()
	mux.Get("/resolve/sudph/{pk}", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "nope", http.StatusNotFound)
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()
	c := newReadyClient(t, srv)

	for i := 0; i < 3; i++ {
		_, err := c.Resolve(context.Background(), "sudph", target)
		require.ErrorIs(t, err, ErrNoEntry)
	}
	require.Equal(t, int64(1), calls.Load(), "a recent not-found is answered without asking")

	other, _ := cipher.GenerateKeyPair()
	_, err := c.Resolve(context.Background(), "sudph", other)
	require.ErrorIs(t, err, ErrNoEntry)
	require.Equal(t, int64(2), calls.Load(), "another peer is asked")
}
