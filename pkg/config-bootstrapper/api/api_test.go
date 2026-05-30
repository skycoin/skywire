package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
)

// --- fake HTTP transport so tests never touch the real network ---

var (
	rtMu      sync.Mutex
	rtHandler func(*http.Request) (*http.Response, error)
)

type fakeRT struct{}

func (fakeRT) RoundTrip(r *http.Request) (*http.Response, error) {
	rtMu.Lock()
	h := rtHandler
	rtMu.Unlock()
	if h == nil {
		return nil, fmt.Errorf("network disabled in tests: %s", r.URL)
	}
	return h(r)
}

func setRT(h func(*http.Request) (*http.Response, error)) {
	rtMu.Lock()
	rtHandler = h
	rtMu.Unlock()
}

// networkDown is the default handler: every request fails, so the background
// refresh goroutine started by New() is a harmless no-op.
func networkDown(r *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("network disabled: %s", r.URL)
}

func jsonResp(code int, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(bytes.NewReader(b)),
		Header:     make(http.Header),
	}
}

func rawResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// errReadCloser is a response body that fails mid-read, exercising the
// io.ReadAll error branch in the fetch helpers.
type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, fmt.Errorf("read boom") }
func (errReadCloser) Close() error             { return nil }

func bodyErrResp() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: errReadCloser{}, Header: make(http.Header)}
}

func TestMain(m *testing.M) {
	http.DefaultClient.Transport = fakeRT{}
	setRT(networkDown)
	os.Exit(m.Run())
}

func testLogger() *logging.Logger {
	return logging.MustGetLogger("test")
}

// --- New / Close ---

func TestNew_Defaults(t *testing.T) {
	setRT(networkDown)
	api := New(testLogger(), Config{}, "", "dmsg://addr:0")
	defer api.Close()

	if api == nil {
		t.Fatal("New returned nil")
	}
	if api.dmsgAddr != "dmsg://addr:0" {
		t.Errorf("dmsgAddr = %q, want dmsg://addr:0", api.dmsgAddr)
	}
	// Defaults come straight from the embedded prod deployment.
	if api.services.DmsgDiscovery != deployment.Prod.DmsgDiscovery {
		t.Errorf("DmsgDiscovery = %q, want prod default %q", api.services.DmsgDiscovery, deployment.Prod.DmsgDiscovery)
	}
	if api.startedAt.IsZero() {
		t.Error("startedAt not set")
	}
}

func TestNew_DomainReplacement(t *testing.T) {
	setRT(networkDown)
	api := New(testLogger(), Config{}, "example.com", "")
	defer api.Close()

	cases := map[string]string{
		"DmsgDiscovery":      api.services.DmsgDiscovery,
		"TransportDiscovery": api.services.TransportDiscovery,
		"AddressResolver":    api.services.AddressResolver,
		"RouteFinder":        api.services.RouteFinder,
		"UptimeTracker":      api.services.UptimeTracker,
		"ServiceDiscovery":   api.services.ServiceDiscovery,
	}
	for name, got := range cases {
		if strings.Contains(got, "skycoin.com") {
			t.Errorf("%s = %q, still contains skycoin.com after replacement", name, got)
		}
		if !strings.Contains(got, "example.com") {
			t.Errorf("%s = %q, expected it to contain example.com", name, got)
		}
	}
}

func TestNew_SameDomainNoReplacement(t *testing.T) {
	setRT(networkDown)
	// The canonical domain must be treated as the default (no replacement).
	api := New(testLogger(), Config{}, "skywire.skycoin.com", "")
	defer api.Close()

	if api.services.DmsgDiscovery != deployment.Prod.DmsgDiscovery {
		t.Errorf("DmsgDiscovery = %q, want unchanged %q", api.services.DmsgDiscovery, deployment.Prod.DmsgDiscovery)
	}
}

func TestNew_ConfigOverrides(t *testing.T) {
	setRT(networkDown)
	pk, _ := cipher.GenerateKeyPair()
	conf := Config{
		StunServers:       []string{"1.2.3.4:3478"},
		SetupNodes:        []cipher.PubKey{pk},
		SurveyWhitelist:   []cipher.PubKey{pk},
		TransportSetupPKs: []cipher.PubKey{pk},
	}
	api := New(testLogger(), conf, "", "")
	defer api.Close()

	if len(api.services.StunServers) != 1 || api.services.StunServers[0] != "1.2.3.4:3478" {
		t.Errorf("StunServers = %v, want override", api.services.StunServers)
	}
	if len(api.services.RouteSetupNodes) != 1 || api.services.RouteSetupNodes[0] != pk {
		t.Errorf("RouteSetupNodes not overridden: %v", api.services.RouteSetupNodes)
	}
	if len(api.services.SurveyWhitelist) != 1 {
		t.Errorf("SurveyWhitelist not overridden: %v", api.services.SurveyWhitelist)
	}
	if len(api.services.TransportSetupPKs) != 1 {
		t.Errorf("TransportSetupPKs not overridden: %v", api.services.TransportSetupPKs)
	}
}

