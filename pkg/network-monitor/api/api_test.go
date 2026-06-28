// Package api pkg/network-monitor/api/api_test.go: unit tests for the
// network-monitor HTTP API and its background cleaning routines.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/network-monitor/store"
	nm "github.com/skycoin/skywire/pkg/network-monitor/types"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/transport"
)

// mockData configures the responses returned by the mock services server.
type mockData struct {
	uptimes    []uptimes
	transports []*transport.Entry
	dmsgd      []string
	ar         visorTransports
	sd         []struct {
		Address string `json:"address"`
	}
	deregistered map[string]int // path -> hit count
}

// newMockServer spins up a single httptest server answering every upstream
// endpoint the API talks to (UT, TPD, DMSGD, AR, SD).
func newMockServer(t *testing.T, data *mockData) *httptest.Server {
	t.Helper()
	if data.deregistered == nil {
		data.deregistered = make(map[string]int)
	}
	writeJSON := func(w http.ResponseWriter, v any) {
		require.NoError(t, json.NewEncoder(w).Encode(v))
	}

	r := chi.NewRouter()
	r.Get("/uptimes", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, data.uptimes) })
	r.Get("/all-transports", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, data.transports) })
	r.Get("/dmsg-discovery/visorEntries", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, data.dmsgd) })
	r.Get("/transports", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, data.ar) })
	r.Get("/api/services", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, data.sd) })

	count := func(path string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			data.deregistered[path]++
			w.WriteHeader(http.StatusOK)
		}
	}
	r.Delete("/transports/deregister", count("tpd"))
	r.Delete("/dmsg-discovery/deregister", count("dmsgd"))
	r.Delete("/deregister/{sType}", count("ar"))
	r.Delete("/api/services/deregister/{sType}", count("sd"))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// newTestAPI builds an API wired to a single backing URL for every service.
func newTestAPI(t *testing.T, url string) (*API, store.Store) {
	t.Helper()
	s, err := store.New(storeconfig.Config{Type: storeconfig.Memory})
	require.NoError(t, err)

	urls := ServicesURLs{TPD: url, DMSGD: url, SD: url, AR: url, UT: url}
	api := New(s, logging.MustGetLogger("nm-test"), urls, NetworkMonitorConfig{CleaningDelay: 0})
	return api, s
}

// TestHealth verifies the /health endpoint reports the service name.
func TestHealth(t *testing.T) {
	api, _ := newTestAPI(t, "http://example")
	srv := httptest.NewServer(api)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer res.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var hc HealthCheckResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&hc))
	assert.Equal(t, "network-monitor", hc.ServiceName)
}

// TestGetStatus verifies the /status endpoint returns the stored network status.
func TestGetStatus(t *testing.T) {
	api, s := newTestAPI(t, "http://example")
	require.NoError(t, s.SetNetworkStatus(nm.Status{OnlineVisors: 3, Transports: 7}))

	srv := httptest.NewServer(api)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/status")
	require.NoError(t, err)
	defer res.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var status nm.Status
	require.NoError(t, json.NewDecoder(res.Body).Decode(&status))
	assert.Equal(t, 3, status.OnlineVisors)
	assert.Equal(t, 7, status.Transports)
}

// TestWriteError verifies the status-code mapping for the three error classes.
func TestWriteError(t *testing.T) {
	api, _ := newTestAPI(t, "http://example")

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"deadline", context.DeadlineExceeded, http.StatusRequestTimeout},
		{"syntax", &json.SyntaxError{}, http.StatusBadRequest},
		{"generic", fmt.Errorf("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			api.writeError(w, r, tc.err)
			assert.Equal(t, tc.want, w.Code)
		})
	}
}

// TestInitCleaning verifies every tracked service gets an empty pending-deaths
// map, and that "ar"/"sd" aggregators do not.
func TestInitCleaning(t *testing.T) {
	api, _ := newTestAPI(t, "http://example")
	api.initCleaning()

	for _, svc := range []string{"tpd", "dmsgd", "vpn", "visor", "skysocks", "sudph", "stcpr"} {
		_, ok := api.pendingDeaths[svc]
		assert.True(t, ok, "expected pending-deaths map for %s", svc)
	}
	_, hasAR := api.pendingDeaths["ar"]
	_, hasSD := api.pendingDeaths["sd"]
	assert.False(t, hasAR)
	assert.False(t, hasSD)
}

