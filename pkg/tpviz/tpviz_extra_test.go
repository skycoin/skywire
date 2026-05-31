// Package tpviz pkg/tpviz/tpviz_extra_test.go
//
// Coverage for the CXO-subscriber integration, geoip/IP-group caches,
// the embedded-visor refresh path, and the server lifecycle helpers.
package tpviz

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

// ---- fake CXO subscription manager -----------------------------------------

type cxoLeaf struct {
	path string
	body []byte
}

// fakeCXOMgr is a hand-written CXOSubMgr that replays a fixed set of
// leaves per feed and records Acquire/Release calls.
type fakeCXOMgr struct {
	leaves   map[int][]cxoLeaf
	walkOK   bool
	acquired int
	released int
}

func (m *fakeCXOMgr) AcquireForTab(_ int) { m.acquired++ }
func (m *fakeCXOMgr) ReleaseForTab(_ int) { m.released++ }
func (m *fakeCXOMgr) Walk(feed int, _ string, fn func(path string, body []byte) bool) bool {
	for _, l := range m.leaves[feed] {
		if !fn(l.path, l.body) {
			break
		}
	}
	return m.walkOK
}

func TestSetCXOSubMgr(t *testing.T) {
	s := testServer(t)
	require.Nil(t, s.cxoMgr())

	mgr := &fakeCXOMgr{walkOK: true}
	s.SetCXOSubMgr(mgr)
	require.Equal(t, mgr, s.cxoMgr())

	// Clearing with nil.
	s.SetCXOSubMgr(nil)
	require.Nil(t, s.cxoMgr())
}

func TestTryCXOServices(t *testing.T) {
	t.Run("no manager", func(t *testing.T) {
		s := testServer(t)
		_, ok := s.tryCXOServices()
		require.False(t, ok)
	})

	t.Run("walk returns false", func(t *testing.T) {
		s := testServer(t)
		s.SetCXOSubMgr(&fakeCXOMgr{walkOK: false, leaves: map[int][]cxoLeaf{
			CXOFeedSDServices: {{path: "services/proxy/pkA/entry", body: []byte(`{}`)}},
		}})
		_, ok := s.tryCXOServices()
		require.False(t, ok)
	})

	t.Run("no leaves", func(t *testing.T) {
		s := testServer(t)
		s.SetCXOSubMgr(&fakeCXOMgr{walkOK: true})
		_, ok := s.tryCXOServices()
		require.False(t, ok)
	})

	t.Run("builds services, skipping tombstones and malformed", func(t *testing.T) {
		s := testServer(t)
		s.SetCXOSubMgr(&fakeCXOMgr{walkOK: true, leaves: map[int][]cxoLeaf{
			CXOFeedSDServices: {
				{path: "services/proxy/pkA/entry", body: []byte(`{"geo":{"country":"US"}}`)},
				{path: "services/vpn/pkA/entry", body: []byte(`{"geo":{"country":"DE"}}`)}, // appends to pkA
				{path: "services/visor/pkB/entry", body: []byte(`{}`)},
				{path: "services/proxy/pkC/tombstone", body: []byte(`{}`)},   // skipped
				{path: "services/bad", body: []byte(`{}`)},                   // wrong segment count, skipped
				{path: "services/proxy/pkD/entry", body: []byte(`not-json`)}, // bad JSON, skipped
			},
		}})

		services, ok := s.tryCXOServices()
		require.True(t, ok)
		require.Len(t, services, 2)
		require.ElementsMatch(t, []string{"proxy", "vpn"}, services["pkA"].Services)
		require.Equal(t, "US", services["pkA"].Country) // first country wins
		require.Equal(t, []string{"visor"}, services["pkB"].Services)
		require.NotContains(t, services, "pkC")
		require.NotContains(t, services, "pkD")
	})
}

func TestHandleServices_CXOFirst(t *testing.T) {
	s := testServer(t)
	mgr := &fakeCXOMgr{walkOK: true, leaves: map[int][]cxoLeaf{
		CXOFeedSDServices: {{path: "services/proxy/pkA/entry", body: []byte(`{"geo":{"country":"US"}}`)}},
	}}
	s.SetCXOSubMgr(mgr)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/services", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "cxo", rec.Header().Get("X-Skywire-Source"))
	require.Contains(t, rec.Body.String(), "pkA")

	// Acquire/Release scope the subscription around the request.
	require.Equal(t, 1, mgr.acquired)
	require.Equal(t, 1, mgr.released)
}

