package logserver

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

type healthStats struct{ srv *httputil.DmsgServerHealth }

func (healthStats) IsPublicAutoconnectRunning() bool               { return true }
func (healthStats) GetTransportCounts() (int, int)                 { return 1, 2 }
func (healthStats) GetTransportTypeCounts() map[string]int         { return map[string]int{"stcpr": 1} }
func (healthStats) GetNetworkTypes() []string                      { return nil }
func (healthStats) DmsgSessionCount() int                          { return 3 }
func (h healthStats) DmsgServerHealth() *httputil.DmsgServerHealth { return h.srv }

// A visor running a dmsg server reports the server's load on /health, and
// every visor its own dmsg sessions.
func TestHealthReportsDmsg(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("logserver-test"), t.TempDir(), "", []cipher.PubKey{pk}, &visorconfig.Survey{}, false)
	health := func() httputil.HealthCheckResponse {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
		var h httputil.HealthCheckResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &h))
		return h
	}

	api.SetHealthStatsProvider(healthStats{srv: &httputil.DmsgServerHealth{ClientSessions: 7, BytesUp: 10}})
	h := health()
	require.Equal(t, 3, h.DmsgSessions)
	require.NotNil(t, h.DmsgServer)
	require.Equal(t, 7, h.DmsgServer.ClientSessions)

	api.SetHealthStatsProvider(healthStats{})
	require.Nil(t, health().DmsgServer, "a visor without a server says nothing about one")
}
