// Package dmsghttp_test pkg/dmsghttp/debug_test.go
package dmsghttp_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
)

func TestDebugMux_PprofEndpoints(t *testing.T) {
	mux := dmsghttp.DebugMux()

	endpoints := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
		"/debug/pprof/allocs",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, ep, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.NotEqual(t, http.StatusNotFound, w.Code, "endpoint %s should be registered", ep)
		})
	}
}

func TestWhitelistMiddleware_EmptyWhitelistAllowsAll(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := dmsghttp.WhitelistMiddleware(nil, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "somekey:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWhitelistMiddleware_AllowsWhitelistedPK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := dmsghttp.WhitelistMiddleware([]cipher.PubKey{pk}, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = pk.String() + ":1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWhitelistMiddleware_RejectsNonWhitelistedPK(t *testing.T) {
	whitelisted, _ := cipher.GenerateKeyPair()
	nonWhitelisted, _ := cipher.GenerateKeyPair()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := dmsghttp.WhitelistMiddleware([]cipher.PubKey{whitelisted}, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = nonWhitelisted.String() + ":1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWhitelistMiddleware_InvalidRemoteAddr(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := dmsghttp.WhitelistMiddleware([]cipher.PubKey{pk}, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "invalid-no-port"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestServeDebug_Integration(t *testing.T) {
	// Integration test: start a dmsg server + client, serve debug, and fetch pprof index
	dc := disc.NewMock(0)

	// Create and start server
	srvPK, srvSK := cipher.GenerateKeyPair()
	srvConf := dmsg.ServerConfig{MaxSessions: 100}
	srv := dmsg.NewServer(srvPK, srvSK, dc, &srvConf, nil)

	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Serve(lis, "") //nolint:errcheck
	<-srv.Ready()
	defer srv.Close() //nolint:errcheck

	// Create debug client that connects through the server
	clientPK, clientSK := cipher.GenerateKeyPair()
	dmsgC := dmsg.NewClient(clientPK, clientSK, dc, &dmsg.Config{MinSessions: 1})
	go dmsgC.Serve(ctx) //nolint:errcheck
	<-dmsgC.Ready()
	defer dmsgC.Close() //nolint:errcheck

	// Serve debug on the client
	log := logging.MustGetLogger("test-debug")
	go dmsghttp.ServeDebug(ctx, dmsgC, log, nil) //nolint:errcheck

	// Allow listener to start
	time.Sleep(100 * time.Millisecond)

	// Create a second client to access the debug interface
	fetchPK, fetchSK := cipher.GenerateKeyPair()
	fetchC := dmsg.NewClient(fetchPK, fetchSK, dc, &dmsg.Config{MinSessions: 1})
	go fetchC.Serve(ctx) //nolint:errcheck
	<-fetchC.Ready()
	defer fetchC.Close() //nolint:errcheck

	httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, fetchC)}
	resp, err := httpC.Get(fmt.Sprintf("dmsg://%s:%d/debug/pprof/", clientPK.Hex(), dmsghttp.DefaultDebugPort))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "pprof")
}
