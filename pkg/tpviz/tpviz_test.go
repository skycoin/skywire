// Package tpviz pkg/tpviz/tpviz_test.go
package tpviz

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// ---- fakes -----------------------------------------------------------------

// fakeVisorAPI is a hand-written stub of the VisorAPI interface so handler
// tests can drive both success and error paths without a real visor.
type fakeVisorAPI struct {
	apps        []*AppState
	appsErr     error
	startErr    error
	stopErr     error
	autoErr     error
	setPKErr    error
	pingResp    *PingResponse
	pingErr     error
	dmsgHealth  *DMSGHealthResponse
	dmsgErr     error
	overview    *VisorOverview
	overviewErr error

	startedApp  string
	stoppedApp  string
	autoApp     string
	autoVal     bool
	setPKApp    string
	setPKVal    string
	closeCalled bool
}

func (f *fakeVisorAPI) Overview() (*VisorOverview, error)     { return f.overview, f.overviewErr }
func (f *fakeVisorAPI) RoutingRules() ([]routing.Rule, error) { return nil, nil }
func (f *fakeVisorAPI) AddTransport(_ context.Context, _, _ string) (*TransportSummary, error) {
	return &TransportSummary{}, nil
}
func (f *fakeVisorAPI) RemoveTransport(_ context.Context, _ string) error { return nil }
func (f *fakeVisorAPI) DMSGHealth(_ context.Context, _ string) (*DMSGHealthResponse, error) {
	return f.dmsgHealth, f.dmsgErr
}
func (f *fakeVisorAPI) Ping(_ context.Context, _ string, _, _ bool, _, _ int) (*PingResponse, error) {
	return f.pingResp, f.pingErr
}
func (f *fakeVisorAPI) Apps() ([]*AppState, error) { return f.apps, f.appsErr }
func (f *fakeVisorAPI) StartApp(name string) error { f.startedApp = name; return f.startErr }
func (f *fakeVisorAPI) StopApp(name string) error  { f.stoppedApp = name; return f.stopErr }
func (f *fakeVisorAPI) SetAutoStart(name string, v bool) error {
	f.autoApp, f.autoVal = name, v
	return f.autoErr
}
func (f *fakeVisorAPI) SetAppPK(name, pk string) error {
	f.setPKApp, f.setPKVal = name, pk
	return f.setPKErr
}
func (f *fakeVisorAPI) Close() error { f.closeCalled = true; return nil }

// fakeTPSAPI is a hand-written stub of the TPSAPI interface.
type fakeTPSAPI struct {
	pk string
}

func (f *fakeTPSAPI) AddTransport(_ context.Context, _, _, _ string) (*TPSTransportResponse, error) {
	return &TPSTransportResponse{}, nil
}
func (f *fakeTPSAPI) RemoveTransport(_ context.Context, _, _ string) error { return nil }
func (f *fakeTPSAPI) GetTransports(_ context.Context, _ string) ([]TPSTransportResponse, error) {
	return nil, nil
}
func (f *fakeTPSAPI) PK() string { return f.pk }

// testServer returns a Server with caching disabled and routes wired.
func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.NoCache = true
	cfg.AutoRefresh = false
	return NewServer(cfg)
}

// decodeBody decodes a JSON response body into a generic map.
func decodeMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m))
	return m
}

// ---- pure functions --------------------------------------------------------

func TestCacheDirFromURL(t *testing.T) {
	require.Equal(t, filepath.Join(os.TempDir(), "tpd.example.com"),
		CacheDirFromURL("http://tpd.example.com"))
	require.Equal(t, filepath.Join(os.TempDir(), "host:8080"),
		CacheDirFromURL("http://host:8080/path"))

	// No host -> empty.
	require.Equal(t, "", CacheDirFromURL(""))
	require.Equal(t, "", CacheDirFromURL("not-a-url"))
	require.Equal(t, "", CacheDirFromURL("://bad"))
}

