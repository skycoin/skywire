package api

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
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

	// With the cache warm, visors are also bucketed by their transport count.
	api.refreshTransportsCache(ctx, logging.MustGetLogger("test"))
	v, err = api.collectCharts(ctx)
	require.NoError(t, err)
	require.Equal(t, 3.0, v[chartPerVisorPrefix+"2"])
	require.Equal(t, 0.0, v[chartPerVisorPrefix+"1"])
	require.Equal(t, 0.0, v[chartPerVisorPrefix+"10+"])
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

func TestPerVisorBucket(t *testing.T) {
	for n, want := range map[int]string{1: "1", 2: "2", 4: "4", 5: "5-9", 9: "5-9", 10: "10+", 500: "10+"} {
		require.Equal(t, want, perVisorBucket(n), n)
	}
}

func TestDailyLatencyChartGapsMissingDays(t *testing.T) {
	c := dailyLatencyChart([]store.DailyAggregate{
		{Date: "2026-10-02", Latency: 80, ByType: map[string]*store.TypeMetricAggregate{"stcpr": {Latency: 70}}},
		{Date: "2026-10-01", Latency: 0},
	})
	require.Equal(t, "all", c.Series[0].Name)
	require.True(t, math.IsNaN(c.Series[0].Vals[0]), "a day without a report is a gap, not 0 ms")
	require.Equal(t, 80.0, c.Series[0].Vals[1])
	require.True(t, math.IsNaN(c.Series[1].Vals[0]))
	require.Equal(t, 70.0, c.Series[1].Vals[1])
}

func TestVisorBWTable(t *testing.T) {
	day := &store.VisorBWDay{Date: "2026-10-05", Transports: 3, SameNetworkExcluded: 1, Visors: map[string]map[string]uint64{
		"aa": {"stcpr": 1 << 20},
		"bb": {"stcpr": 1 << 30, "sudph": 1 << 20},
	}}
	tb := visorBWTable(day)
	require.Contains(t, tb.Title, "2026-10-05")
	require.Contains(t, tb.Note, "1 transports between visors on the same network")
	require.Equal(t, []string{"bb", "1 GiB", "stcpr 1 GiB, sudph 1 MiB"}, tb.Rows[0])
	require.Equal(t, "aa", tb.Rows[1][0])
}
