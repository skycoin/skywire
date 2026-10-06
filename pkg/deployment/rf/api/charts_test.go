package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestCollectChartsCountsRequestsPerInterval(t *testing.T) {
	a := &API{}
	c := &rfCharts{prevNode: map[cipher.PubKey]*setupmetrics.StatsSnapshot{}}

	a.stats.request(2, 40*time.Millisecond)
	a.stats.route(2)
	a.stats.miss()
	v := collect(t, a, c)
	require.Equal(t, 1.0, v[chartRequests])
	require.Equal(t, 2.0, v[chartPairs])
	require.Equal(t, 1.0, v[chartNoRoute])
	require.Equal(t, 1.0, v[chartHopsPrefix+"2"])
	require.Equal(t, 40.0, v[chartMeanMs])

	a.stats.request(1, 10*time.Millisecond)
	a.stats.route(9)
	v = collect(t, a, c)
	require.Equal(t, 1.0, v[chartRequests], "only the change since the last sample")
	require.Equal(t, 0.0, v[chartNoRoute])
	require.Equal(t, 1.0, v[chartHopsPrefix+"6"], "long routes count in the last bucket")
	require.Equal(t, 10.0, v[chartMeanMs])
}

func collect(t *testing.T, a *API, c *rfCharts) map[string]float64 {
	t.Helper()
	v, err := a.collectCharts(context.Background(), c)
	require.NoError(t, err)
	return v
}

func TestCollectSetupNodes(t *testing.T) {
	var mu sync.Mutex
	snap := setupmetrics.StatsSnapshot{StartedAt: time.Unix(100, 0), TotalRequests: 10, Successful: 8, Failed: 2,
		FailuresByReason: map[setupmetrics.FailureReason]uint64{"source_unreachable": 2},
		RouteLengthHist:  map[int]uint64{1: 5, 2: 3},
		LatencyMs:        setupmetrics.LatencyStats{Count: 8, P50: 120, P95: 900}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(snap) //nolint:errcheck
	}))
	defer srv.Close()
	pk, _ := cipher.GenerateKeyPair()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}}}
	c := &rfCharts{sn: &SetupNodes{PKs: []cipher.PubKey{pk}, Client: client, Port: 80},
		prevNode: map[cipher.PubKey]*setupmetrics.StatsSnapshot{}}
	k := chartSNPrefix + pk.Hex() + "."

	v := map[string]float64{}
	c.collectSetupNodes(context.Background(), v)
	require.Equal(t, 1.0, v[k+"up"])
	require.Equal(t, 120.0, v[k+"p50"])
	_, counted := v[k+"req"]
	require.False(t, counted, "the first read is only a baseline")

	mu.Lock()
	snap.TotalRequests, snap.Successful, snap.Failed = 15, 12, 3
	snap.FailuresByReason = map[setupmetrics.FailureReason]uint64{"source_unreachable": 3}
	snap.RouteLengthHist = map[int]uint64{1: 7, 2: 5}
	mu.Unlock()
	v = map[string]float64{}
	c.collectSetupNodes(context.Background(), v)
	require.Equal(t, 5.0, v[k+"req"])
	require.Equal(t, 80.0, v[k+"rate"])
	require.Equal(t, 1.0, v[chartSNFail+"source_unreachable"])
	require.Equal(t, 2.0, v[chartSNHops+"1"])

	mu.Lock()
	snap.StartedAt, snap.TotalRequests, snap.Successful, snap.Failed = time.Unix(200, 0), 2, 2, 0
	snap.FailuresByReason, snap.RouteLengthHist = nil, map[int]uint64{1: 2}
	mu.Unlock()
	v = map[string]float64{}
	c.collectSetupNodes(context.Background(), v)
	require.Equal(t, 2.0, v[k+"req"], "a restarted node counts from zero")
	require.Equal(t, 100.0, v[k+"rate"])

	srv.Close()
	v = map[string]float64{}
	c.collectSetupNodes(context.Background(), v)
	require.Equal(t, 0.0, v[k+"up"])
}

func TestRouteRequestsAreCountedAndCharted(t *testing.T) {
	src, dst, lone := newPK(t), newPK(t), newPK(t)
	s := newFakeStore()
	s.saveEntry(src, dst)
	api := newTestAPI(s)
	opts := &rfclient.RouteOptions{MaxHops: 5}
	require.Equal(t, http.StatusOK, postRoutes(t, api, rfclient.FindRoutesRequest{Edges: []routing.PathEdges{{src, dst}}, Opts: opts}).Code)
	require.Equal(t, http.StatusNotFound, postRoutes(t, api, rfclient.FindRoutesRequest{Edges: []routing.PathEdges{{src, lone}}, Opts: opts}).Code)

	got := api.stats.snapshot()
	require.Equal(t, uint64(2), got.requests)
	require.Equal(t, uint64(2), got.pairs)
	require.Equal(t, uint64(1), got.noRoute)
	require.Equal(t, uint64(1), got.hops[1])

	require.Equal(t, http.StatusNotFound, do(t, api, http.MethodGet, "/", nil).Code)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.StartCharts(ctx, charts.NewMemoryStore(), nil, logrus.New())
	rec := do(t, api, http.MethodGet, "/", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Route requests")
}
