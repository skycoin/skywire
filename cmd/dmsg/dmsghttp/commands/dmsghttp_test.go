// Package commands — dmsghttp_test.go: unit tests for the gin-layer
// helpers (whitelist auth middleware, logging middleware, the GinHandler
// adapter) and the pure color/format helpers. The server() run loop is
// dmsg-networking and is not unit-tested here.
package commands

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// --- whitelistAuth ---------------------------------------------------

func newAuthEngine(pks []cipher.PubKey) *gin.Engine {
	r := gin.New()
	r.Use(whitelistAuth(pks))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

func TestWhitelistAuth(t *testing.T) {
	allowed, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	t.Run("whitelisted PK passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		// The middleware reads the host portion of RemoteAddr and compares
		// it to the whitelisted PK string.
		req.RemoteAddr = allowed.String() + ":80"
		w := httptest.NewRecorder()
		newAuthEngine([]cipher.PubKey{allowed}).ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("non-whitelisted PK is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = other.String() + ":80"
		w := httptest.NewRecorder()
		newAuthEngine([]cipher.PubKey{allowed}).ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("empty whitelist allows everyone", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = other.String() + ":80"
		w := httptest.NewRecorder()
		newAuthEngine([]cipher.PubKey{}).ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("malformed RemoteAddr yields 500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "no-port-here" // SplitHostPort fails
		w := httptest.NewRecorder()
		newAuthEngine([]cipher.PubKey{allowed}).ServeHTTP(w, req)
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// --- GinHandler + loggingMiddleware ----------------------------------

func TestGinHandlerAndLoggingMiddleware(t *testing.T) {
	r := gin.New()
	r.Use(loggingMiddleware())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "hi") })

	h := &GinHandler{Router: r}

	// Redirect stdout so the middleware's log line doesn't pollute output.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = devnull
	defer func() { os.Stdout = orig; _ = devnull.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "hi", w.Body.String())
}

// --- pure color/format helpers ---------------------------------------

func TestGetBackgroundColor(t *testing.T) {
	require.Equal(t, green, getBackgroundColor(http.StatusOK))           // 2xx
	require.Equal(t, white, getBackgroundColor(http.StatusMovedPermanently)) // 3xx
	require.Equal(t, yellow, getBackgroundColor(http.StatusBadRequest))  // 4xx
	require.Equal(t, red, getBackgroundColor(http.StatusInternalServerError)) // 5xx
}

func TestGetMethodColor(t *testing.T) {
	cases := map[string]string{
		http.MethodGet:     blue,
		http.MethodPost:    cyan,
		http.MethodPut:     yellow,
		http.MethodDelete:  red,
		http.MethodPatch:   green,
		http.MethodHead:    magenta,
		http.MethodOptions: white,
		"TRACE":            reset, // default branch
	}
	for method, want := range cases {
		require.Equal(t, want, getMethodColor(method), "method=%s", method)
	}
}

func TestResetColor(t *testing.T) {
	require.Equal(t, reset, resetColor())
}

// --- server() early-exit path ----------------------------------------

// TestServerEarlyExitOnDmsgError drives server()'s setup to completion
// and out via its early return. Setting dmsgclient.DmsgServerAddr to an
// invalid value makes InitDmsgWithFlags fail fast at ParseServerAddr (no
// network dial, no blocking on dmsg readiness), so server() logs the
// error and returns instead of proceeding to Listen/Serve. This covers
// the pprof/config/keypair/whitelist/proxy setup without standing up a
// real dmsg network.
func TestServerEarlyExitOnDmsgError(t *testing.T) {
	// Snapshot and restore the package + dmsgclient globals server() reads.
	origServerAddr := dmsgclient.DmsgServerAddr
	origWl, origProxy, origSK, origPK, origWlkeys, origErr := wl, proxyAddr, sk, pk, wlkeys, err
	t.Cleanup(func() {
		dmsgclient.DmsgServerAddr = origServerAddr
		wl, proxyAddr, sk, pk, wlkeys, err = origWl, origProxy, origSK, origPK, origWlkeys, origErr
	})

	dmsgclient.DmsgServerAddr = "not-a-valid-server-addr" // → ParseServerAddr error

	good, _ := cipher.GenerateKeyPair()
	wl = []string{good.Hex(), "invalid-key"} // exercises valid-append + invalid-skip
	wlkeys = nil
	proxyAddr = "127.0.0.1:1080" // valid host:port → SOCKS5 dialer builds (lazy, no connect)
	sk = cipher.SecKey{}         // zero → PubKey() errors → GenerateKeyPair branch

	done := make(chan struct{})
	go func() {
		defer close(done)
		server()
	}()

	select {
	case <-done:
		// server() returned via the early-exit path as expected.
	case <-time.After(15 * time.Second):
		t.Fatal("server() did not return — it likely blocked on dmsg setup")
	}

	require.Len(t, wlkeys, 1, "only the valid whitelist key should be parsed")
}