func TestCacheFilePath(t *testing.T) {
	dir := t.TempDir()

	// Empty cache dir disables caching.
	require.Equal(t, "", CacheFilePath("", "http://x/y"))

	// type query wins.
	require.Equal(t, filepath.Join(dir, "visor.json"),
		CacheFilePath(dir, "http://sd/api/services?type=visor"))

	// last path segment used as name.
	require.Equal(t, filepath.Join(dir, "all-transports.json"),
		CacheFilePath(dir, "http://tpd/all-transports"))

	// trailing slash trimmed.
	require.Equal(t, filepath.Join(dir, "entries.json"),
		CacheFilePath(dir, "http://dmsg/dmsg-discovery/entries/"))

	// no path -> "cache.json".
	require.Equal(t, filepath.Join(dir, "cache.json"),
		CacheFilePath(dir, "http://host"))

	// directory was created as a side effect.
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	require.Equal(t, "127.0.0.1", cfg.Addr)
	require.Equal(t, 8080, cfg.Port)
	require.Equal(t, 5, cfg.CacheMaxAge)
	require.True(t, cfg.AutoRefresh)
	require.NotEmpty(t, cfg.TPDURL)
	require.NotEmpty(t, cfg.SDURL)
	require.NotEmpty(t, cfg.DMSGURL)
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data.json")
	require.NoError(t, os.WriteFile(f, []byte("hello"), 0o600))

	got, err := readFile(f)
	require.NoError(t, err)
	require.Equal(t, "hello", got)

	_, err = readFile(filepath.Join(dir, "missing"))
	require.Error(t, err)
}

func TestFetchURL(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
		}))
		defer srv.Close()

		got, err := fetchURL(srv.URL)
		require.NoError(t, err)
		require.Equal(t, `{"ok":true}`, got)
	})

	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusBadGateway)
		}))
		defer srv.Close()

		_, err := fetchURL(srv.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "502")
	})

	t.Run("bad url", func(t *testing.T) {
		_, err := fetchURL("http://127.0.0.1:0")
		require.Error(t, err)
	})
}

func TestParseServices(t *testing.T) {
	s := testServer(t)
	services := make(map[string]ServiceInfo)

	proxy := `[{"address":"pkA:1080","geo":{"country":"US"}},{"address":"pkB:1081","geo":{"country":""}}]`
	s.parseServices(proxy, "proxy", services)
	require.Len(t, services, 2)
	require.Equal(t, "US", services["pkA"].Country)
	require.Equal(t, []string{"proxy"}, services["pkA"].Services)

	// Same PK from another service type appends and back-fills country.
	vpn := `[{"address":"pkB:1082","geo":{"country":"DE"}}]`
	s.parseServices(vpn, "vpn", services)
	require.Equal(t, []string{"vpn"}, services["pkB"].Services[1:])
	require.Equal(t, "DE", services["pkB"].Country)

	// Address with no port is used verbatim as PK.
	s.parseServices(`[{"address":"pkC"}]`, "visor", services)
	require.Equal(t, "pkC", services["pkC"].PK)

	// Malformed JSON is a silent no-op.
	before := len(services)
	s.parseServices("not json", "proxy", services)
	require.Len(t, services, before)
}

func TestGetCacheAgeSeconds(t *testing.T) {
	s := testServer(t)
	s.config.CacheMaxAge = 5

	// Empty path -> 0.
	require.Equal(t, int64(0), s.getCacheAgeSeconds(""))

	// Missing file -> max age in seconds.
	require.Equal(t, int64(5*60), s.getCacheAgeSeconds(filepath.Join(t.TempDir(), "nope")))

	// Existing fresh file -> small, non-negative age.
	f := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	age := s.getCacheAgeSeconds(f)
	require.GreaterOrEqual(t, age, int64(0))
	require.Less(t, age, int64(60))
}

