package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/serviceuptime"
	"github.com/skycoin/skywire/pkg/transport"
)

type errorSetter interface {
	SetError(error)
}

var testPubKey, testSec = cipher.GenerateKeyPair()

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

func newTestEntry() *transport.Entry {
	pk1, _ := cipher.GenerateKeyPair()
	return &transport.Entry{
		ID:    uuid.New(),
		Edges: transport.SortEdges(testPubKey, pk1),
		Type:  "dmsg",
	}
}

func newTestStore(t *testing.T) store.TransportStore {
	ctx := context.Background()
	storeConfig := storeconfig.Config{Type: storeconfig.Memory}
	logger := logging.MustGetLogger("test")
	s, err := store.New(ctx, storeConfig, 10*time.Minute, logger)
	require.NoError(t, err)
	return s
}

func TestBadRequest(t *testing.T) {
	mock := newTestStore(t)
	ctx := context.TODO()
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/transports/", bytes.NewBufferString("not-a-json"))
	r.Header = validHeaders(t, []byte("not-a-json"))

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")
	api.ServeHTTP(w, r)

	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, w.Code, resp.Status)

	assert.NoError(t, resp.Body.Close())
}

func TestRegisterTransport(t *testing.T) {
	mock := newTestStore(t)
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	sEntry := &transport.SignedEntry{Entry: newTestEntry(), Signatures: [2]cipher.Sig{}}
	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")
	w := httptest.NewRecorder()

	body := bytes.NewBuffer(nil)
	require.NoError(t, json.NewEncoder(body).Encode([]*transport.SignedEntry{sEntry}))
	r := httptest.NewRequest("POST", "/transports/", body)
	r.Header = validHeaders(t, body.Bytes())
	api.ServeHTTP(w, r)

	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp []*transport.SignedEntry
	require.NoError(t, json.NewDecoder(bytes.NewBuffer(w.Body.Bytes())).Decode(&resp))

	require.Len(t, resp, 1)
	assert.Equal(t, sEntry.Entry, resp[0].Entry)
}

func TestRegisterTimeout(t *testing.T) {
	const timeout = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	mock := newTestStore(t)
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	sEntry := &transport.SignedEntry{Entry: newTestEntry(), Signatures: [2]cipher.Sig{}}
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")

	// Wait until the context's cancel goroutine has actually run and ctx.Err()
	// is set. time.Sleep alone doesn't guarantee this on platforms with coarse
	// timer resolution (notably Windows), where a 2x-deadline sleep can still
	// race the WithTimeout cancel.
	<-ctx.Done()

	mock.(errorSetter).SetError(ctx.Err())

	w := httptest.NewRecorder()
	body := bytes.NewBuffer(nil)
	require.NoError(t, json.NewEncoder(body).Encode([]*transport.SignedEntry{sEntry}))
	r := httptest.NewRequest("POST", "/transports/", body)
	r.Header = validHeaders(t, body.Bytes())

	api.ServeHTTP(w, r.WithContext(ctx))

	require.Equal(t, http.StatusRequestTimeout, w.Code, w.Body.String())
}

func TestGETTransportByID(t *testing.T) {
	mock := newTestStore(t)
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	ctx := context.Background()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")

	entry := newTestEntry()
	sEntry := &transport.SignedEntry{Entry: entry, Signatures: [2]cipher.Sig{}}
	require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, sEntry))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/transports/id:%s", entry.ID), nil)
	r.Header = validHeaders(t, nil)
	api.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp *transport.Entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, entry, resp)

	t.Run("Persistence", func(t *testing.T) {
		found, err := mock.GetTransportByID(ctx, entry.ID)
		require.NoError(t, err)
		assert.Equal(t, found, entry)
	})
}