func TestClose_Idempotent(t *testing.T) {
	a := &API{closeC: make(chan struct{})}
	a.Close()
	a.Close() // second call must not panic / double-close.
}

// --- handlers (constructed directly; no router, no goroutine) ---

func newHandlerAPI() *API {
	svcs := deployment.Prod
	return &API{
		log:            testLogger(),
		startedAt:      time.Now(),
		services:       &svcs,
		dmsghttpConfTs: time.Now().Add(-5 * time.Minute),
		closeC:         make(chan struct{}),
		dmsgAddr:       "dmsg://test:0",
	}
}

func TestHealth(t *testing.T) {
	a := newHandlerAPI()
	rec := httptest.NewRecorder()
	a.health(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp HealthCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ServiceName != "config-bootstrapper" {
		t.Errorf("ServiceName = %q", resp.ServiceName)
	}
	if resp.DmsgAddr != "dmsg://test:0" {
		t.Errorf("DmsgAddr = %q", resp.DmsgAddr)
	}
}

func TestConfig(t *testing.T) {
	a := newHandlerAPI()
	rec := httptest.NewRecorder()
	a.config(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp deployment.Services
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DmsgDiscovery != deployment.Prod.DmsgDiscovery {
		t.Errorf("DmsgDiscovery = %q, want %q", resp.DmsgDiscovery, deployment.Prod.DmsgDiscovery)
	}
}

func TestWriteJSON_MarshalError(t *testing.T) {
	a := newHandlerAPI()
	rec := httptest.NewRecorder()
	// A channel can't be marshaled to JSON, forcing the encode-error branch.
	a.writeJSON(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, make(chan int))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 on marshal failure", rec.Code)
	}
}

// --- dmsghttp endpoint + generation ---

func dmsgServingHandler(dmsgAddr, serverStatic, serverAddr string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/all_servers"):
			s := httputil.DMSGServersConf{Static: serverStatic}
			s.Server.Address = serverAddr
			return jsonResp(http.StatusOK, []httputil.DMSGServersConf{s}), nil
		case strings.HasSuffix(r.URL.Path, "/health"):
			return jsonResp(http.StatusOK, httputil.HealthCheckResponse{DmsgAddr: dmsgAddr}), nil
		default:
			return rawResp(http.StatusNotFound, ""), nil
		}
	}
}

