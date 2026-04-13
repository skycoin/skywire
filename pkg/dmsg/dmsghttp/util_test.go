// Package dmsghttp_test pkg/dmsghttp/util_test.go
package dmsghttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

func init() {
	logrus.SetLevel(logrus.WarnLevel)
}

// --- MakeHTTPTransport tests ---

func TestMakeHTTPTransport_ReturnsValidTransport(t *testing.T) {
	// Verify MakeHTTPTransport returns a transport that implements http.RoundTripper.
	dc := disc.NewMock(0)

	pk, sk := cipher.GenerateKeyPair()
	dmsgC := dmsg.NewClient(pk, sk, dc, nil)
	defer dmsgC.Close() //nolint:errcheck,gosec

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transport := dmsghttp.MakeHTTPTransport(ctx, dmsgC)

	// Verify the returned value satisfies http.RoundTripper.
	var _ http.RoundTripper = transport
}

func TestMakeHTTPTransport_RoundTripInvalidHost(t *testing.T) {
	// RoundTrip should fail with an invalid host address.
	dc := disc.NewMock(0)

	pk, sk := cipher.GenerateKeyPair()
	dmsgC := dmsg.NewClient(pk, sk, dc, nil)
	defer dmsgC.Close() //nolint:errcheck,gosec

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transport := dmsghttp.MakeHTTPTransport(ctx, dmsgC)

	req, err := http.NewRequest(http.MethodGet, "http://invalid-host:80/test", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "invalid host address")
}

func TestMakeHTTPTransport_RoundTripDialFailure(t *testing.T) {
	// RoundTrip should fail when dial fails (no servers available).
	const maxSessions = 10
	dc := disc.NewMock(0)

	// Create a single server so the client can start.
	srvPK, srvSK := cipher.GenerateKeyPair()
	srvConf := dmsg.ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}
	srv := dmsg.NewServer(srvPK, srvSK, dc, &srvConf, nil)
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	go srv.Serve(lis, "") //nolint:errcheck,gosec
	defer srv.Close()     //nolint:errcheck,gosec

	pk, sk := cipher.GenerateKeyPair()
	dmsgC := dmsg.NewClient(pk, sk, dc, &dmsg.Config{MinSessions: 1})
	go dmsgC.Serve(context.Background())
	defer dmsgC.Close() //nolint:errcheck,gosec
	<-dmsgC.Ready()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	transport := dmsghttp.MakeHTTPTransport(ctx, dmsgC)

	// Use a valid PK format but one that no client is listening on.
	fakePK, _ := cipher.GenerateKeyPair()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s:80/test", fakePK.String()), nil)
	require.NoError(t, err)
	req = req.WithContext(ctx)

	resp, err := transport.RoundTrip(req)
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestMakeHTTPTransport_FullRoundTrip(t *testing.T) {
	// End-to-end: client dials to HTTP server over dmsg and gets a response.
	const maxSessions = 20
	const dmsgHTTPPort = uint16(80)

	dc := disc.NewMock(0)

	// Start dmsg server.
	srvPK, srvSK := cipher.GenerateKeyPair()
	srvConf := dmsg.ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}
	srv := dmsg.NewServer(srvPK, srvSK, dc, &srvConf, nil)
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	go srv.Serve(lis, "")             //nolint:errcheck,gosec
	t.Cleanup(func() { srv.Close() }) //nolint:errcheck,gosec
	<-srv.Ready()

	// Start dmsg client that hosts HTTP server.
	hostPK, hostSK := cipher.GenerateKeyPair()
	dmsgHost := dmsg.NewClient(hostPK, hostSK, dc, &dmsg.Config{MinSessions: 1})
	go dmsgHost.Serve(context.Background())
	t.Cleanup(func() { dmsgHost.Close() }) //nolint:errcheck,gosec
	<-dmsgHost.Ready()

	dmsgLis, err := dmsgHost.Listen(dmsgHTTPPort)
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Get("/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("world")) //nolint:errcheck,gosec
	})
	r.Post("/echo", func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body) //nolint:errcheck,gosec
		w.Write(data)                 //nolint:errcheck,gosec
	})
	go http.Serve(dmsgLis, r) //nolint:errcheck,gosec

	// Start dmsg client that runs HTTP client.
	clientPK, clientSK := cipher.GenerateKeyPair()
	dmsgClient := dmsg.NewClient(clientPK, clientSK, dc, &dmsg.Config{MinSessions: 1})
	go dmsgClient.Serve(context.Background())
	t.Cleanup(func() { dmsgClient.Close() }) //nolint:errcheck,gosec
	<-dmsgClient.Ready()

	// Allow time for dmsg sessions to stabilize.
	time.Sleep(300 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	httpC := http.Client{
		Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgClient),
		Timeout:   10 * time.Second,
	}

	t.Run("GET_request", func(t *testing.T) {
		resp, err := httpC.Get(fmt.Sprintf("http://%s:%d/hello", hostPK.String(), dmsgHTTPPort))
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck,gosec

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "world", string(body))
	})

	t.Run("POST_echo", func(t *testing.T) {
		resp, err := httpC.Post(
			fmt.Sprintf("http://%s:%d/echo", hostPK.String(), dmsgHTTPPort),
			"text/plain",
			http.NoBody,
		)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck,gosec
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestMakeHTTPTransport_DefaultPort(t *testing.T) {
	// When no port is specified in the host, default port 80 should be used.
	const maxSessions = 20

	dc := disc.NewMock(0)

	srvPK, srvSK := cipher.GenerateKeyPair()
	srvConf := dmsg.ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}
	srv := dmsg.NewServer(srvPK, srvSK, dc, &srvConf, nil)
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	go srv.Serve(lis, "")             //nolint:errcheck,gosec
	t.Cleanup(func() { srv.Close() }) //nolint:errcheck,gosec
	<-srv.Ready()

	// Host HTTP server on port 80.
	hostPK, hostSK := cipher.GenerateKeyPair()
	dmsgHost := dmsg.NewClient(hostPK, hostSK, dc, &dmsg.Config{MinSessions: 1})
	go dmsgHost.Serve(context.Background())
	t.Cleanup(func() { dmsgHost.Close() }) //nolint:errcheck,gosec
	<-dmsgHost.Ready()

	dmsgLis, err := dmsgHost.Listen(80)
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("default-port")) //nolint:errcheck,gosec
	})
	go http.Serve(dmsgLis, r) //nolint:errcheck,gosec

	// Client.
	clientPK, clientSK := cipher.GenerateKeyPair()
	dmsgClient := dmsg.NewClient(clientPK, clientSK, dc, &dmsg.Config{MinSessions: 1})
	go dmsgClient.Serve(context.Background())
	t.Cleanup(func() { dmsgClient.Close() }) //nolint:errcheck,gosec
	<-dmsgClient.Ready()

	// Allow time for dmsg sessions to stabilize.
	time.Sleep(300 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	httpC := http.Client{
		Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgClient),
		Timeout:   10 * time.Second,
	}

	// URL without port — should default to 80.
	resp, err := httpC.Get(fmt.Sprintf("http://%s/", hostPK.String()))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck,gosec

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "default-port", string(body))
}