func TestTryCXOClientsByServer(t *testing.T) {
	t.Run("no manager", func(t *testing.T) {
		s := testServer(t)
		_, ok := s.tryCXOClientsByServer()
		require.False(t, ok)
	})

	t.Run("empty", func(t *testing.T) {
		s := testServer(t)
		s.SetCXOSubMgr(&fakeCXOMgr{})
		_, ok := s.tryCXOClientsByServer()
		require.False(t, ok)
	})

	t.Run("builds map", func(t *testing.T) {
		s := testServer(t)
		s.SetCXOSubMgr(&fakeCXOMgr{leaves: map[int][]cxoLeaf{
			CXOFeedDMSGDClientsByServer: {
				{path: "clients-by-server/srvA/cli1/entry", body: []byte(`{"static":"cli1"}`)},
				{path: "clients-by-server/srvA/cli2/entry", body: []byte(`{"static":"cli2"}`)},
				{path: "clients-by-server/srvB/cli3/tombstone", body: []byte(`{}`)},   // skipped
				{path: "clients-by-server/bad", body: []byte(`{}`)},                   // skipped
				{path: "clients-by-server/srvB/cli4/entry", body: []byte(`bad-json`)}, // skipped
			},
		}})
		out, ok := s.tryCXOClientsByServer()
		require.True(t, ok)
		require.Len(t, out["srvA"], 2)
		require.NotContains(t, out, "srvB")
	})
}

func TestHandleDMSGServersClients_CXOFirst(t *testing.T) {
	s := testServer(t)
	mgr := &fakeCXOMgr{leaves: map[int][]cxoLeaf{
		CXOFeedDMSGDClientsByServer: {
			{path: "clients-by-server/srvA/cli1/entry", body: []byte(`{"static":"cli1"}`)},
		},
	}}
	s.SetCXOSubMgr(mgr)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dmsg/servers/clients", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "cxo", rec.Header().Get("X-Skywire-Source"))
	require.Contains(t, rec.Body.String(), "srvA")
	require.Equal(t, 1, mgr.acquired)
	require.Equal(t, 1, mgr.released)
}

// ---- geoip -----------------------------------------------------------------