func TestGetData(t *testing.T) {
	t.Run("no cache fetches directly", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("live")) //nolint:errcheck
		}))
		defer srv.Close()

		s := testServer(t) // NoCache true
		got, err := s.getData("", srv.URL)
		require.NoError(t, err)
		require.Equal(t, "live", got)
	})

	t.Run("writes and reads cache file", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("cached-content")) //nolint:errcheck
		}))
		defer srv.Close()

		s := testServer(t)
		s.config.NoCache = false
		cacheFile := filepath.Join(t.TempDir(), "tp.json")

		// First call: file missing -> synchronous fetch + write.
		got, err := s.getData(cacheFile, srv.URL)
		require.NoError(t, err)
		require.Equal(t, "cached-content", got)

		// File now exists with the fetched content.
		data, err := os.ReadFile(cacheFile)
		require.NoError(t, err)
		require.Equal(t, "cached-content", string(data))

		// Second call: fresh file -> served from cache (server can be down).
		srv.Close()
		got, err = s.getData(cacheFile, srv.URL)
		require.NoError(t, err)
		require.Equal(t, "cached-content", got)
	})
}

func TestSdCacheFileAndDmsgCacheFiles(t *testing.T) {
	s := testServer(t)
	dir := t.TempDir()
	s.config.CacheDirSD = dir
	s.config.SDURL = "http://sd.example.com"
	require.Equal(t, filepath.Join(dir, "services.json"), s.sdCacheFile())

	s.config.CacheDirDMSG = dir
	s.config.DMSGURL = "http://dmsg.example.com"
	servers, entries, clients := s.dmsgCacheFiles()
	require.Equal(t, filepath.Join(dir, "all_servers.json"), servers)
	require.Equal(t, filepath.Join(dir, "entries.json"), entries)
	require.Equal(t, filepath.Join(dir, "clients.json"), clients)

	// Disabled SD cache.
	s.config.CacheDirSD = ""
	require.Equal(t, "", s.sdCacheFile())
}

func TestGetEmbeddedIndexWithNavLinks(t *testing.T) {
	// No nav links: returns index unmodified.
	plain, err := GetEmbeddedIndexWithNavLinks(nil)
	require.NoError(t, err)
	require.NotEmpty(t, plain)

	// With nav links: injects an anchor.
	withLinks, err := GetEmbeddedIndexWithNavLinks([]NavLink{
		{URL: "/foo", Label: "Foo"},
	})
	require.NoError(t, err)
	require.Contains(t, withLinks, `href="/foo"`)
	require.Contains(t, withLinks, "Foo")
	require.Contains(t, withLinks, "nav-links")
}

// ---- server setup ----------------------------------------------------------

func TestNewServerAndSetters(t *testing.T) {
	s := testServer(t)
	require.NotNil(t, s.Handler())

	// SetVisorAPI marks embedded mode and seeds the cache; replacing it
	// closes the previous API.
	first := &fakeVisorAPI{}
	s.SetVisorAPI(first, "PK1")
	require.True(t, s.embeddedMode)
	require.NotNil(t, s.visorCache)
	require.Equal(t, "PK1", s.visorCache.PubKey)

	second := &fakeVisorAPI{}
	s.SetVisorAPI(second, "PK2")
	require.True(t, first.closeCalled)
	require.Equal(t, "PK2", s.visorCache.PubKey)

	// SetTPSAPI stores the API.
	tps := &fakeTPSAPI{pk: "TPSPK"}
	s.SetTPSAPI(tps)
	require.Equal(t, tps, s.tpsAPI)
}

// ---- HTTP handlers ---------------------------------------------------------

func TestStaticRoutes(t *testing.T) {
	s := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	t.Run("index", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/")
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	})

	t.Run("unknown path 404s", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/does-not-exist")
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("textures empty filename 404s", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/textures/")
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestHandleHealth(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/health", "/api/health"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		s.Handler().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "ok", body["status"])
		require.Equal(t, float64(5), body["cache_max_age"])
	}
}

func TestHandleServices_LiveFallback(t *testing.T) {
	// SD server returns proxy/vpn/visor entries depending on ?type.
	sd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("type") {
		case "proxy":
			w.Write([]byte(`[{"address":"pkA:1080","geo":{"country":"US"}}]`)) //nolint:errcheck
		default:
			w.Write([]byte(`[]`)) //nolint:errcheck
		}
	}))
	defer sd.Close()

	s := testServer(t)
	s.config.SDURL = sd.URL
	s.config.CacheDirSD = "" // force live fallback

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	s.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))

	var services map[string]ServiceInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &services))
	require.Contains(t, services, "pkA")
	require.Equal(t, "US", services["pkA"].Country)
}

