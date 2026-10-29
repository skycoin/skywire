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

func TestDisableBinding(t *testing.T) {
	var binds atomic.Int32
	mux := httputil.NewRouter()
	for _, p := range []string{"/bind/stcpr", "/bind/quic", "/bind/wt"} {
		mux.Post(p, func(w http.ResponseWriter, _ *http.Request) {
			binds.Add(1)
			w.WriteHeader(http.StatusOK)
		})
	}
	mux.Get("/resolve/{type}/{pk}", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(VisorData{RemoteAddr: "203.0.113.7:7777"}) //nolint
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()

	c := newReadyClient(t, srv)
	c.SetPublicIP("", "")
	c.DisableBinding()
	ctx := context.Background()
	require.NoError(t, c.BindSTCPR(ctx, "7777"))
	require.NoError(t, c.BindQUIC(ctx, "7778"))
	require.NoError(t, c.BindWT(ctx, "7779", "aa", "bb"))
	_, err := c.BindSUDPH(nil, nil)
	require.ErrorIs(t, err, ErrBindingDisabled)
	require.Zero(t, binds.Load(), "a disabled client must not reach the bind endpoints")

	pk, _ := cipher.GenerateKeyPair()
	vd, err := c.Resolve(ctx, "stcpr", pk)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.7:7777", vd.RemoteAddr)

	on := newReadyClient(t, srv)
	on.SetPublicIP("", "")
	require.NoError(t, on.BindSTCPR(ctx, "7777"))
	require.Equal(t, int32(1), binds.Load())
}