func TestFetchLocalGeoIP(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		s := testServer(t)
		s.config.GeoIPURL = ""
		s.fetchLocalGeoIP()
		require.Nil(t, s.localGeo)
	})

	t.Run("success", func(t *testing.T) {
		geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"ip_address":"1.2.3.4","country_code":"US"}`)) //nolint:errcheck
		}))
		defer geo.Close()

		s := testServer(t)
		s.config.GeoIPURL = geo.URL
		s.fetchLocalGeoIP()
		require.NotNil(t, s.localGeo)
		require.Equal(t, "US", s.localGeo.Country)
		require.Equal(t, "1.2.3.4", s.localGeo.IP)
	})

	t.Run("bad json", func(t *testing.T) {
		geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`not json`)) //nolint:errcheck
		}))
		defer geo.Close()

		s := testServer(t)
		s.config.GeoIPURL = geo.URL
		s.fetchLocalGeoIP()
		require.Nil(t, s.localGeo)
	})

	t.Run("request error", func(t *testing.T) {
		s := testServer(t)
		s.config.GeoIPURL = "http://127.0.0.1:0"
		s.fetchLocalGeoIP()
		require.Nil(t, s.localGeo)
	})
}

// ---- IP groups -------------------------------------------------------------

func TestRefreshIPGroupsCache(t *testing.T) {
	surveyDir := t.TempDir()
	// Two visors sharing one IP, one with its own IP, one without IP
	// (skipped), one malformed (skipped), one without a PK in the file
	// (falls back to the directory name).
	writeSurvey := func(dir, content string) {
		require.NoError(t, os.MkdirAll(filepath.Join(surveyDir, dir), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(surveyDir, dir, "node-info.json"), []byte(content), 0o600))
	}
	writeSurvey("pk1", `{"public_key":"pk1","ip_address":"10.0.0.1"}`)
	writeSurvey("pk2", `{"public_key":"pk2","ip_address":"10.0.0.1"}`) // same IP as pk1
	writeSurvey("pk3", `{"public_key":"pk3","ip_address":"10.0.0.2"}`)
	writeSurvey("noip", `{"public_key":"x","ip_address":""}`) // skipped
	writeSurvey("bad", `not json`)                            // skipped
	writeSurvey("dirpk", `{"ip_address":"10.0.0.3"}`)         // pk from dir name

	s := testServer(t)
	s.config.SurveyDir = surveyDir

	// Local visor with geoip + a longer-than-16-char PK (the log line
	// slices pk[:16], so it must be long enough not to panic).
	s.localGeo = &localGeoData{IP: "10.0.0.9", Country: "US"}
	s.visorCache = &LocalVisorData{PubKey: "localvisorpublickey0123456789"}

	// A DMSG server with a known IP -> grouped as dmsg-srv-<pk>.
	s.dmsgCache = &DMSGData{Servers: []DMSGServer{{PK: "srvPK", IP: "10.0.0.4"}}}

	s.refreshIPGroupsCache()

	s.ipGroupsMu.RLock()
	cache := s.ipGroupsCache
	s.ipGroupsMu.RUnlock()

	require.NotNil(t, cache)
	require.True(t, cache.Enabled)
	// pk1 and pk2 share a group; pk3, dirpk, local, dmsg server each add one.
	require.Equal(t, cache.Groups["pk1"], cache.Groups["pk2"])
	require.NotEqual(t, cache.Groups["pk1"], cache.Groups["pk3"])
	require.Contains(t, cache.Groups, "dirpk")
	require.Contains(t, cache.Groups, "localvisorpublickey0123456789")
	require.Contains(t, cache.Groups, "dmsg-srv-srvPK")
	require.Equal(t, 5, cache.TotalGroups) // 10.0.0.1..4 + 10.0.0.9
}

func TestRefreshIPGroupsCache_LocalSharesSurveyIP(t *testing.T) {
	surveyDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(surveyDir, "pk1"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(surveyDir, "pk1", "node-info.json"),
		[]byte(`{"public_key":"pk1","ip_address":"10.0.0.1"}`), 0o600))

	s := testServer(t)
	s.config.SurveyDir = surveyDir
	// Local visor on the SAME IP as pk1 -> joins the existing group.
	s.localGeo = &localGeoData{IP: "10.0.0.1", Country: "US"}
	s.visorCache = &LocalVisorData{PubKey: "localvisorpublickey0123456789"}

	s.refreshIPGroupsCache()

	s.ipGroupsMu.RLock()
	cache := s.ipGroupsCache
	s.ipGroupsMu.RUnlock()
	require.Equal(t, 1, cache.TotalGroups)
	require.Equal(t, cache.Groups["pk1"], cache.Groups["localvisorpublickey0123456789"])
}

func TestRefreshIPGroupsCache_BadSurveyDir(t *testing.T) {
	s := testServer(t)
	s.config.SurveyDir = filepath.Join(t.TempDir(), "does-not-exist")
	s.refreshIPGroupsCache() // logs a warning, does not panic

	s.ipGroupsMu.RLock()
	cache := s.ipGroupsCache
	s.ipGroupsMu.RUnlock()
	require.NotNil(t, cache)
	require.False(t, cache.Enabled)
}

// ---- embedded visor refresh ------------------------------------------------

func u64(v uint64) *uint64 { return &v }

func TestRefreshVisorData_Transports(t *testing.T) {
	id1 := uuid.New()
	fv := &fakeVisorAPI{
		overview: &VisorOverview{
			RoutesCount: 1,
			Transports: []*TransportSummary{
				{
					ID:    id1,
					Type:  "stcpr",
					Label: "skycoin",
					Log:   &transport.LogEntry{SentBytes: u64(100), RecvBytes: u64(200)},
				},
				{ID: uuid.New(), Type: "dmsg"}, // nil Log
			},
		},
	}

	s := testServer(t)
	s.SetVisorAPI(fv, "PKlocal")
	s.localGeo = &localGeoData{IP: "9.9.9.9", Country: "FR"}

	// First refresh seeds the bandwidth snapshot (no deltas yet).
	s.refreshVisorData()
	s.visorMu.RLock()
	c1 := s.visorCache
	s.visorMu.RUnlock()
	require.True(t, c1.Connected)
	require.Len(t, c1.Transports, 2)
	require.Equal(t, "FR", c1.Country)
	require.Equal(t, uint64(0), c1.TotalSentDelta)

	// Bump bandwidth and refresh again -> deltas computed against snapshot.
	fv.overview.Transports[0].Log = &transport.LogEntry{SentBytes: u64(150), RecvBytes: u64(260)}
	s.refreshVisorData()
	s.visorMu.RLock()
	c2 := s.visorCache
	s.visorMu.RUnlock()
	require.Equal(t, uint64(50), c2.TotalSentDelta)
	require.Equal(t, uint64(60), c2.TotalRecvDelta)
	var tp1 LocalTransport
	for _, tp := range c2.Transports {
		if tp.ID == id1.String() {
			tp1 = tp
		}
	}
	require.Equal(t, uint64(50), tp1.SentDelta)
	require.Equal(t, uint64(60), tp1.RecvDelta)
}

func TestRefreshVisorData_OverviewError(t *testing.T) {
	t.Run("embedded keeps API and preserves PK", func(t *testing.T) {
		s := testServer(t)
		s.SetVisorAPI(&fakeVisorAPI{overviewErr: errBoom}, "PKlocal")
		s.refreshVisorData()
		s.visorMu.RLock()
		defer s.visorMu.RUnlock()
		require.NotNil(t, s.visorAPI) // embedded mode never drops the API
		require.True(t, s.visorCache.Connected)
		require.Equal(t, "PKlocal", s.visorCache.PubKey)
	})

	t.Run("nil api is a no-op", func(t *testing.T) {
		s := testServer(t)
		s.refreshVisorData() // no API set
		s.visorMu.RLock()
		defer s.visorMu.RUnlock()
		require.Nil(t, s.visorCache)
	})
}

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }

// ---- broadcast + lifecycle -------------------------------------------------

func TestBroadcastLocalVisorData(t *testing.T) {
	s := testServer(t)
	// nil cache path (marshals a disconnected payload, no clients to send to).
	s.broadcastLocalVisorData()

	// populated cache, still no clients.
	s.visorMu.Lock()
	s.visorCache = &LocalVisorData{Connected: true, PubKey: "PK"}
	s.visorMu.Unlock()
	s.broadcastLocalVisorData()
}

func TestStart(t *testing.T) {
	s := testServer(t)
	// Keep Start fully offline: no geoip, no cache dirs, no DMSG URL.
	s.config.GeoIPURL = ""
	s.config.CacheDirTPD = ""
	s.config.CacheDirUT = ""
	s.config.CacheDirSD = ""
	s.config.DMSGURL = ""
	s.config.AutoRefresh = false
	// Embedded mode so Start also exercises the refreshVisorData branch.
	s.SetVisorAPI(&fakeVisorAPI{overview: &VisorOverview{}}, "PK")

	s.Start()
	s.Stop() // closes stopChan -> the broadcast goroutine returns
}

func TestStartAutoRefresh_Enabled(t *testing.T) {
	s := testServer(t)
	s.config.AutoRefresh = true
	s.config.CacheMaxAge = 1

	s.startAutoRefresh()
	require.NotNil(t, s.autoTick)
	s.Stop() // goroutine selects stopChan, stops the ticker, returns
}

func TestStartLocalVisorBroadcast_TickAndStop(t *testing.T) {
	s := testServer(t)

	done := make(chan struct{})
	go func() {
		s.startLocalVisorBroadcast()
		close(done)
	}()

	// Let the 2s ticker fire at least once (no clients -> continue branch).
	time.Sleep(2200 * time.Millisecond)
	s.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("startLocalVisorBroadcast did not return after Stop()")
	}
}

// ---- websocket -------------------------------------------------------------

func TestHandleLocalVisorWS(t *testing.T) {
	s := testServer(t)
	s.SetVisorAPI(&fakeVisorAPI{overview: &VisorOverview{}}, "PKws")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/local-visor"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)

	// The handler sends the current local-visor snapshot immediately.
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(data), "connected")

	// The client got registered server-side.
	require.Eventually(t, func() bool {
		s.wsClientsMu.RLock()
		defer s.wsClientsMu.RUnlock()
		return len(s.wsClients) == 1
	}, time.Second, 10*time.Millisecond)

	// Closing the client makes the server read loop error out and
	// unregister the client.
	conn.Close(websocket.StatusNormalClosure, "done")
	require.Eventually(t, func() bool {
		s.wsClientsMu.RLock()
		defer s.wsClientsMu.RUnlock()
		return len(s.wsClients) == 0
	}, 2*time.Second, 10*time.Millisecond)
}

// ---- ListenAndServe --------------------------------------------------------

func TestListenAndServe(t *testing.T) {
	// Grab a free port, then hand it to ListenAndServe.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	s := testServer(t)
	s.config.Addr = "127.0.0.1"
	s.config.Port = port
	// Keep it offline.
	s.config.GeoIPURL = ""
	s.config.CacheDirTPD = ""
	s.config.CacheDirUT = ""
	s.config.CacheDirSD = ""
	s.config.DMSGURL = ""
	s.config.AutoRefresh = true // exercise the "auto-refresh enabled" log branch

	// ListenAndServe blocks; run it in the background. It has no graceful
	// shutdown hook, so the goroutine is intentionally left running until
	// the test binary exits.
	go func() { _ = s.ListenAndServe() }()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	var resp *http.Response
	require.Eventually(t, func() bool {
		r, e := http.Get(url) //nolint:gosec
		if e != nil {
			return false
		}
		resp = r
		return true
	}, 5*time.Second, 25*time.Millisecond)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)

	s.Stop()
}

// ---- static asset routes (setupRoutes closures) ---------------------------

func TestStaticAssetRoutes(t *testing.T) {
	s := testServer(t)
	// Each of these is served by a setupRoutes closure that reads from an
	// embedded FS. We only assert the route is wired and returns a final
	// status (200 when the asset is embedded, 404/500 otherwise) — either
	// way the handler body executes.
	paths := []string{
		"/bundle.js",
		"/wasm",
		"/main.wasm",
		"/wasm_exec.js",
		"/textures/earth.jpg",
		"/textures/earth.png",
		"/index.html",
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		require.NotEqual(t, 0, rec.Code, p)
	}
}

// ---- app handler error/edge paths ------------------------------------------

func TestAppHandlers_ErrorPaths(t *testing.T) {
	paths := []string{"/api/apps/stop", "/api/apps/autostart", "/api/apps/set-pk"}

	for _, p := range paths {
		t.Run("wrong method "+p, func(t *testing.T) {
			s := testServer(t)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			require.Equal(t, "POST required", decodeMap(t, rec.Body.Bytes())["error"])
		})

		t.Run("invalid body "+p, func(t *testing.T) {
			s := testServer(t)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, strings.NewReader("{bad")))
			require.Equal(t, "invalid request body", decodeMap(t, rec.Body.Bytes())["error"])
		})

		t.Run("not connected "+p, func(t *testing.T) {
			s := testServer(t)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{"name":"x"}`)))
			require.Equal(t, "visor not connected", decodeMap(t, rec.Body.Bytes())["error"])
		})
	}

	// Visor-error path for each app mutation handler.
	t.Run("stop error", func(t *testing.T) {
		s := testServer(t)
		s.SetVisorAPI(&fakeVisorAPI{stopErr: errBoom}, "PK")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/apps/stop", strings.NewReader(`{"name":"x"}`)))
		require.Equal(t, "boom", decodeMap(t, rec.Body.Bytes())["error"])
	})
	t.Run("autostart error", func(t *testing.T) {
		s := testServer(t)
		s.SetVisorAPI(&fakeVisorAPI{autoErr: errBoom}, "PK")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/apps/autostart", strings.NewReader(`{"name":"x","auto_start":true}`)))
		require.Equal(t, "boom", decodeMap(t, rec.Body.Bytes())["error"])
	})
	t.Run("set-pk error", func(t *testing.T) {
		s := testServer(t)
		s.SetVisorAPI(&fakeVisorAPI{setPKErr: errBoom}, "PK")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/apps/set-pk", strings.NewReader(`{"name":"x","pk":"y"}`)))
		require.Equal(t, "boom", decodeMap(t, rec.Body.Bytes())["error"])
	})
}