func TestHandleTransports(t *testing.T) {
	tpd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"t_id":"x"}]`)) //nolint:errcheck
	}))
	defer tpd.Close()

	s := testServer(t)
	s.config.TPDURL = tpd.URL

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/transports", nil)
	s.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `[{"t_id":"x"}]`, rec.Body.String())
}

func TestHandleTransports_Error(t *testing.T) {
	s := testServer(t)
	s.config.TPDURL = "http://127.0.0.1:0" // unreachable

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/transports", nil)
	s.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleIPGroups(t *testing.T) {
	s := testServer(t)

	// No cache -> disabled with empty groups.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ip-groups", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeMap(t, rec.Body.Bytes())
	require.Equal(t, false, body["enabled"])

	// With cache populated.
	s.ipGroupsMu.Lock()
	s.ipGroupsCache = &ipGroupsResponse{Enabled: true, Groups: map[string]int{"g": 2}}
	s.ipGroupsMu.Unlock()

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ip-groups", nil))
	body = decodeMap(t, rec.Body.Bytes())
	require.Equal(t, true, body["enabled"])
}

func TestHandleLocalVisor(t *testing.T) {
	s := testServer(t)

	// No visor API -> disconnected.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/local-visor", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeMap(t, rec.Body.Bytes())
	require.Equal(t, false, body["connected"])
}

func TestHandleTPSStatus(t *testing.T) {
	s := testServer(t)

	// Not running.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tps/status", nil))
	body := decodeMap(t, rec.Body.Bytes())
	require.Equal(t, false, body["running"])

	// Running with PK.
	s.SetTPSAPI(&fakeTPSAPI{pk: "TPSPK"})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tps/status", nil))
	body = decodeMap(t, rec.Body.Bytes())
	require.Equal(t, true, body["running"])
	require.Equal(t, "TPSPK", body["tps_pk"])
}

func TestHandleTPSAddTransport(t *testing.T) {
	s := testServer(t)

	t.Run("TPS not running", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/tps/add-transport",
			strings.NewReader(`{"target_pk":"a","remote_pk":"b","type":"stcpr"}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("OPTIONS preflight", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/api/tps/add-transport", nil)
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
	})

	t.Run("wrong method", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/tps/add-transport", nil)
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})

	t.Run("invalid type rejected", func(t *testing.T) {
		s.SetTPSAPI(&fakeTPSAPI{pk: "PK"})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/tps/add-transport",
			strings.NewReader(`{"target_pk":"a","remote_pk":"b","type":"dmsg"}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("success", func(t *testing.T) {
		s.SetTPSAPI(&fakeTPSAPI{pk: "PK"})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/tps/add-transport",
			strings.NewReader(`{"target_pk":"a","remote_pk":"b","type":"stcpr"}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestHandlePing(t *testing.T) {
	s := testServer(t)

	t.Run("missing pk", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "error", body["status"])
	})

	t.Run("visor not connected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ping?pk=abc", nil))
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "error", body["status"])
		require.Contains(t, body["error"], "visor not connected")
	})

	t.Run("success", func(t *testing.T) {
		s.SetVisorAPI(&fakeVisorAPI{
			pingResp: &PingResponse{Status: "ok", Mode: "dmsg", AvgMs: 12.5},
		}, "PK")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ping?pk=abc&tries=2&size=4", nil))
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "ok", body["status"])
	})

	t.Run("visor error", func(t *testing.T) {
		s.SetVisorAPI(&fakeVisorAPI{pingErr: errors.New("ping boom")}, "PK")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ping?pk=abc", nil))
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "error", body["status"])
		require.Contains(t, body["error"], "ping boom")
	})
}