func TestDmsghttp_GeneratesThenCaches(t *testing.T) {
	setRT(dmsgServingHandler("dmsg://serviceaddr:0", "0300000000000000000000000000000000000000000000000000000000000000", "1.1.1.1:8080"))
	a := newHandlerAPI()

	rec := httptest.NewRecorder()
	a.dmsghttp(rec, httptest.NewRequest(http.MethodGet, "/dmsghttp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var conf httputil.DMSGHTTPConf
	if err := json.Unmarshal(rec.Body.Bytes(), &conf); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if conf.AddressResolver != "dmsg://serviceaddr:0" {
		t.Errorf("AddressResolver = %q, want dmsg://serviceaddr:0", conf.AddressResolver)
	}
	if len(conf.DMSGServers) != 1 || conf.DMSGServers[0].Server.Address != "1.1.1.1:8080" {
		t.Errorf("DMSGServers = %+v, want one entry with address 1.1.1.1:8080", conf.DMSGServers)
	}

	// Second call within the 5m window must serve the cache: switch the
	// transport to fail, and the response must still be the cached conf.
	cachedTs := a.dmsghttpConfTs
	setRT(networkDown)
	rec2 := httptest.NewRecorder()
	a.dmsghttp(rec2, httptest.NewRequest(http.MethodGet, "/dmsghttp", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("cached call status = %d, want 200", rec2.Code)
	}
	if !a.dmsghttpConfTs.Equal(cachedTs) {
		t.Error("dmsghttpConfTs changed on a cached call; cache was not used")
	}
	if !bytes.Equal(rec.Body.Bytes(), rec2.Body.Bytes()) {
		t.Error("cached response differs from generated response")
	}
}

// --- refreshDmsgServers ---

func TestRefreshDmsgServers_EmptyURL(t *testing.T) {
	svcs := deployment.Services{} // no DmsgDiscovery
	a := &API{log: testLogger(), services: &svcs}
	a.refreshDmsgServers() // must return early without touching the network
	if len(a.services.DmsgServers) != 0 {
		t.Errorf("DmsgServers = %v, want empty", a.services.DmsgServers)
	}
}

func TestRefreshDmsgServers_KeepsOnEmptyResult(t *testing.T) {
	setRT(networkDown) // fetch fails => 0 entries
	prev := []deployment.DmsgServerEntry{{Static: "keep"}}
	svcs := deployment.Services{DmsgDiscovery: "http://dmsgd.example.com", DmsgServers: prev}
	a := &API{log: testLogger(), services: &svcs}

	a.refreshDmsgServers()
	if len(a.services.DmsgServers) != 1 || a.services.DmsgServers[0].Static != "keep" {
		t.Errorf("previous DmsgServers not kept: %v", a.services.DmsgServers)
	}
}

func TestRefreshDmsgServers_UpdatesOnLiveResult(t *testing.T) {
	setRT(dmsgServingHandler("", "static-pk", "9.9.9.9:1234"))
	svcs := deployment.Services{DmsgDiscovery: "http://dmsgd.example.com"}
	a := &API{log: testLogger(), services: &svcs}

	a.refreshDmsgServers()
	if len(a.services.DmsgServers) != 1 {
		t.Fatalf("DmsgServers = %v, want one live entry", a.services.DmsgServers)
	}
	if a.services.DmsgServers[0].Static != "static-pk" || a.services.DmsgServers[0].Server.Address != "9.9.9.9:1234" {
		t.Errorf("entry not mapped correctly: %+v", a.services.DmsgServers[0])
	}
}

// --- refresh loop stops on Close ---

func TestRefreshDmsgServersLoop_StopsOnClose(t *testing.T) {
	setRT(networkDown)
	svcs := deployment.Services{DmsgDiscovery: "http://dmsgd.example.com"}
	a := &API{log: testLogger(), services: &svcs, closeC: make(chan struct{})}

	done := make(chan struct{})
	go func() {
		a.refreshDmsgServersLoop()
		close(done)
	}()

	a.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refreshDmsgServersLoop did not return after Close")
	}
}

// --- fetch helpers ---

func TestFetchDMSGAddress(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		setRT(func(*http.Request) (*http.Response, error) {
			return jsonResp(http.StatusOK, httputil.HealthCheckResponse{DmsgAddr: "dmsg://x:0"}), nil
		})
		if got := fetchDMSGAddress("http://svc.example.com"); got != "dmsg://x:0" {
			t.Errorf("got %q, want dmsg://x:0", got)
		}
	})
	t.Run("http error", func(t *testing.T) {
		setRT(networkDown)
		if got := fetchDMSGAddress("http://svc.example.com"); got != "" {
			t.Errorf("got %q, want empty on http error", got)
		}
	})
	t.Run("bad json", func(t *testing.T) {
		setRT(func(*http.Request) (*http.Response, error) { return rawResp(http.StatusOK, "}{not json"), nil })
		if got := fetchDMSGAddress("http://svc.example.com"); got != "" {
			t.Errorf("got %q, want empty on bad json", got)
		}
	})
	t.Run("body read error", func(t *testing.T) {
		setRT(func(*http.Request) (*http.Response, error) { return bodyErrResp(), nil })
		if got := fetchDMSGAddress("http://svc.example.com"); got != "" {
			t.Errorf("got %q, want empty on body read error", got)
		}
	})
}

func TestFetchDMSGServers(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		setRT(func(*http.Request) (*http.Response, error) {
			s := httputil.DMSGServersConf{Static: "pk"}
			s.Server.Address = "5.5.5.5:1"
			return jsonResp(http.StatusOK, []httputil.DMSGServersConf{s}), nil
		})
		got := fetchDMSGServers("http://dmsgd.example.com")
		if len(got) != 1 || got[0].Server.Address != "5.5.5.5:1" {
			t.Errorf("got %+v, want one entry", got)
		}
	})
	t.Run("http error", func(t *testing.T) {
		setRT(networkDown)
		if got := fetchDMSGServers("http://dmsgd.example.com"); len(got) != 0 {
			t.Errorf("got %+v, want empty on http error", got)
		}
	})
	t.Run("bad json", func(t *testing.T) {
		setRT(func(*http.Request) (*http.Response, error) { return rawResp(http.StatusOK, "notjson"), nil })
		if got := fetchDMSGServers("http://dmsgd.example.com"); len(got) != 0 {
			t.Errorf("got %+v, want empty on bad json", got)
		}
	})
	t.Run("body read error", func(t *testing.T) {
		setRT(func(*http.Request) (*http.Response, error) { return bodyErrResp(), nil })
		if got := fetchDMSGServers("http://dmsgd.example.com"); len(got) != 0 {
			t.Errorf("got %+v, want empty on body read error", got)
		}
	})
}