// ---- TPS / local transport handler edge paths ------------------------------

func TestTPSRemoveTransport_Paths(t *testing.T) {
	s := testServer(t)
	s.SetTPSAPI(&fakeTPSAPI{pk: "PK"})

	t.Run("OPTIONS", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/tps/remove-transport", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("wrong method", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tps/remove-transport", nil))
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
	t.Run("invalid body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/tps/remove-transport", strings.NewReader("{bad")))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("success", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/tps/remove-transport", strings.NewReader(`{"target_pk":"a","id":"b"}`)))
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestLocalTransportHandlers_EdgePaths(t *testing.T) {
	for _, p := range []string{"/api/local/add-transport", "/api/local/remove-transport"} {
		s := testServer(t)
		s.SetVisorAPI(&fakeVisorAPI{}, "PK")

		t.Run("OPTIONS "+p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, p, nil))
			require.Equal(t, http.StatusOK, rec.Code)
		})
		t.Run("wrong method "+p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		})
		t.Run("invalid body "+p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, strings.NewReader("{bad")))
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// ---- DMSG handler error + cache paths --------------------------------------

func TestHandleDMSGServersEntries_Error(t *testing.T) {
	s := testServer(t)
	s.config.DMSGURL = "http://127.0.0.1:0" // unreachable -> getDMSGData errors

	for _, p := range []string{"/api/dmsg/servers", "/api/dmsg/entries"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		require.Equal(t, http.StatusOK, rec.Code) // errors are encoded into the JSON body
		require.Contains(t, rec.Body.String(), "error", p)
	}
}

