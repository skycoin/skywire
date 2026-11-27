// Package clirewards cmd/skywire-cli/commands/rewards/bwcollect_local_test.go c4-vis-cli
package clirewards

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/tpd/api"
	tpdmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
)

// metricsRecordStore is a TPD store whose GET /metrics answer is recs. It
// records the query each read was made with.
type metricsRecordStore struct {
	tpdstore.Store
	recs []tpdstore.TransportMetric

	mu      sync.Mutex
	queries []tpdstore.MetricsQuery
}

func (s *metricsRecordStore) GetAllTransportMetrics(_ context.Context, q tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	s.mu.Lock()
	s.queries = append(s.queries, q)
	s.mu.Unlock()
	// A copy, since the in-process reader clears the records it reduced.
	return append([]tpdstore.TransportMetric(nil), s.recs...), nil
}

// TestBandwidthCollectLocalMatchesDmsg checks that bw-collect writes the same
// bytes when it reads transport discovery in this process as when it fetches
// GET /metrics from the same TPD API, and that both reads ask the store the
// same question.
func TestBandwidthCollectLocalMatchesDmsg(t *testing.T) {
	const date = "2026-11-21"
	pks := make([]string, 8)
	for i := range pks {
		pk, _ := cipher.GenerateKeyPair()
		pks[i] = pk.Hex()
	}
	edge := func(sent, recv uint64) *tpdstore.EdgeBandwidth {
		return &tpdstore.EdgeBandwidth{Sent: sent, Recv: recv}
	}
	recs := []tpdstore.TransportMetric{
		{ID: "tp-7", Type: "stcpr", Live: true, Edges: []string{pks[2], pks[3]}, Daily: []tpdstore.DailyEdgeBandwidth{
			{Date: date, A: edge(500, 70), B: edge(80, 400)},
			{Date: "2026-11-20", A: edge(9, 9), B: edge(9, 9)},
		}},
		// Same LAN by the surveys below: left out.
		{ID: "tp-2", Type: "sudph", Edges: []string{pks[0], pks[1]}, Daily: []tpdstore.DailyEdgeBandwidth{
			{Date: date, A: edge(100, 100), B: edge(100, 100)},
		}},
		// Only one edge reported: no credit.
		{ID: "tp-3", Type: "stcpr", Edges: []string{pks[4], pks[5]}, Daily: []tpdstore.DailyEdgeBandwidth{
			{Date: date, A: edge(100, 0)},
		}},
		// No edges: left out.
		{ID: "tp-4", Type: "stcpr", Daily: []tpdstore.DailyEdgeBandwidth{{Date: date, A: edge(1, 1), B: edge(1, 1)}}},
		// Checkpoints cap what A is paid.
		{ID: "tp-5", Type: "sudph", Live: true, Edges: []string{pks[6], pks[7]}, Daily: []tpdstore.DailyEdgeBandwidth{
			{Date: date, SameNetwork: true,
				A: &tpdstore.EdgeBandwidth{Sent: 1 << 40, Recv: 3000, Verified: 10, Checked: true},
				B: &tpdstore.EdgeBandwidth{Sent: 3000, Recv: 1 << 40}},
		}},
		{ID: "tp-1", Type: "stcpr", Edges: []string{pks[3], pks[2]}, Daily: []tpdstore.DailyEdgeBandwidth{
			{Date: date, A: edge(1, 2), B: edge(2, 1)},
		}},
	}
	st := &metricsRecordStore{recs: recs}
	nonces, err := httpauth.NewNonceStore(context.Background(), storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	tpdAPI := api.New(logging.MustGetLogger("tpd-test"), st, nonces, false, tpdmetrics.NewEmpty(), "", "")
	srv := httptest.NewServer(tpdAPI)
	defer srv.Close()

	wd := t.TempDir()
	surveys := filepath.Join(wd, "log_backups")
	for _, pk := range pks[:2] {
		require.NoError(t, os.MkdirAll(filepath.Join(surveys, pk), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(surveys, pk, "node-info.json"), []byte(`{"ip_address":"203.0.113.9","skywire_version":"v1.3.112"}`), 0o600))
	}

	oldFetch, oldCache := fetchServiceURL, bwCacheFile
	oldHist, oldSurveys, oldDate, oldDays, oldNoCache := histPath, bwSurveyPath, bwDate, bwDays, noCache
	t.Cleanup(func() {
		fetchServiceURL, bwCacheFile = oldFetch, oldCache
		histPath, bwSurveyPath, bwDate, bwDays, noCache = oldHist, oldSurveys, oldDate, oldDays, oldNoCache
	})
	bwCacheFile = filepath.Join(wd, "cache.json")
	// The dmsg path: the same URL against the TPD API over HTTP.
	fetchServiceURL = func(_ *pflag.FlagSet, raw string) ([]byte, error) {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		resp, err := http.Get(srv.URL + u.RequestURI()) //nolint:gosec,noctx // test server
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	}
	bwSurveyPath, bwDate, bwDays, noCache = surveys, date, 2, true

	collect := func(dir string, local tpdMetricsLookup) []byte {
		histPath = filepath.Join(wd, dir)
		require.NoError(t, collectBandwidth(nil, logging.MustGetLogger("bw-collect-test"), local))
		b, err := os.ReadFile(filepath.Join(histPath, date+"_bandwidth.json")) //nolint:gosec
		require.NoError(t, err)
		return b
	}
	viaDmsg := collect("dmsg", nil)
	inProcess := collect("local", func() tpdMetricsReader { return tpdLocalAPI{tpdAPI} })

	require.Equal(t, string(viaDmsg), string(inProcess))
	require.Contains(t, string(viaDmsg), pks[2])
	require.NotContains(t, string(viaDmsg), pks[0])
	require.Len(t, st.queries, 2)
	require.Equal(t, st.queries[0], st.queries[1], "the in-process read asks the store what GET /metrics asks")

	// Transport discovery that cannot answer falls back to dmsg.
	fallback := collect("fallback", func() tpdMetricsReader { return tpdLocalFailing{} })
	require.Equal(t, string(viaDmsg), string(fallback))
}

// tpdLocalAPI is the in-process reader pkg/services/tpd gives, over an API.
type tpdLocalAPI struct{ api *api.API }

func (l tpdLocalAPI) TransportMetricsRead(ctx context.Context, q tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return l.api.TransportMetrics(ctx, q)
}

type tpdLocalFailing struct{}

func (tpdLocalFailing) TransportMetricsRead(context.Context, tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return nil, fmt.Errorf("not started")
}
