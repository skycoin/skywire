package api

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	tb := visorBWTable(day, map[string]string{"bb": roleRegistered})
	require.Contains(t, tb.Title, "2026-10-05")
	require.Contains(t, tb.Note, "1 transports between visors on the same network")
	require.Equal(t, []string{"bb", "registered dmsg server", "1 GiB", "stcpr 1 GiB, sudph 1 MiB"}, tb.Rows[0])
	require.Equal(t, "#4e79a7", tb.Marks[0])
	require.Equal(t, []string{"aa", ""}, tb.Rows[1][:2])
	require.Empty(t, tb.Marks[1])
}

func TestGraphPage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nonces, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	mock := newTestStore(t)
	api := New(nil, mock, nonces, false, tpdiscmetrics.NewEmpty(), "", "")
	api.StartCharts(ctx, charts.NewMemoryStore(), logging.MustGetLogger("test"))

	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/graph", nil))
		return w
	}
	require.Equal(t, http.StatusServiceUnavailable, get().Code, "no graph before the transport cache is warm")

	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, &transport.SignedEntry{Entry: &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}}))
	api.refreshTransportsCache(ctx, logging.MustGetLogger("test"))
	w := get()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "2 visors, 1 visor pairs, 1 transports")
}

func TestGraphEngineRoutes(t *testing.T) {
	ctx := context.Background()
	nonces, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, newTestStore(t), nonces, false, tpdiscmetrics.NewEmpty(), "", "")

	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/graph/engine.js", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "globalThis.Go", "the loader is the wasm_exec.js that runs the netview module")

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/graph/engine.wasm", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	api.ServeHTTP(w, r)
	require.Contains(t, []int{http.StatusOK, http.StatusFound}, w.Code, "served when embedded, redirected when not")
}

func TestServerRoles(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/dmsg-discovery/all_servers":
			_, _ = w.Write([]byte(`[{"static":"reg"}]`)) //nolint:errcheck
		case "/dmsg-discovery/servers/clients":
			_, _ = w.Write([]byte(`{"reg":["c1"],"lan":["c2"]}`)) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	api := &API{}
	require.Nil(t, api.serverRoles(context.Background()), "no roles without dmsg discovery")

	api.SetDmsgDiscovery(srv.Client(), "dmsg://"+strings.TrimPrefix(srv.URL, "http://"))
	roles := api.serverRoles(context.Background())
	require.Equal(t, map[string]string{"reg": roleRegistered, "lan": roleLAN}, roles)
	api.serverRoles(context.Background())
	require.Equal(t, 2, calls, "a second read within the refresh time is cached")

	srv.Close()
	api.dmsgRoles.Load().at = time.Time{}
	require.Equal(t, roles, api.serverRoles(context.Background()), "a failed refresh keeps the last answer")
}