func TestGetDMSGData_Cached(t *testing.T) {
	s := testServer(t)
	// Seed the in-memory cache as fresh -> getDMSGData returns it directly.
	s.dmsgCache = &DMSGData{EntriesCount: 7, LastUpdated: time.Now()}
	got, err := s.getDMSGData()
	require.NoError(t, err)
	require.Equal(t, 7, got.EntriesCount)
}

func TestGetDMSGSubData_DiskCache(t *testing.T) {
	dmsgURL, geoURL, closeFn := newDMSGStack(t)
	defer closeFn()

	s := testServer(t)
	s.config.NoCache = false
	s.config.CacheDirDMSG = t.TempDir()
	s.config.DMSGURL = dmsgURL
	s.config.GeoIPURL = geoURL

	// First call populates the on-disk caches via getDMSGSubData.
	d1, err := s.getDMSGData()
	require.NoError(t, err)
	require.Len(t, d1.Servers, 1)
	servers, _, _ := s.dmsgCacheFiles()
	require.FileExists(t, servers)

	// Clear in-memory cache; second call reads the fresh disk cache
	// (exercises getDMSGSubData's cache-hit branch).
	s.dmsgMu.Lock()
	s.dmsgCache = nil
	s.dmsgMu.Unlock()
	d2, err := s.getDMSGData()
	require.NoError(t, err)
	require.Len(t, d2.Servers, 1)
}

