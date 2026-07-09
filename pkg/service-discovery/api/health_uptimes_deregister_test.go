package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	sdmetrics "github.com/skycoin/skywire/pkg/service-discovery/metrics"
	"github.com/skycoin/skywire/pkg/service-discovery/store"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// newTestAPI builds an API with the given store, no auth (nonceDB nil so the
// httpauth middleware is skipped), metrics disabled, and a fixed dmsg address.
func newTestAPI(t *testing.T, db store.Store) *API {
	t.Helper()
	return New(logging.MustGetLogger("test_sd"), db, nil, false,
		sdmetrics.NewEmpty(), "test-dmsg-addr", deployment.Prod.GeoIP)
}

func TestAPI_Health(t *testing.T) {
	api := newTestAPI(t, &store.MockStore{})

	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var resp HealthCheckResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Equal(t, "service-discovery", resp.ServiceName)
	require.Equal(t, "test-dmsg-addr", resp.DmsgAddr)
	require.False(t, resp.StartedAt.IsZero())
}

// primeUptimesCache builds an API whose in-memory uptimes caches are populated
// via the real refresh path: v1 summaries for the default cache, v2 for the
// v2/v3 cache. Returns the API plus the two PKs seeded.
func primeUptimesCache(t *testing.T) (*API, cipher.PubKey, cipher.PubKey) {
	t.Helper()
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	v1 := []store.VisorSummary{{PK: pk1}, {PK: pk2}}
	v2 := []store.VisorSummary{{PK: pk1}, {PK: pk2}}

	db := &store.MockStore{}
	db.On("GetAllVisorSummaries", mock.Anything, false, false).Return(v1, nil)
	db.On("GetAllVisorSummaries", mock.Anything, true, false).Return(v2, nil)

	api := newTestAPI(t, db)
	api.refreshUptimesCache(context.Background(), api.log)
	return api, pk1, pk2
}

func decodeSummaries(t *testing.T, rr *httptest.ResponseRecorder) []store.VisorSummary {
	t.Helper()
	require.Equal(t, http.StatusOK, rr.Code)
	var out []store.VisorSummary
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	return out
}

func serve(api *API, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, req)
	return rr
}

func TestAPI_GetUptimes(t *testing.T) {
	api, pk1, _ := primeUptimesCache(t)

	t.Run("v1 default returns whole cache", func(t *testing.T) {
		got := decodeSummaries(t, serve(api, httptest.NewRequest(http.MethodGet, "/uptimes", nil)))
		require.Len(t, got, 2)
	})

	t.Run("v2 returns v2 cache", func(t *testing.T) {
		got := decodeSummaries(t, serve(api, httptest.NewRequest(http.MethodGet, "/uptimes?v=v2", nil)))
		require.Len(t, got, 2)
	})

	t.Run("visors filter narrows to one PK", func(t *testing.T) {
		got := decodeSummaries(t, serve(api, httptest.NewRequest(http.MethodGet, "/uptimes?visors="+pk1.Hex(), nil)))
		require.Len(t, got, 1)
		require.Equal(t, pk1.Hex(), got[0].PK.Hex())
	})

	t.Run("v3 computes timeline per filtered visor", func(t *testing.T) {
		tl := map[string]string{"2026-04-28": "up"}
		api.db.(*store.MockStore).On("GetDailyTimeline", mock.Anything, pk1.Hex(), mock.Anything).Return(tl)
		got := decodeSummaries(t, serve(api, httptest.NewRequest(http.MethodGet, "/uptimes?v=v3&visors="+pk1.Hex(), nil)))
		require.Len(t, got, 1)
		require.Equal(t, tl, got[0].Timeline)
	})
}

func TestAPI_PostUptimes(t *testing.T) {
	api, pk1, _ := primeUptimesCache(t)

	t.Run("v2 bulk query filters by pks", func(t *testing.T) {
		body, _ := json.Marshal(bulkUptimesRequest{PKs: []string{pk1.Hex()}, Version: "v2"})
		got := decodeSummaries(t, serve(api, httptest.NewRequest(http.MethodPost, "/uptimes", bytes.NewReader(body))))
		require.Len(t, got, 1)
		require.Equal(t, pk1.Hex(), got[0].PK.Hex())
	})

	t.Run("no pks returns whole cache", func(t *testing.T) {
		body, _ := json.Marshal(bulkUptimesRequest{})
		got := decodeSummaries(t, serve(api, httptest.NewRequest(http.MethodPost, "/uptimes", bytes.NewReader(body))))
		require.Len(t, got, 2)
	})

	t.Run("invalid JSON is 400", func(t *testing.T) {
		rr := serve(api, httptest.NewRequest(http.MethodPost, "/uptimes", bytes.NewReader([]byte("{not json"))))
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestAPI_DeregisterEntry_Unauthorized(t *testing.T) {
	api := newTestAPI(t, &store.MockStore{})

	// No NM-PK header → not in the whitelist → 403, before any store access.
	rr := serve(api, httptest.NewRequest(http.MethodDelete, "/api/services/deregister/vpn", nil))
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAPI_DeregisterEntry_Success(t *testing.T) {
	nmPK, nmSK := cipher.GenerateKeyPair()
	WhitelistPKs.Set(nmPK.Hex())
	t.Cleanup(func() { delete(WhitelistPKs, nmPK.Hex()) })

	sig, err := cipher.SignPayload([]byte(nmPK.Hex()), nmSK)
	require.NoError(t, err)

	deadPK, _ := cipher.GenerateKeyPair()
	db := &store.MockStore{}
	db.On("DeleteService", mock.Anything, "vpn", mock.Anything).Return((*servicedisc.HTTPError)(nil))
	api := newTestAPI(t, db)

	body, _ := json.Marshal([]string{deadPK.Hex()})
	req := httptest.NewRequest(http.MethodDelete, "/api/services/deregister/vpn", bytes.NewReader(body))
	req.Header.Set("NM-PK", nmPK.Hex())
	req.Header.Set("NM-Sign", sig.Hex())

	rr := serve(api, req)
	require.Equal(t, http.StatusOK, rr.Code)
	db.AssertCalled(t, "DeleteService", mock.Anything, "vpn", mock.Anything)
}