func TestHandleApps(t *testing.T) {
	t.Run("not connected", func(t *testing.T) {
		s := testServer(t)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/apps", nil))
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "visor not connected", body["error"])
	})

	t.Run("success", func(t *testing.T) {
		s := testServer(t)
		s.SetVisorAPI(&fakeVisorAPI{
			apps: []*AppState{{Name: "vpn-client", Status: 1, Port: 3}},
		}, "PK")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/apps", nil))
		require.Equal(t, http.StatusOK, rec.Code)

		var apps []*AppState
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &apps))
		require.Len(t, apps, 1)
		require.Equal(t, "vpn-client", apps[0].Name)
	})

	t.Run("error", func(t *testing.T) {
		s := testServer(t)
		s.SetVisorAPI(&fakeVisorAPI{appsErr: errors.New("apps boom")}, "PK")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/apps", nil))
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "apps boom", body["error"])
	})
}

func TestHandleAppStart(t *testing.T) {
	t.Run("wrong method", func(t *testing.T) {
		s := testServer(t)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/apps/start", nil))
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "POST required", body["error"])
	})

	t.Run("invalid body", func(t *testing.T) {
		s := testServer(t)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/apps/start", strings.NewReader("{bad"))
		s.Handler().ServeHTTP(rec, req)
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "invalid request body", body["error"])
	})

	t.Run("not connected", func(t *testing.T) {
		s := testServer(t)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/apps/start", strings.NewReader(`{"name":"vpn"}`))
		s.Handler().ServeHTTP(rec, req)
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "visor not connected", body["error"])
	})

	t.Run("success", func(t *testing.T) {
		s := testServer(t)
		fv := &fakeVisorAPI{}
		s.SetVisorAPI(fv, "PK")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/apps/start", strings.NewReader(`{"name":"vpn"}`))
		s.Handler().ServeHTTP(rec, req)
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "started", body["status"])
		require.Equal(t, "vpn", fv.startedApp)
	})

	t.Run("visor error", func(t *testing.T) {
		s := testServer(t)
		s.SetVisorAPI(&fakeVisorAPI{startErr: errors.New("start boom")}, "PK")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/apps/start", strings.NewReader(`{"name":"vpn"}`))
		s.Handler().ServeHTTP(rec, req)
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, "start boom", body["error"])
	})
}

func TestHandleAppStop(t *testing.T) {
	s := testServer(t)
	fv := &fakeVisorAPI{}
	s.SetVisorAPI(fv, "PK")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/apps/stop", strings.NewReader(`{"name":"skysocks"}`))
	s.Handler().ServeHTTP(rec, req)
	body := decodeMap(t, rec.Body.Bytes())
	require.Equal(t, "stopped", body["status"])
	require.Equal(t, "skysocks", fv.stoppedApp)
}

func TestHandleAppAutoStart(t *testing.T) {
	s := testServer(t)
	fv := &fakeVisorAPI{}
	s.SetVisorAPI(fv, "PK")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/apps/autostart",
		strings.NewReader(`{"name":"vpn","auto_start":true}`))
	s.Handler().ServeHTTP(rec, req)
	body := decodeMap(t, rec.Body.Bytes())
	require.Equal(t, "updated", body["status"])
	require.Equal(t, true, body["auto_start"])
	require.Equal(t, "vpn", fv.autoApp)
	require.True(t, fv.autoVal)
}

func TestHandleAppSetPK(t *testing.T) {
	s := testServer(t)
	fv := &fakeVisorAPI{}
	s.SetVisorAPI(fv, "PK")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/apps/set-pk",
		strings.NewReader(`{"name":"vpn","pk":"remotePK"}`))
	s.Handler().ServeHTTP(rec, req)
	body := decodeMap(t, rec.Body.Bytes())
	require.Equal(t, "updated", body["status"])
	require.Equal(t, "remotePK", fv.setPKVal)
	require.Equal(t, "vpn", fv.setPKApp)
}

