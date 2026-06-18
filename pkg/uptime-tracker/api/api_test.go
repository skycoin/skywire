package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/geo"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/storeconfig"
	utmetrics "github.com/skycoin/skywire/pkg/uptime-tracker/metrics"
	"github.com/skycoin/skywire/pkg/uptime-tracker/store"
)

var testPubKey, testSec = cipher.GenerateKeyPair()

var geoFunc = func(ip net.IP) (*geo.LocationData, error) {
	wantIP := net.IPv4(127, 0, 0, 1)
	if wantIP.Equal(ip) {
		return &geo.LocationData{
			Lat: 1,
			Lon: 1,
		}, nil
	}

	return nil, errors.New("unexpected ip")
}

func TestHandleUptimes(t *testing.T) {
	mock := store.NewMemoryStore()
	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, geoFunc, false, false,
		utmetrics.NewEmpty(), 0, "", "")

	pk, _ := cipher.GenerateKeyPair()

	const iterations = 15
	for i := 0; i < iterations; i++ {
		require.NoError(t, mock.UpdateUptime(pk.String(), "127.0.0.1", ""))
	}

	w := httptest.NewRecorder()

	body := bytes.NewBuffer(make([]byte, 0))
	r := httptest.NewRequest(http.MethodGet, "/uptimes", body)
	r.Header = validHeaders(t, body.Bytes())
	api.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp store.UptimeResponse
	require.NoError(t, json.NewDecoder(bytes.NewBuffer(w.Body.Bytes())).Decode(&resp))

	require.Len(t, resp, 1)
	assert.Equal(t, pk.String(), resp[0].Key)

	assert.True(t, resp[0].Online)
}

func TestAPI_handleUpdate(t *testing.T) {
	mock := store.NewMemoryStore()

	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, geoFunc, false, false,
		utmetrics.NewEmpty(), 0, "", "")

	t.Run("StatusOK", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/v4/update", body)
		r.Header = validHeaders(t, body.Bytes())
		r.Header.Add("X-Forwarded-For", "127.0.0.1")
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("StatusUnauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/v4/update", body)
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "{\"error\":{\"message\":\"SW-Public missing\",\"code\":401}}\n", w.Body.String())
	})
}

func TestApi_UpdateRemovedMethod(t *testing.T) {
	mock := store.NewMemoryStore()

	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, geoFunc, false, false,
		utmetrics.NewEmpty(), 0, "", "")

	t.Run("StatusGone", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/update", body)
		r.Header = validHeaders(t, body.Bytes())
		r.Header.Add("X-Forwarded-For", "127.0.0.1")
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusGone, w.Code)
	})

	t.Run("StatusGonev2", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/v2/update", body)
		r.Header = validHeaders(t, body.Bytes())
		r.Header.Add("X-Forwarded-For", "127.0.0.1")
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusGone, w.Code)
	})

	t.Run("StatusUnauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/v4/update", body)
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "{\"error\":{\"message\":\"SW-Public missing\",\"code\":401}}\n", w.Body.String())
	})
}

// validHeaders returns a valid set of headers
func validHeaders(t *testing.T, payload []byte) http.Header {
	nonce := httpauth.Nonce(0)
	sig, err := httpauth.Sign(payload, nonce, testSec)
	require.NoError(t, err)

	hdr := http.Header{}
	hdr.Set("SW-Public", testPubKey.Hex())
	hdr.Set("SW-Sig", sig.Hex())
	hdr.Set("SW-Nonce", nonce.String())

	return hdr
}

func newTestAPI(t *testing.T, storePath string) (*API, store.Store) {
	t.Helper()
	mock := store.NewMemoryStore()
	nonceMock, err := httpauth.NewNonceStore(context.TODO(), storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, geoFunc, false, false, utmetrics.NewEmpty(), 0, storePath, "dmsg-addr")
	return api, mock
}

func populate(t *testing.T, mock store.Store, pk cipher.PubKey, n int) {
	t.Helper()
	for range n {
		require.NoError(t, mock.UpdateUptime(pk.String(), "127.0.0.1", "v1.0.0"))
	}
}