// --- GetServers tests ---

// newDiscoveryMockServer creates an httptest.Server that mimics the dmsg-discovery
// AllServers endpoint at /dmsg-discovery/all_servers.
func newDiscoveryMockServer(t *testing.T, entries []*disc.Entry) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/dmsg-discovery/all_servers", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(entries); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGetServers_ReturnsServers(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	entries := []*disc.Entry{
		{
			Static: pk1,
			Server: &disc.Server{Address: "1.2.3.4:8080", AvailableSessions: 10},
		},
		{
			Static: pk2,
			Server: &disc.Server{Address: "5.6.7.8:8080", AvailableSessions: 5},
		},
	}

	srv := newDiscoveryMockServer(t, entries)
	log := logging.MustGetLogger("test_get_servers")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := dmsghttp.GetServers(ctx, srv.URL, "", log)
	require.Len(t, result, 2)
}

func TestGetServers_FiltersServerType(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	pk3, _ := cipher.GenerateKeyPair()

	entries := []*disc.Entry{
		{
			Static: pk1,
			Server: &disc.Server{Address: "1.2.3.4:8080", AvailableSessions: 10, ServerType: "official"},
		},
		{
			Static: pk2,
			Server: &disc.Server{Address: "5.6.7.8:8080", AvailableSessions: 5, ServerType: "community"},
		},
		{
			Static: pk3,
			Server: &disc.Server{Address: "9.10.11.12:8080", AvailableSessions: 3, ServerType: "official"},
		},
	}

	srv := newDiscoveryMockServer(t, entries)
	log := logging.MustGetLogger("test_filter")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("filter_official", func(t *testing.T) {
		result := dmsghttp.GetServers(ctx, srv.URL, "official", log)
		require.Len(t, result, 2)
		for _, e := range result {
			assert.Equal(t, "official", e.Server.ServerType)
		}
	})

	t.Run("filter_community", func(t *testing.T) {
		result := dmsghttp.GetServers(ctx, srv.URL, "community", log)
		require.Len(t, result, 1)
		assert.Equal(t, "community", result[0].Server.ServerType)
	})
}

func TestGetServers_FilterRemovesAllRetries(t *testing.T) {
	// When filtering removes all servers, GetServers retries. On context cancellation
	// it should return empty.
	pk1, _ := cipher.GenerateKeyPair()

	entries := []*disc.Entry{
		{
			Static: pk1,
			Server: &disc.Server{Address: "1.2.3.4:8080", AvailableSessions: 10, ServerType: "community"},
		},
	}

	srv := newDiscoveryMockServer(t, entries)
	log := logging.MustGetLogger("test_filter_none")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Filter for "official" but only "community" servers exist: should retry until context expires.
	result := dmsghttp.GetServers(ctx, srv.URL, "official", log)
	assert.Empty(t, result)
}

