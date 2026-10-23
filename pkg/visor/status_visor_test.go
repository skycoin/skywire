package visor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestVisorChartsPage(t *testing.T) {
	// A visor still starting has none of what is sampled.
	c := &visorCharts{v: &Visor{}, prev: map[uuid.UUID]transport.BandwidthData{}}
	v, err := c.collect(context.Background())
	require.NoError(t, err)
	require.Empty(t, v)

	ctx := context.Background()
	st := charts.NewMemoryStore()
	now := time.Now().UTC().Truncate(charts.Interval)
	for i := 3; i > 0; i-- {
		require.NoError(t, st.Add(ctx, charts.Sample{At: now.Add(-time.Duration(i) * charts.Interval), V: map[string]float64{
			chartTpCount + "stcpr": 2, chartTpOut + "stcpr": 1 << 20, chartTpIn + "stcpr": 2 << 20,
			chartRoutes: 4, chartDmsgSess: 3,
		}}))
	}
	page := &charts.Page{Title: "Skywire visor", Store: st, Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
		return buildVisorCharts(ctx, st, r, now)
	}}
	rec := httptest.NewRecorder()
	page.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status/visor", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	for _, s := range []string{"Bytes sent over transports, by type", "Transports, by type", "Routes and sessions", "stcpr"} {
		require.Contains(t, string(body), s)
	}
}