// TestUpdateNetworkStatus verifies the live/dead bookkeeping is summarized into
// the stored status.
func TestUpdateNetworkStatus(t *testing.T) {
	api, s := newTestAPI(t, "http://example")
	api.liveEntries = map[string]int{"tpd": 2, "vpn": 1, "visor": 3, "skysocks": 4}
	api.deadEntries = map[string][]string{
		"dmsgd": {"a"},
		"tpd":   {"b", "c"},
		"sudph": {"d"},
		"stcpr": {"e", "f"},
		"vpn":   {"g"},
	}
	api.utData = map[string]bool{"v1": true, "v2": true}

	require.NoError(t, api.updateNetworkStatus())

	status, err := s.GetNetworkStatus()
	require.NoError(t, err)
	assert.Equal(t, 2, status.Transports)
	assert.Equal(t, 1, status.VPN)
	assert.Equal(t, 3, status.PublicVisor)
	assert.Equal(t, 4, status.Skysocks)
	assert.Equal(t, 2, status.OnlineVisors)
	assert.Equal(t, 2, status.LastCleaning.Tpd)
	assert.Equal(t, 1, status.LastCleaning.Dmsgd)
	assert.Equal(t, 1, status.LastCleaning.Ar.SUDPH)
	assert.Equal(t, 2, status.LastCleaning.Ar.STCPR)
	// total dead = 1+2+1+2+1 = 7
	assert.Equal(t, 7, status.LastCleaning.AllDeadEntriesCleaned)
}

// TestFetchUTData covers the happy path plus the empty, bad-json, request-error
// and canceled-context failure modes.
func TestFetchUTData(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		data := &mockData{uptimes: []uptimes{{Key: "v1", Online: true}, {Key: "v2", Online: false}}}
		srv := newMockServer(t, data)
		api, _ := newTestAPI(t, srv.URL)

		require.NoError(t, api.fetchUTData(context.Background()))
		assert.Len(t, api.utData, 2)
		assert.True(t, api.utData["v1"])
	})

	t.Run("empty is error", func(t *testing.T) {
		srv := newMockServer(t, &mockData{uptimes: []uptimes{}})
		api, _ := newTestAPI(t, srv.URL)
		assert.Error(t, api.fetchUTData(context.Background()))
	})

	t.Run("canceled context", func(t *testing.T) {
		api, _ := newTestAPI(t, "http://example")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.Equal(t, context.DeadlineExceeded, api.fetchUTData(ctx))
	})

	t.Run("request error", func(t *testing.T) {
		api, _ := newTestAPI(t, "http://127.0.0.1:0")
		assert.Error(t, api.fetchUTData(context.Background()))
	})
}

// TestFetchServiceData covers the SD, AR and DMSGD fetchers, happy and
// canceled-context paths.
func TestFetchServiceData(t *testing.T) {
	data := &mockData{
		sd: []struct {
			Address string `json:"address"`
		}{{Address: "pk1:44"}, {Address: "pk2:44"}},
		ar:    visorTransports{Sudph: []string{"s1"}, Stcpr: []string{"r1", "r2"}},
		dmsgd: []string{"d1", "d2", "d3"},
	}
	srv := newMockServer(t, data)
	api, _ := newTestAPI(t, srv.URL)
	ctx := context.Background()

	t.Run("sd", func(t *testing.T) {
		got, err := api.fetchSdData(ctx, "vpn")
		require.NoError(t, err)
		assert.Equal(t, []string{"pk1", "pk2"}, got)
	})

	t.Run("ar stcpr", func(t *testing.T) {
		got, err := api.fetchArData(ctx, "stcpr")
		require.NoError(t, err)
		assert.Equal(t, []string{"r1", "r2"}, got)
	})

	t.Run("ar sudph", func(t *testing.T) {
		got, err := api.fetchArData(ctx, "sudph")
		require.NoError(t, err)
		assert.Equal(t, []string{"s1"}, got)
	})

	t.Run("dmsgd", func(t *testing.T) {
		got, err := api.fetchDmsgdData(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"d1", "d2", "d3"}, got)
	})

	t.Run("canceled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := api.fetchSdData(cctx, "vpn")
		assert.Equal(t, context.DeadlineExceeded, err)
		_, err = api.fetchArData(cctx, "stcpr")
		assert.Equal(t, context.DeadlineExceeded, err)
		_, err = api.fetchDmsgdData(cctx)
		assert.Equal(t, context.DeadlineExceeded, err)
	})
}