// ---- refreshCacheFile ------------------------------------------------------

func TestRefreshCacheFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("fresh-data")) //nolint:errcheck
	}))
	defer srv.Close()

	s := testServer(t)
	s.config.CacheMaxAge = 5
	cacheFile := filepath.Join(t.TempDir(), "tp.json")

	// Missing file -> fetch + write.
	s.refreshCacheFile(cacheFile, srv.URL)
	data, err := os.ReadFile(cacheFile)
	require.NoError(t, err)
	require.Equal(t, "fresh-data", string(data))

	// Fresh file -> early return (no error, content unchanged even if the
	// upstream would now serve something else).
	s.refreshCacheFile(cacheFile, srv.URL)
	data, err = os.ReadFile(cacheFile)
	require.NoError(t, err)
	require.Equal(t, "fresh-data", string(data))

	// Stale file -> refetch.
	old := time.Now().Add(-10 * time.Minute)
	require.NoError(t, os.Chtimes(cacheFile, old, old))
	s.refreshCacheFile(cacheFile, srv.URL)
	require.FileExists(t, cacheFile)
}

func TestGetSDData_Paths(t *testing.T) {
	s := testServer(t)

	// Disabled cache.
	s.config.CacheDirSD = ""
	_, err := s.getSDData()
	require.Error(t, err)

	// Missing cache file.
	s.config.CacheDirSD = t.TempDir()
	s.config.SDURL = "http://sd.example.com"
	_, err = s.getSDData()
	require.Error(t, err)

	// Stale cache file -> returns stale content and kicks a background
	// refresh (which we don't wait on).
	cacheFile := s.sdCacheFile()
	require.NoError(t, os.WriteFile(cacheFile, []byte(`{"pk":{}}`), 0o600))
	old := time.Now().Add(-10 * time.Minute)
	require.NoError(t, os.Chtimes(cacheFile, old, old))
	data, err := s.getSDData()
	require.NoError(t, err)
	require.Contains(t, data, "pk")
}
