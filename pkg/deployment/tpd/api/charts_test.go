package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestCollectCharts(t *testing.T) {
	ctx := context.Background()
	mock := newTestStore(t)
	nonces, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, mock, nonces, false, tpdiscmetrics.NewEmpty(), "", "")

	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()
	for _, e := range []*transport.Entry{
		{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"},
		{ID: uuid.New(), Edges: transport.SortEdges(a, c), Type: "stcpr"},
		{ID: uuid.New(), Edges: transport.SortEdges(b, c), Type: "dmsg"},
	} {
		require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, &transport.SignedEntry{Entry: e}))
	}

	v, err := api.collectCharts(ctx)
	require.NoError(t, err)
	require.Equal(t, 2.0, v[chartTransportPrefix+"stcpr"])
	require.Equal(t, 1.0, v[chartTransportPrefix+"dmsg"])
	require.Equal(t, 3.0, v[chartVisorsLinked])
}

func TestChartsPageOnRoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nonces, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, newTestStore(t), nonces, false, tpdiscmetrics.NewEmpty(), "", "")

	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusNotFound, w.Code)

	api.StartCharts(ctx, charts.NewMemoryStore(), logging.MustGetLogger("test"))
	w = httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?range=30d", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Transports by type")
}