// TestCheckingEntries verifies an offline entry becomes pending on the first
// pass and dead once it is already pending.
func TestCheckingEntries(t *testing.T) {
	api, _ := newTestAPI(t, "http://example")
	api.initCleaning()
	api.utData = map[string]bool{"online": true}

	// First pass: "offline" is unknown to UT, so it becomes pending (not dead).
	require.NoError(t, api.checkingEntries(context.Background(), []string{"online", "offline"}, "sd", "vpn"))
	assert.Contains(t, api.pendingDeaths["vpn"], "offline")
	assert.Empty(t, api.deadEntries["vpn"])

	// Second pass: already pending → moves to dead.
	require.NoError(t, api.checkingEntries(context.Background(), []string{"offline"}, "sd", "vpn"))
	assert.Contains(t, api.deadEntries["vpn"], "offline")

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.Equal(t, context.DeadlineExceeded, api.checkingEntries(ctx, nil, "sd", "vpn"))
	})
}

// TestCleaningInfo exercises the logging helper, including its canceled path.
func TestCleaningInfo(t *testing.T) {
	api, _ := newTestAPI(t, "http://example")
	api.initCleaning()
	assert.NoError(t, api.cleaningInfo(context.Background(), "sd", "vpn"))
	assert.NoError(t, api.cleaningInfo(context.Background(), "dmsgd", ""))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, context.DeadlineExceeded, api.cleaningInfo(ctx, "sd", "vpn"))
}

// TestTpdCleaning verifies dead transports are detected (one edge offline,
// already pending) and deregistered.
func TestTpdCleaning(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	id := uuid.New()
	entry := &transport.Entry{ID: id, Edges: [2]cipher.PubKey{pk1, pk2}}

	data := &mockData{transports: []*transport.Entry{entry}}
	srv := newMockServer(t, data)
	api, _ := newTestAPI(t, srv.URL)
	api.initCleaning()
	api.utData = map[string]bool{} // both edges offline
	api.pendingDeaths["tpd"][id.String()] = true

	require.NoError(t, api.tpdCleaning(context.Background()))
	assert.Contains(t, api.deadEntries["tpd"], id.String())
	assert.Equal(t, 1, data.deregistered["tpd"])

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.Equal(t, context.DeadlineExceeded, api.tpdCleaning(ctx))
	})
}

// TestCleaningServiceWithDeregister drives cleaningService end-to-end so a dead
// entry triggers a deregister call.
func TestCleaningServiceWithDeregister(t *testing.T) {
	data := &mockData{dmsgd: []string{"deadpk"}}
	srv := newMockServer(t, data)
	api, _ := newTestAPI(t, srv.URL)
	api.initCleaning()
	api.utData = map[string]bool{}              // deadpk is offline
	api.pendingDeaths["dmsgd"]["deadpk"] = true // already pending → dies this pass

	require.NoError(t, api.cleaningService(context.Background(), "dmsgd", ""))
	assert.Contains(t, api.deadEntries["dmsgd"], "deadpk")
	assert.Equal(t, 1, data.deregistered["dmsgd"])
}

// TestDeregisterRequestBadURL verifies an unparseable URL is reported as an
// error.
func TestDeregisterRequestBadURL(t *testing.T) {
	api, _ := newTestAPI(t, "http://example")
	err := api.deregisterRequest([]string{"a"}, "://bad-url", "tpd")
	assert.Error(t, err)
}

// TestDeregisterNonOKStatus verifies a non-200 deregister response is an error.
func TestDeregisterNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	api, _ := newTestAPI(t, "http://example")
	err := api.deregisterRequest([]string{"a"}, srv.URL+"/x", "tpd")
	assert.Error(t, err)
}

// TestCleanNetwork drives a full cleaning cycle across every main service.
func TestCleanNetwork(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	data := &mockData{
		uptimes:    []uptimes{{Key: pk1.Hex(), Online: true}},
		transports: []*transport.Entry{{ID: uuid.New(), Edges: [2]cipher.PubKey{pk1, pk2}}},
		dmsgd:      []string{pk1.Hex()},
		ar:         visorTransports{Sudph: []string{pk1.Hex()}, Stcpr: []string{pk1.Hex()}},
		sd: []struct {
			Address string `json:"address"`
		}{{Address: pk1.Hex() + ":44"}},
	}
	srv := newMockServer(t, data)
	api, _ := newTestAPI(t, srv.URL)
	api.initCleaning()

	require.NoError(t, api.cleanNetwork(context.Background()))
	// First pass never kills anything, so no deregistration should have fired.
	assert.Empty(t, data.deregistered)

	// updateNetworkStatus should run cleanly off the populated bookkeeping.
	require.NoError(t, api.updateNetworkStatus())
}

// TestCleanNetworkUTError verifies a failing UT fetch aborts the cycle.
func TestCleanNetworkUTError(t *testing.T) {
	srv := newMockServer(t, &mockData{uptimes: []uptimes{}}) // empty → error
	api, _ := newTestAPI(t, srv.URL)
	api.initCleaning()
	assert.Error(t, api.cleanNetwork(context.Background()))
}