func TestHandleDMSGHealth(t *testing.T) {
	s := testServer(t)

	// Not connected.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dmsg/health?pk=abc", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	// Connected with a health response.
	s.SetVisorAPI(&fakeVisorAPI{
		dmsgHealth: &DMSGHealthResponse{Status: "healthy"},
	}, "PK")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dmsg/health?pk=abc", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleUptimes(t *testing.T) {
	ut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"uptimes":[]}`)) //nolint:errcheck
	}))
	defer ut.Close()

	s := testServer(t)
	s.config.UTURL = ut.URL

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/uptimes", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `{"uptimes":[]}`, rec.Body.String())
}

func TestHandleServices_CachedSD(t *testing.T) {
	s := testServer(t)
	s.config.NoCache = false
	dir := t.TempDir()
	s.config.CacheDirSD = dir
	s.config.SDURL = "http://sd.example.com"

	// Seed a fresh SD cache file at the path sdCacheFile() expects.
	cacheFile := s.sdCacheFile()
	require.NotEmpty(t, cacheFile)
	require.NoError(t, os.WriteFile(cacheFile, []byte(`{"pkA":{"pk":"pkA","services":["vpn"]}}`), 0o600))

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/services", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "pkA")
}

// newDMSGServer returns an httptest server speaking the three DMSG-D
// sub-endpoints used by getDMSGData, plus a geoip endpoint.
func newDMSGStack(t *testing.T) (dmsgURL, geoURL string, closeFn func()) {
	t.Helper()
	dmsg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/all_servers"):
			w.Write([]byte(`[{"static":"srvPK","server":{"address":"1.2.3.4:8080","availableSessions":5,"serverType":"public"}}]`)) //nolint:errcheck
		case strings.HasSuffix(r.URL.Path, "/entries"):
			w.Write([]byte(`["e1","e2","e3"]`)) //nolint:errcheck
		case strings.HasSuffix(r.URL.Path, "/servers/clients"):
			w.Write([]byte(`{"srvPK":["c1","c2"]}`)) //nolint:errcheck
		default:
			w.Write([]byte(`[]`)) //nolint:errcheck
		}
	}))
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"country_code":"US"}`)) //nolint:errcheck
	}))
	return dmsg.URL, geo.URL, func() { dmsg.Close(); geo.Close() }
}

func TestDMSGEndpoints(t *testing.T) {
	dmsgURL, geoURL, closeFn := newDMSGStack(t)
	defer closeFn()

	s := testServer(t) // NoCache true -> getDMSGSubData fetches directly
	s.config.DMSGURL = dmsgURL
	s.config.GeoIPURL = geoURL

	t.Run("servers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dmsg/servers", nil))
		require.Equal(t, http.StatusOK, rec.Code)

		var data DMSGData
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &data))
		require.Len(t, data.Servers, 1)
		require.Equal(t, "srvPK", data.Servers[0].PK)
		require.Equal(t, "1.2.3.4", data.Servers[0].IP)
		require.Equal(t, "US", data.Servers[0].Country)
		require.Equal(t, []string{"c1", "c2"}, data.Servers[0].Clients)
	})

	t.Run("entries", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dmsg/entries", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		body := decodeMap(t, rec.Body.Bytes())
		require.Equal(t, float64(3), body["count"])
	})

	t.Run("servers/clients", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dmsg/servers/clients", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "srvPK")
	})

	t.Run("servers/clients not configured", func(t *testing.T) {
		s2 := testServer(t)
		s2.config.DMSGURL = ""
		rec := httptest.NewRecorder()
		s2.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dmsg/servers/clients", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}

func TestFetchGeoForIP(t *testing.T) {
	// Not configured.
	s := testServer(t)
	s.config.GeoIPURL = ""
	_, err := s.fetchGeoForIP("1.2.3.4")
	require.Error(t, err)

	// Configured.
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"country_code":"DE"}`)) //nolint:errcheck
	}))
	defer geo.Close()
	s.config.GeoIPURL = geo.URL
	country, err := s.fetchGeoForIP("1.2.3.4")
	require.NoError(t, err)
	require.Equal(t, "DE", country)
}

func TestRefreshCache(t *testing.T) {
	tpd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[]`)) //nolint:errcheck
	}))
	defer tpd.Close()
	ut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	defer ut.Close()

	s := testServer(t)
	s.config.NoCache = false
	dir := t.TempDir()
	s.config.CacheDirTPD = filepath.Join(dir, "tpd")
	s.config.CacheDirUT = filepath.Join(dir, "ut")
	s.config.TPDURL = tpd.URL
	s.config.UTURL = ut.URL
	// Disable the SD/DMSG sub-refreshes that refreshCache also triggers so
	// the test exercises only the TPD/UT path and never touches the network.
	s.config.CacheDirSD = ""
	s.config.DMSGURL = ""

	s.refreshCache()

	// Both cache files written.
	require.FileExists(t, CacheFilePath(s.config.CacheDirTPD, s.config.TPDURL+"/all-transports"))
	require.FileExists(t, CacheFilePath(s.config.CacheDirUT, s.config.UTURL+"/uptimes?v=v2"))
}