func get(t *testing.T, api *API, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

// --- public handlers -------------------------------------------------

// handleVisors / handleUptimes sit behind a per-IP rate limiter in the
// router, so they're called directly here to avoid 429s from the
// repeated same-IP requests a table-driven test makes.

func TestHandleVisors(t *testing.T) {
	api, mock := newTestAPI(t, "")
	pk, _ := cipher.GenerateKeyPair()
	populate(t, mock, pk, 3)

	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.handleVisors(w, httptest.NewRequest(http.MethodGet, "/visors", nil))
		return w
	}

	// Store path (cache empty).
	require.Equal(t, http.StatusOK, call().Code)

	// Cache path.
	require.NoError(t, api.updateVisorsCache())
	require.Positive(t, api.getVisorsLen())
	require.Equal(t, http.StatusOK, call().Code)
}

func TestHandleUptimeRoute(t *testing.T) {
	api, mock := newTestAPI(t, "")
	pk, _ := cipher.GenerateKeyPair()
	populate(t, mock, pk, 2)

	t.Run("known pk", func(t *testing.T) {
		w := get(t, api, "/uptime/"+pk.Hex())
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})
	t.Run("invalid pk", func(t *testing.T) {
		w := get(t, api, "/uptime/not-a-pubkey")
		require.NotEqual(t, http.StatusOK, w.Code)
	})
	t.Run("unknown pk has no entries", func(t *testing.T) {
		other, _ := cipher.GenerateKeyPair()
		w := get(t, api, "/uptime/"+other.Hex())
		require.NotEqual(t, http.StatusOK, w.Code)
	})
}

func TestHandleUptimesVariants(t *testing.T) {
	api, mock := newTestAPI(t, "")
	pk, _ := cipher.GenerateKeyPair()
	populate(t, mock, pk, 5)
	api.updateInternalCaches(logging.MustGetLogger("t"))

	// Called directly to bypass the per-IP rate limiter.
	callUptimes := func(target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.handleUptimes(w, httptest.NewRequest(http.MethodGet, target, nil))
		return w
	}

	now := time.Now()
	cases := []string{
		"/uptimes",
		"/uptimes?visors=" + pk.String(),
		"/uptimes?status=on",
		"/uptimes?status=off",
		"/uptimes?v=v2",
		"/uptimes?visors=" + pk.String() + "&v=v2&status=on",
	}
	for _, c := range cases {
		w := callUptimes(c)
		require.Equal(t, http.StatusOK, w.Code, "%s: %s", c, w.Body.String())
	}

	// Explicit valid date range (non-current period bypasses the cache).
	last := now.AddDate(0, -1, 0)
	w := callUptimes("/uptimes?startDate=" + itoa(last.Unix()) + "&endDate=" + itoa(now.Unix()))
	require.Equal(t, http.StatusOK, w.Code)

	// Invalid date → error.
	w = callUptimes("/uptimes?startDate=bad&endDate=bad")
	require.NotEqual(t, http.StatusOK, w.Code)
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestHealthRoute(t *testing.T) {
	api, _ := newTestAPI(t, "")
	w := get(t, api, "/health")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "uptime-tracker")
}

func TestChartRoute(t *testing.T) {
	api, mock := newTestAPI(t, "")
	pk, _ := cipher.GenerateKeyPair()
	populate(t, mock, pk, 1)

	require.Equal(t, http.StatusOK, get(t, api, "/dashboard").Code)
	require.Equal(t, http.StatusOK, get(t, api, "/dashboard?length=3").Code)
}

func TestSendGoneRoutes(t *testing.T) {
	api, _ := newTestAPI(t, "")
	for _, p := range []string{"/update", "/v2/update", "/v3/update"} {
		require.Equal(t, http.StatusGone, get(t, api, p).Code, p)
	}
}

