package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/services"
	_ "github.com/skycoin/skywire/pkg/services/ar"
	_ "github.com/skycoin/skywire/pkg/services/rf"
	_ "github.com/skycoin/skywire/pkg/services/tpd"
)

// embedBlock builds the block, embeds it with no dmsg client and mounts it
// under its prefix on a fresh mux, the way the visor does.
func embedBlock(t *testing.T, raw string) (*http.ServeMux, string) {
	t.Helper()
	var b services.Block
	require.NoError(t, json.Unmarshal([]byte(raw), &b))
	factory, ok := services.Lookup(b.Type)
	require.True(t, ok, "factory for %s", b.Type)
	svc, err := factory(b.Raw, nil)
	require.NoError(t, err)
	emb, ok := svc.(services.Embeddable)
	require.True(t, ok, "%s must be embeddable", b.Type)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h, err := emb.Embed(ctx, services.Host{DmsgAddr: "0300000000000000000000000000000000000000000000000000000000000000ab:80"})
	require.NoError(t, err)

	mux := http.NewServeMux()
	services.Mount(mux, b.Prefix(), h)
	return mux, b.Prefix()
}

func TestEmbedMountsUnderPrefix(t *testing.T) {
	cases := []struct {
		raw    string
		prefix string
	}{
		{`{"type":"transport-discovery","testing":true}`, "/tpd"},
		{`{"type":"route-finder","testing":true}`, "/rf"},
		{`{"type":"address-resolver","testing":true,"udp_addr":"127.0.0.1:0"}`, "/ar"},
		{`{"type":"route-finder","testing":true,"prefix":"routes/"}`, "/routes"},
	}
	for _, c := range cases {
		t.Run(c.prefix, func(t *testing.T) {
			mux, prefix := embedBlock(t, c.raw)
			require.Equal(t, c.prefix, prefix)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, prefix+"/health", nil))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			// The same path without the prefix is not the service.
			rec = httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
			require.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestMountKeepsRootHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("root")) //nolint:errcheck
	}))
	var gotPath string
	services.Mount(mux, "/tpd", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("svc")) //nolint:errcheck
	}))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tpd/transports/edge:abc", nil))
	require.Equal(t, "svc", rec.Body.String())
	require.Equal(t, "/transports/edge:abc", gotPath)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, "root", rec.Body.String())
}

func TestBlockPrefixAndRoundTrip(t *testing.T) {
	raw := `{"type":"transport-discovery","name":"tpd-a","redis":"redis://x:6379","entry_timeout":"5m"}`
	var b services.Block
	require.NoError(t, json.Unmarshal([]byte(raw), &b))
	require.Equal(t, "/tpd", b.Prefix())

	out, err := json.Marshal(b)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(out))

	var unknown services.Block
	require.NoError(t, json.Unmarshal([]byte(`{"type":"stun-server"}`), &unknown))
	require.Equal(t, "/stun-server", unknown.Prefix())

	var custom services.Block
	require.NoError(t, json.Unmarshal([]byte(`{"type":"address-resolver","prefix":"/resolver/"}`), &custom))
	require.Equal(t, "/resolver", custom.Prefix())
}