func TestGetServers_ContextCanceledReturnsEmpty(t *testing.T) {
	// If context is canceled before any servers are found, return empty.
	log := logging.MustGetLogger("test_ctx_cancel")

	// Use a URL that will fail (no server listening).
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	result := dmsghttp.GetServers(ctx, "http://127.0.0.1:1", "", log)
	assert.Empty(t, result)
}

func TestGetServers_EmptyResponseRetries(t *testing.T) {
	// If the discovery returns an empty list, GetServers retries until context is done.
	srv := newDiscoveryMockServer(t, []*disc.Entry{})
	log := logging.MustGetLogger("test_empty")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := dmsghttp.GetServers(ctx, srv.URL, "", log)
	assert.Empty(t, result)
}

func TestGetServers_NoFilterReturnsAll(t *testing.T) {
	// Empty dmsgServerType string should return all servers without filtering.
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	entries := []*disc.Entry{
		{
			Static: pk1,
			Server: &disc.Server{Address: "1.2.3.4:8080", AvailableSessions: 10, ServerType: "official"},
		},
		{
			Static: pk2,
			Server: &disc.Server{Address: "5.6.7.8:8080", AvailableSessions: 5, ServerType: "community"},
		},
	}

	srv := newDiscoveryMockServer(t, entries)
	log := logging.MustGetLogger("test_no_filter")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := dmsghttp.GetServers(ctx, srv.URL, "", log)
	require.Len(t, result, 2)
}

// --- ListenAndServe tests ---

func TestListenAndServe_ServesHTTP(t *testing.T) {
	const maxSessions = 20
	const dmsgHTTPPort = uint16(8080)

	dc := disc.NewMock(0)

	// Start dmsg server.
	srvPK, srvSK := cipher.GenerateKeyPair()
	srvConf := dmsg.ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}
	srv := dmsg.NewServer(srvPK, srvSK, dc, &srvConf, nil)
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	go srv.Serve(lis, "")             //nolint:errcheck,gosec
	t.Cleanup(func() { srv.Close() }) //nolint:errcheck,gosec
	<-srv.Ready()

	// Host client.
	hostPK, hostSK := cipher.GenerateKeyPair()
	dmsgHost := dmsg.NewClient(hostPK, hostSK, dc, &dmsg.Config{MinSessions: 1})
	go dmsgHost.Serve(context.Background())
	t.Cleanup(func() { dmsgHost.Close() }) //nolint:errcheck,gosec
	<-dmsgHost.Ready()

	log := logging.MustGetLogger("test_listen_serve")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("listen-and-serve")) //nolint:errcheck,gosec
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- dmsghttp.ListenAndServe(ctx, hostSK, handler, dc, dmsgHTTPPort, dmsgHost, log)
	}()

	// Give the server a moment to start.
	time.Sleep(100 * time.Millisecond)

	// Dial from another client.
	clientPK, clientSK := cipher.GenerateKeyPair()
	dmsgClient := dmsg.NewClient(clientPK, clientSK, dc, &dmsg.Config{MinSessions: 1})
	go dmsgClient.Serve(context.Background())
	t.Cleanup(func() { dmsgClient.Close() }) //nolint:errcheck,gosec
	<-dmsgClient.Ready()

	// Allow time for dmsg sessions to stabilize.
	time.Sleep(300 * time.Millisecond)

	httpC := http.Client{
		Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgClient),
		Timeout:   10 * time.Second,
	}

	resp, err := httpC.Get(fmt.Sprintf("http://%s:%d/", hostPK.String(), dmsgHTTPPort))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck,gosec

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "listen-and-serve", string(body))

	// Cancel context to shut down the server.
	cancel()
}

func TestListenAndServe_InvalidPort(t *testing.T) {
	// Trying to listen on a port that's already in use should fail.
	const maxSessions = 20
	const dmsgHTTPPort = uint16(8081)

	dc := disc.NewMock(0)

	srvPK, srvSK := cipher.GenerateKeyPair()
	srvConf := dmsg.ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}
	srv := dmsg.NewServer(srvPK, srvSK, dc, &srvConf, nil)
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	go srv.Serve(lis, "")             //nolint:errcheck,gosec
	t.Cleanup(func() { srv.Close() }) //nolint:errcheck,gosec
	<-srv.Ready()

	hostPK, hostSK := cipher.GenerateKeyPair()
	dmsgHost := dmsg.NewClient(hostPK, hostSK, dc, &dmsg.Config{MinSessions: 1})
	go dmsgHost.Serve(context.Background())
	t.Cleanup(func() { dmsgHost.Close() }) //nolint:errcheck,gosec
	<-dmsgHost.Ready()

	log := logging.MustGetLogger("test_invalid_port")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})

	// First listen should succeed.
	_, err = dmsgHost.Listen(dmsgHTTPPort)
	require.NoError(t, err)

	// Second listen on the same port should fail.
	err = dmsghttp.ListenAndServe(ctx, hostSK, handler, dc, dmsgHTTPPort, dmsgHost, log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dmsg listen")
}