func TestHandleUpdateInvalidAuth(t *testing.T) {
	api, _ := newTestAPI(t, "")
	// Call the handler directly without the auth context value → the
	// "invalid auth" branch.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v4/update", nil)
	api.handleUpdate()(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleUptimeDateParams(t *testing.T) {
	api, mock := newTestAPI(t, "")
	pk, _ := cipher.GenerateKeyPair()
	populate(t, mock, pk, 2)
	now := time.Now()

	// Valid (current-period) date range → entry found.
	w := get(t, api, "/uptime/"+pk.Hex()+"?startDate="+itoa(now.Unix())+"&endDate="+itoa(now.Unix()))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Invalid start date → error.
	w = get(t, api, "/uptime/"+pk.Hex()+"?startDate=bad&endDate=bad")
	require.NotEqual(t, http.StatusOK, w.Code)
}

func TestWriteJSONMarshalError(t *testing.T) {
	api, _ := newTestAPI(t, "")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	// A channel can't be JSON-marshaled → the error branch (which also
	// exercises api.logger) runs and a 500 is written.
	api.writeJSON(w, r, http.StatusOK, make(chan int))
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- cache + background tasks ----------------------------------------

func TestCacheUpdatesAndGetters(t *testing.T) {
	api, mock := newTestAPI(t, "")
	pk, _ := cipher.GenerateKeyPair()
	populate(t, mock, pk, 4)

	api.updateInternalCaches(logging.MustGetLogger("t"))
	require.Positive(t, api.getVisorsLen())
	require.NotEmpty(t, api.getVisors())
	require.Positive(t, api.getAllUptimesLen())
	require.NotEmpty(t, api.getAllUptimes())
	require.NotNil(t, api.getDailyUptimes())
}

func TestUpdateInternalState(t *testing.T) {
	api, mock := newTestAPI(t, "")
	pk, _ := cipher.GenerateKeyPair()
	populate(t, mock, pk, 2)
	// updateInternalState reads the current-month count and records it
	// to the metrics sink. (RunBackgroundTasks / dailyRoutine are not
	// exercised: the memory store's GetOldestEntry returns a zero-time
	// entry, which makes dailyRoutine loop from year 1 to now.)
	api.updateInternalState(context.Background(), logging.MustGetLogger("t"))
}

func TestStoreDailyData(t *testing.T) {
	dir := t.TempDir()
	api, _ := newTestAPI(t, dir)
	day := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	require.NoError(t, api.storeDailyData([]store.DailyUptimeHistory{}, day))
	_, err := os.Stat(dir + "/2026-06-18-uptime-data.json")
	require.NoError(t, err)
}

// --- pure helpers ----------------------------------------------------

func TestWriteErrorStatuses(t *testing.T) {
	api, _ := newTestAPI(t, "")
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	w := httptest.NewRecorder()
	api.writeError(w, r, context.DeadlineExceeded)
	require.Equal(t, http.StatusRequestTimeout, w.Code)

	w = httptest.NewRecorder()
	api.writeError(w, r, &json.SyntaxError{})
	require.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	api.writeError(w, r, errors.New("boom"))
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRetrievePkFromURL(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	u, _ := url.Parse("/uptime/" + pk.Hex())
	got, err := retrievePkFromURL(u)
	require.NoError(t, err)
	require.Equal(t, pk, got)

	u2, _ := url.Parse("/uptime/not-a-key")
	_, err = retrievePkFromURL(u2)
	require.Error(t, err)
}

func TestIsCurrentPeriod(t *testing.T) {
	now := time.Now()
	require.True(t, isCurrentPeriod(now.Year(), now.Month()))
	require.False(t, isCurrentPeriod(2000, time.January))
}

func TestSelectUptimesByPKs(t *testing.T) {
	api, mock := newTestAPI(t, "")
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	populate(t, mock, pkA, 1)
	populate(t, mock, pkB, 1)
	require.NoError(t, api.updateUptimesCache())
	all := api.getAllUptimes()
	require.GreaterOrEqual(t, len(all), 2)

	sel := selectUptimesByPKs(all, []string{pkA.String()})
	require.Len(t, sel, 1)
	require.Equal(t, pkA.String(), sel[0].Key)

	require.Empty(t, selectUptimesByPKs(all, []string{"no-match"}))
}

// --- private API -----------------------------------------------------

func TestPrivateAPIVisorIPs(t *testing.T) {
	mock := store.NewMemoryStore()
	pk, _ := cipher.GenerateKeyPair()
	require.NoError(t, mock.UpdateUptime(pk.String(), "127.0.0.1", ""))
	pAPI := NewPrivate(nil, mock)

	for _, target := range []string{"/visor-ips", "/visor-ips?month=all"} {
		w := httptest.NewRecorder()
		pAPI.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}

	// Cover the private writeError + log helpers directly (the memory
	// store's GetVisorsIPs never errors, so the handler can't reach them).
	w := httptest.NewRecorder()
	pAPI.writeError(w, httptest.NewRequest(http.MethodGet, "/visor-ips", nil), errors.New("boom"))
	require.Equal(t, http.StatusInternalServerError, w.Code)
}
