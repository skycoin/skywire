// Package api pkg/deployment/tpd/api/snapshot_headers_test.go c4-net-discovery
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// /all-transports/stats and /all-transports/per-key-stats are two reductions
// of one cached slice, so read off one snapshot the per-key sum is exactly
// twice the aggregate total — every transport is two edges and nothing is
// deduplicated. The headers say WHICH snapshot each body came from, which is
// what lets a consumer check that identity instead of checking it against
// whatever the aggregate happens to read at some other moment.
//
// Without the stamp a consumer comparing the two across the network total's
// own movement (measured at 2.4x inside forty seconds, skycoin/skywire#4513)
// finds them tens of percent apart and concludes the per-key index is losing
// edges. It is not; the two fetches described different moments.
func TestSnapshotHeadersMakeTheTwoViewsComparable(t *testing.T) {
	ctx := context.Background()
	mock := newTestStore(t)
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)

	a1, _ := cipher.GenerateKeyPair()
	b1, _ := cipher.GenerateKeyPair()
	b2, _ := cipher.GenerateKeyPair()
	c1, _ := cipher.GenerateKeyPair()
	c2, _ := cipher.GenerateKeyPair()
	seed := []*transport.SignedEntry{
		{Entry: &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a1, b1), Type: "stcpr"}},
		{Entry: &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a1, b2), Type: "stcpr"}},
		{Entry: &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(c1, c2), Type: "sudph"}},
	}
	require.NoError(t, mock.RegisterTransportsBatch(ctx, cipher.PubKey{}, seed))

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")

	// A cold cache shares no snapshot with anything, and says so by carrying
	// no stamp rather than by inventing one.
	cold := httptest.NewRecorder()
	api.ServeHTTP(cold, httptest.NewRequest(http.MethodGet, "/all-transports/per-key-stats", nil))
	require.Equal(t, http.StatusOK, cold.Code, cold.Body.String())
	assert.Empty(t, cold.Header().Get(SnapshotAtHeader),
		"a cold-cache body was stamped with a snapshot it was not served from")

	api.refreshTransportsCache(ctx, logging.MustGetLogger("test"))

	stats := httptest.NewRecorder()
	api.ServeHTTP(stats, httptest.NewRequest(http.MethodGet, "/all-transports/stats", nil))
	require.Equal(t, http.StatusOK, stats.Code, stats.Body.String())

	perKey := httptest.NewRecorder()
	api.ServeHTTP(perKey, httptest.NewRequest(http.MethodGet, "/all-transports/per-key-stats", nil))
	require.Equal(t, http.StatusOK, perKey.Code, perKey.Body.String())

	at := stats.Header().Get(SnapshotAtHeader)
	require.NotEmpty(t, at, "the aggregate carried no snapshot stamp")
	assert.Equal(t, at, perKey.Header().Get(SnapshotAtHeader),
		"the two views were served from different snapshots")

	total := stats.Header().Get(SnapshotTransportsHeader)
	require.Equal(t, "3", total)
	assert.Equal(t, total, perKey.Header().Get(SnapshotTransportsHeader))

	var agg struct {
		Total int `json:"total_transports"`
	}
	require.NoError(t, json.Unmarshal(stats.Body.Bytes(), &agg))

	var byKey map[string]map[string]int
	require.NoError(t, json.Unmarshal(perKey.Body.Bytes(), &byKey))
	edges := 0
	for _, counts := range byKey {
		edges += counts["total"]
	}

	assert.Equal(t, 2*agg.Total, edges,
		"one snapshot disagreed with itself: %d edges over %d transports", edges, agg.Total)
}