func TestDELETETransportByID(t *testing.T) {
	mock := newTestStore(t)
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	ctx := context.Background()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")

	entry := newTestEntry()
	sEntry := &transport.SignedEntry{Entry: entry, Signatures: [2]cipher.Sig{}}

	t.Run("can delete own transport", func(t *testing.T) {
		require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, sEntry))
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/transports/id:%s", entry.ID), nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		_, err := mock.GetTransportByID(context.TODO(), entry.ID)
		require.Equal(t, store.ErrTransportNotFound, err)
	})

	t.Run("cannot delete transport of unauthorized visor", func(t *testing.T) {
		ctx := context.TODO()
		nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
		require.NoError(t, err)

		api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")
		pk1, _ := cipher.GenerateKeyPair()
		pk2, _ := cipher.GenerateKeyPair()
		otherVisorEntry := &transport.Entry{
			ID:    uuid.New(),
			Edges: transport.SortEdges(pk1, pk2),
			Type:  "dmsg",
		}
		sEntry := &transport.SignedEntry{Entry: otherVisorEntry, Signatures: [2]cipher.Sig{}}
		require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, sEntry))

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/transports/id:%s", otherVisorEntry.ID), nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

		e, err := mock.GetTransportByID(context.TODO(), otherVisorEntry.ID)
		require.NoError(t, err)
		require.Equal(t, otherVisorEntry, e)
	})
}

func TestGETTransportByEdge(t *testing.T) {
	mock := newTestStore(t)
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	ctx := context.Background()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")

	entry := newTestEntry()
	sEntry := &transport.SignedEntry{Entry: entry, Signatures: [2]cipher.Sig{}}
	require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, sEntry))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/transports/edge:%s", entry.Edges[0]), nil)
	r.Header = validHeaders(t, nil)
	api.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp []*transport.Entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp, 1)

	t.Run("Persistence", func(t *testing.T) {
		found, err := mock.GetTransportByID(ctx, entry.ID)
		require.NoError(t, err)
		assert.Equal(t, found, entry)
	})
}

func TestGETAllTransports(t *testing.T) {
	mock := newTestStore(t)
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	ctx := context.Background()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")

	entry1 := newTestEntry()
	sEntry1 := &transport.SignedEntry{Entry: entry1, Signatures: [2]cipher.Sig{}}
	require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, sEntry1))

	entry2 := newTestEntry()
	sEntry2 := &transport.SignedEntry{Entry: entry2, Signatures: [2]cipher.Sig{}}
	require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, sEntry2))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/all-transports", nil)
	r.Header = validHeaders(t, nil)
	api.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp []*transport.Entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp, 2)

	t.Run("Persistence", func(t *testing.T) {
		found, err := mock.GetAllTransports(ctx, true)
		require.NoError(t, err)
		for i, f := range found {
			if f.ID == resp[i].ID {
				assert.EqualValues(t, *f, *resp[i])
			} else {
				j := (i + 1) % 2
				assert.EqualValues(t, *f, *resp[j])
			}
		}
	})
}

func TestGETIncrementingNonces(t *testing.T) {
	mock := newTestStore(t)
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	pubKey, _ := cipher.GenerateKeyPair()

	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")

	t.Run("ValidRequest", func(t *testing.T) {
		const iterations = 0xFF

		for i := 0; i < iterations; i++ {
			_, err := nonceMock.IncrementNonce(context.Background(), pubKey)
			require.NoError(t, err)
		}

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/security/nonces/%s", pubKey), nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r.WithContext(context.Background()))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var resp httpauth.NextNonceResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.Equal(t, pubKey, resp.Edge)
		assert.Equal(t, httpauth.Nonce(iterations), resp.NextNonce)
	})

	t.Run("StoreError", func(t *testing.T) {
		boom := errors.New("boom")
		nonceMock.(errorSetter).SetError(boom)
		defer mock.(errorSetter).SetError(nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/security/nonces/%s", pubKey), nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), boom.Error())
	})

	t.Run("InvalidKey", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/security/nonces/foo-bar", nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "Invalid public key")
	})
}

// --- additional coverage: public endpoints, helpers, background tasks --------

func newTestAPI(t *testing.T) *API {
	t.Helper()
	mock := newTestStore(t)
	nonceMock, err := httpauth.NewNonceStore(context.TODO(), storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	return New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "dmsg-addr", "")
}

