package visor

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// chartsAPI answers VisorCharts with a few samples and counts the calls.
type chartsAPI struct {
	visorapi.API
	mu    sync.Mutex
	calls int
}

func (a *chartsAPI) VisorCharts(args visorapi.VisorChartsArgs) (*visorapi.VisorCharts, error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	out := &visorapi.VisorCharts{Starts: []visorapi.ChartStart{{At: args.To.Add(-time.Hour), Version: "v1.3.101"}}}
	for at := args.To.Add(-time.Hour).Truncate(charts.Interval); at.Before(args.To); at = at.Add(charts.Interval) {
		out.Samples = append(out.Samples, visorapi.ChartSample{At: at, V: map[string]float64{
			chartTpCount + "dmsg": 3, chartTpOut + "dmsg": 4096, "proc.cpu": 2,
		}})
	}
	return out, nil
}

// oldAPI is a visor whose build has no VisorCharts.
type oldAPI struct{ visorapi.API }

func (oldAPI) VisorCharts(visorapi.VisorChartsArgs) (*visorapi.VisorCharts, error) {
	return nil, errors.New("gobrpc: can't find service app-visor")
}

func TestHypervisorStatus(t *testing.T) {
	hvPK, _ := cipher.GenerateKeyPair()
	remote, _ := cipher.GenerateKeyPair()
	old, _ := cipher.GenerateKeyPair()
	self, other := &chartsAPI{}, &chartsAPI{}
	hv := &Hypervisor{
		c:            visorconfig.HypervisorConfig{PK: hvPK},
		mu:           new(sync.RWMutex),
		selfConn:     Conn{API: self},
		remoteVisors: map[cipher.PubKey]Conn{remote: {API: other}, old: {API: oldAPI{}}},
		logger:       logging.MustGetLogger("hv-status-test"),
	}
	rec := httptest.NewRecorder()
	(&hvStatus{hv: hv}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	b, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	body := string(b)

	require.Contains(t, body, "Visor "+hvPK.Hex())
	require.Contains(t, body, "Visor "+remote.Hex())
	require.Less(t, strings.Index(body, hvPK.Hex()), strings.Index(body, remote.Hex()), "the hypervisor comes first")
	// Each visor is its own process, so each section keeps its process charts.
	require.Equal(t, 2, strings.Count(body, "Share of one core used by the visor&#39;s process"))
	require.Contains(t, body, "Transports, by type")
	// A page reads one window for its charts and its process charts.
	require.Equal(t, 1, self.calls)
	require.Equal(t, 1, other.calls)
	require.Contains(t, body, "keeps no status charts")
}