func TestRefreshSDCache(t *testing.T) {
	sd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"address":"pkA:1080","geo":{"country":"US"}}]`)) //nolint:errcheck
	}))
	defer sd.Close()

	s := testServer(t)
	s.config.NoCache = false
	s.config.CacheDirSD = t.TempDir()
	s.config.SDURL = sd.URL

	s.refreshSDCache()
	require.FileExists(t, s.sdCacheFile())

	// Disabled SD cache is a no-op (no panic).
	s.config.CacheDirSD = ""
	s.refreshSDCache()
}

func TestRefreshDMSGCache(t *testing.T) {
	dmsgURL, geoURL, closeFn := newDMSGStack(t)
	defer closeFn()

	s := testServer(t)
	s.config.NoCache = false
	s.config.CacheDirDMSG = t.TempDir()
	s.config.DMSGURL = dmsgURL
	s.config.GeoIPURL = geoURL

	s.refreshDMSGCache()
	servers, _, _ := s.dmsgCacheFiles()
	require.FileExists(t, servers)

	// Disabled -> no-op.
	s.config.DMSGURL = ""
	s.refreshDMSGCache()
}

func TestHandleLocalVisor_Connected(t *testing.T) {
	s := testServer(t)
	s.SetVisorAPI(&fakeVisorAPI{
		overview: &VisorOverview{RoutesCount: 2},
	}, "PKlocal")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/local-visor", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeMap(t, rec.Body.Bytes())
	require.Equal(t, true, body["connected"])
	require.Equal(t, float64(2), body["routes_count"])
}

func TestLocalTransportHandlers_NotConnected(t *testing.T) {
	s := testServer(t) // no visor API

	for _, path := range []string{"/api/local/add-transport", "/api/local/remove-transport"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, path)
	}
}

func TestHandleLocalAddTransport(t *testing.T) {
	s := testServer(t)
	s.SetVisorAPI(&fakeVisorAPI{}, "PK")

	t.Run("missing remote_pk", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/local/add-transport", strings.NewReader(`{}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid type", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/local/add-transport",
			strings.NewReader(`{"remote_pk":"abc","type":"bogus"}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("success", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/local/add-transport",
			strings.NewReader(`{"remote_pk":"abc","type":"stcpr"}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestHandleLocalRemoveTransport(t *testing.T) {
	s := testServer(t)
	s.SetVisorAPI(&fakeVisorAPI{}, "PK")

	t.Run("missing id", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/local/remove-transport", strings.NewReader(`{}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("success", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/local/remove-transport",
			strings.NewReader(`{"id":"tp-id"}`))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestTPSHandlers_NotRunning(t *testing.T) {
	s := testServer(t) // no TPS API

	cases := []struct {
		path   string
		method string
		body   string
	}{
		{"/api/tps/remove-transport", http.MethodPost, `{}`},
		{"/api/tps/refresh-transports", http.MethodGet, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		s.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, c.path)
	}
}

func TestHandleTPSRefreshTransports(t *testing.T) {
	s := testServer(t)
	s.SetTPSAPI(&fakeTPSAPI{pk: "PK"})

	// Missing pk.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tps/refresh-transports", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// With pk -> success (fake returns nil, nil).
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tps/refresh-transports?pk=abc", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestStop(t *testing.T) {
	s := testServer(t)
	// startAutoRefresh is a no-op when AutoRefresh is false; Stop should be
	// safe to call regardless and must close stopChan without panicking.
	s.Stop()

	select {
	case <-s.stopChan:
		// closed as expected
	default:
		t.Fatal("stopChan was not closed by Stop()")
	}
}