// serveUnique routes one request through the API with a unique RemoteAddr so
// the per-IP rate limiter (burst 10) never trips across a table of requests.
func serveUnique(api *API, idx int, method, path string, body []byte, hdr http.Header) *httptest.ResponseRecorder {
	var b *bytes.Buffer
	if body != nil {
		b = bytes.NewBuffer(body)
	} else {
		b = bytes.NewBuffer(nil)
	}
	r := httptest.NewRequest(method, path, b)
	r.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1234", (idx/250)%250, idx%250, (idx*7)%250)
	if hdr != nil {
		r.Header = hdr
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}

func TestPublicReadEndpoints(t *testing.T) {
	api := newTestAPI(t)
	pk := testPubKey.Hex()
	id := uuid.New().String()

	paths := []string{
		"/health",
		"/all-transports/stats",
		"/all-transports/per-key-stats",
		"/transports/stats/" + pk,
		"/v3/transports/edge:" + pk,
		"/bandwidth/transport/" + id,
		"/bandwidth/visor/" + pk,
		"/metric",
		"/metric/visor/" + pk,
		"/metrics",
		"/metrics/" + id,
		"/metrics/visor/" + pk,
		"/uptimes",
		"/uptimes?v=v2",
		"/uptimes?v=v3",
		"/uptimes?visors=" + pk,
		"/uptimes/transports",
		"/uptimes/transports?v=v2&visors=" + pk,
		"/uptimes/transports?v=v3&edges=true",
		"/metrics/uptime",
		"/metrics/uptime?v=v2",
		"/metrics/uptime/" + id,
		"/metrics/uptime/visor/" + pk,
		"/version",
		"/versions",
		"/versions?v=v2",
		"/versions/" + pk,
		"/all-transports?status=true",
	}
	for i, p := range paths {
		w := serveUnique(api, i, http.MethodGet, p, nil, nil)
		require.Less(t, w.Code, http.StatusInternalServerError, "%s -> %d: %s", p, w.Code, w.Body.String())
	}

	// /health reports the service name.
	w := serveUnique(api, 100, http.MethodGet, "/health", nil, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "transport-discovery")
}

func TestPublicPostEndpoints(t *testing.T) {
	api := newTestAPI(t)
	pk := testPubKey.Hex()

	// POST /transports/edges — body is a JSON list of edge PKs.
	edgesBody, _ := json.Marshal([]string{pk}) //nolint
	w := serveUnique(api, 1, http.MethodPost, "/transports/edges", edgesBody, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// POST /uptimes — bulk uptimes request (v1, v2, and v3 dispatch).
	upBody, _ := json.Marshal(map[string]any{"pks": []string{pk}}) //nolint
	w = serveUnique(api, 2, http.MethodPost, "/uptimes", upBody, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	for i, ver := range []string{"v2", "v3"} {
		b, _ := json.Marshal(map[string]any{"pks": []string{pk}, "version": ver}) //nolint
		w = serveUnique(api, 20+i, http.MethodPost, "/uptimes", b, nil)
		require.Less(t, w.Code, http.StatusInternalServerError, "version=%s: %s", ver, w.Body.String())
	}

	// POST /uptimes/transports — bulk transport uptimes request.
	w = serveUnique(api, 3, http.MethodPost, "/uptimes/transports", upBody, nil)
	require.Less(t, w.Code, http.StatusInternalServerError, w.Body.String())

	// POST /statuses is gone.
	w = serveUnique(api, 4, http.MethodPost, "/statuses", nil, nil)
	require.Equal(t, http.StatusGone, w.Code)

	// Malformed edges body → bad request (exercises the parse-error path).
	w = serveUnique(api, 5, http.MethodPost, "/transports/edges", []byte("not-json"), nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeregisterUnauthorized(t *testing.T) {
	api := newTestAPI(t)
	// No NM-PK header → the network-monitor whitelist check rejects it.
	w := serveUnique(api, 1, http.MethodDelete, "/transports/deregister", nil, nil)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestUptimeRoutesWithoutRecorder(t *testing.T) {
	api := newTestAPI(t)
	for i, p := range []string{"/uptime/now", "/uptime/sessions", "/uptime/timeline", "/uptime/dates"} {
		w := serveUnique(api, i, http.MethodGet, p, nil, nil)
		require.Equal(t, http.StatusServiceUnavailable, w.Code, p)
	}
}

func TestAuthedEndpoints(t *testing.T) {
	// Each sub-test gets its own API (hence its own nonce store): the auth
	// middleware increments the per-PK nonce, so reusing one store would
	// 401 every request after the first (which still passes the <500
	// assertion but skips the handler).

	t.Run("registerTransportV3", func(t *testing.T) {
		body, err := json.Marshal([]*transport.Entry{newTestEntry()})
		require.NoError(t, err)
		w := serveUnique(newTestAPI(t), 1, http.MethodPost, "/v3/transports/", body, validHeaders(t, body))
		require.Less(t, w.Code, http.StatusInternalServerError, w.Body.String())
	})

	t.Run("visorHeartbeat", func(t *testing.T) {
		w := serveUnique(newTestAPI(t), 2, http.MethodGet, "/v4/update", nil, validHeaders(t, nil))
		require.Less(t, w.Code, http.StatusInternalServerError, w.Body.String())
	})

	t.Run("deleteTransportsBatch", func(t *testing.T) {
		body, _ := json.Marshal([]string{uuid.New().String()}) //nolint
		w := serveUnique(newTestAPI(t), 3, http.MethodPost, "/transports/delete-batch", body, validHeaders(t, body))
		require.Less(t, w.Code, http.StatusInternalServerError, w.Body.String())
	})
}

func TestRunBackgroundTasksOnce(t *testing.T) {
	api := newTestAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled → one refresh pass, then the loop returns
	api.RunBackgroundTasks(ctx, logging.MustGetLogger("t"))

	// Exercise the cache getters (empty store → may be nil; we only care
	// that the refresh pass + getters ran without panicking).
	_ = api.getTransportsFromCache(true)
	_ = api.getTransportsFromCache(false)
	_ = api.getUptimesFromCache()
	_ = api.getUptimesV2FromCache()
}

func TestWriteErrorStatuses(t *testing.T) {
	api := newTestAPI(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	cases := []struct {
		err  error
		want int
	}{
		{ErrEmptyPubKey, http.StatusBadRequest},
		{ErrInvalidPubKey, http.StatusBadRequest},
		{ErrEmptyTransportID, http.StatusBadRequest},
		{ErrInvalidTransportID, http.StatusBadRequest},
		{store.ErrTransportNotFound, http.StatusNotFound},
		{store.ErrAlreadyRegistered, http.StatusConflict},
		{context.DeadlineExceeded, http.StatusRequestTimeout},
		{&json.SyntaxError{}, http.StatusBadRequest},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		api.writeError(w, r, c.err)
		require.Equal(t, c.want, w.Code, c.err.Error())
	}
}

func TestPureParseHelpers(t *testing.T) {
	require.Equal(t, []string{"a", "b", "c"}, splitPKs("a, b ,c")) // comma-split + trim
	require.Empty(t, splitPKs(""))

	ids := parseTpIDs(uuid.New().String() + ";" + uuid.New().String() + ";not-a-uuid")
	require.Len(t, ids, 2) // semicolon-split; invalid one dropped

	pks, err := parsePKs(testPubKey.Hex())
	require.NoError(t, err)
	require.Len(t, pks, 1)

	parsedIDs, err := parseIDs(uuid.New().String())
	require.NoError(t, err)
	require.Len(t, parsedIDs, 1)

	// filterByPKs keeps only matching summaries.
	other, _ := cipher.GenerateKeyPair()
	summaries := []store.VisorSummary{{PK: testPubKey}, {PK: other}}
	require.Len(t, filterByPKs(summaries, testPubKey.Hex()), 1)
	require.Empty(t, filterByPKs(summaries, ""))
}

// --- CXO register + pure helpers (no CXO node needed) ------------------------

func TestRegisterDeregisterFromCXO(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	entry := newTestEntry() // edges = sorted(testPubKey, pk1)
	reporter := entry.Edges[0]
	other, _ := cipher.GenerateKeyPair()

	require.Error(t, api.RegisterTransportFromCXO(ctx, nil, reporter, "v1")) // nil entry
	require.Error(t, api.RegisterTransportFromCXO(ctx, entry, other, "v1"))  // reporter not an edge
	require.NoError(t, api.RegisterTransportFromCXO(ctx, entry, reporter, "v1.0.0"))

	require.NoError(t, api.DeregisterTransportFromCXO(ctx, uuid.New(), reporter)) // unknown id → no-op
	require.Error(t, api.DeregisterTransportFromCXO(ctx, entry.ID, other))        // reporter not an edge
	require.NoError(t, api.DeregisterTransportFromCXO(ctx, entry.ID, reporter))   // removes it
}

func TestCXOPathHelpers(t *testing.T) {
	require.Equal(t, MetricsPath(7), metricsPath(7))
	require.Equal(t, "uptimes/days/7", UptimePath(7))
	require.NotEmpty(t, MetricsPath(30))
}

func TestTrimSummariesToDays(t *testing.T) {
	now := time.Now()
	recent := now.Format("2006-01-02")
	old := now.AddDate(0, 0, -10).Format("2006-01-02")
	in := []store.VisorSummary{{
		PK:       testPubKey,
		Daily:    map[string]string{recent: "100", old: "50"},
		Timeline: map[string]string{recent: "x", old: "y"},
	}}

	// days <= 0 → no trim.
	require.Equal(t, in, trimSummariesToDays(in, 0, now))

	// days=3 drops the 10-day-old entries.
	out := trimSummariesToDays(in, 3, now)
	require.Len(t, out, 1)
	require.Contains(t, out[0].Daily, recent)
	require.NotContains(t, out[0].Daily, old)
	require.NotContains(t, out[0].Timeline, old)
}

func TestCXOPublisherErrorTracking(t *testing.T) {
	// recordError / LastError only touch mu + lastError, so zero-value
	// struct literals are safe (no CXO node required).
	boom := errors.New("boom")

	a := &AllTransportsCXOPublisher{}
	require.NoError(t, a.LastError())
	a.recordError(boom)
	require.Error(t, a.LastError())

	m := &MetricsCXOPublisher{}
	m.recordError(boom)
	require.Error(t, m.LastError())

	u := &UptimeCXOPublisher{}
	u.recordError(boom)
	require.Error(t, u.LastError())
}

// --- DHT mirror + uptime recorder --------------------------------------------

type fakeDHTMirror struct{ mirrored, deleted int }

func (m *fakeDHTMirror) Mirror(cipher.PubKey, interface{}, uint64)       { m.mirrored++ }
func (m *fakeDHTMirror) MirrorMany([]cipher.PubKey, interface{}, uint64) { m.mirrored++ }
func (m *fakeDHTMirror) Delete(cipher.PubKey)                            { m.deleted++ }

func TestDHTMirror(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	mir := &fakeDHTMirror{}
	api.SetDHTMirror(mir)

	entry := newTestEntry()
	reporter := entry.Edges[0]

	// Registering an edge with a live transport → mirrorEdges publishes it.
	require.NoError(t, api.RegisterTransportFromCXO(ctx, entry, reporter, "v1.0.0"))
	require.Positive(t, mir.mirrored)

	// Backfill walks every edge and mirrors its transport list.
	api.BackfillDHTMirror(ctx, logging.MustGetLogger("t"))

	// Deregistering empties the edge → mirrorEdges deletes the DHT entry.
	require.NoError(t, api.DeregisterTransportFromCXO(ctx, entry.ID, reporter))
	require.Positive(t, mir.deleted)
}

func TestUptimeRecorderRoutes(t *testing.T) {
	api := newTestAPI(t)

	// Before a recorder is wired, the /uptime/* routes report 503.
	require.Equal(t, http.StatusServiceUnavailable, serveUnique(api, 0, http.MethodGet, "/uptime/now", nil, nil).Code)

	rec, err := serviceuptime.New(filepath.Join(t.TempDir(), "uptime.db"), serviceuptime.Config{Service: "transport-discovery"})
	require.NoError(t, err)
	// Close the recorder (and its bbolt DB) before the test ends, otherwise the
	// open DB file keeps the temp dir locked on Windows and t.TempDir cleanup
	// fails with "being used by another process".
	t.Cleanup(func() { _ = rec.Close() }) //nolint:errcheck
	api.SetUptimeRecorder(rec)
	require.NotNil(t, api.getUptimeRecorder())

	for i, p := range []string{"/uptime/now", "/uptime/sessions", "/uptime/timeline", "/uptime/dates"} {
		w := serveUnique(api, i+1, http.MethodGet, p, nil, nil)
		require.Less(t, w.Code, http.StatusInternalServerError, p)
	}
}
