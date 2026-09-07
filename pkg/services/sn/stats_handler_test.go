// Package sn stats_handler_test.go: access control and payload shape of the
// setup-node /stats handler.
package sn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
)

// collectorWithFailure returns a collector holding one recorded route-setup
// failure, plus the src/dst keys it was recorded against.
func collectorWithFailure(t *testing.T) (*setupmetrics.Collector, cipher.PubKey, cipher.PubKey) {
	t.Helper()
	c := setupmetrics.NewCollector(setupmetrics.CollectorConfig{})
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	err := errors.New("failed to reserve route ids: reserve routeID from " +
		dst.Hex() + " failed: context deadline exceeded")
	c.RecordRouteContext(context.Background(), src, dst, 2)(&err)
	return c, src, dst
}

// requestAs issues a GET /stats whose RemoteAddr is "<pk>:<port>", the shape
// an http.Server sees when the listener is a dmsg listener.
func requestAs(h http.Handler, pk cipher.PubKey) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	req.RemoteAddr = pk.Hex() + ":80"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestStatsHandlerRequiresWhitelist is the access control the old code assumed
// it had: dmsghttp.WithDebug gates only /debug/, so /stats was open to anyone
// who could dial the setup node.
func TestStatsHandlerRequiresWhitelist(t *testing.T) {
	c, _, _ := collectorWithFailure(t)
	allowed, _ := cipher.GenerateKeyPair()
	stranger, _ := cipher.GenerateKeyPair()

	h := statsHandler(c, []cipher.PubKey{allowed})

	require.Equal(t, http.StatusUnauthorized, requestAs(h, stranger).Code)
	require.Equal(t, http.StatusOK, requestAs(h, allowed).Code)
}

// TestStatsHandlerEmptyWhitelistDeniesAll — an unconfigured whitelist must not
// fail open (WhitelistMiddleware's contract; asserted here because /stats now
// depends on it).
func TestStatsHandlerEmptyWhitelistDeniesAll(t *testing.T) {
	c, _, _ := collectorWithFailure(t)
	caller, _ := cipher.GenerateKeyPair()

	require.Equal(t, http.StatusUnauthorized, requestAs(statsHandler(c, nil), caller).Code)
}

// TestStatsHandlerServesStructuredPKs asserts the fields the old handler
// blanked are present for an authorized caller — and shows why blanking them
// achieved nothing: the same key is in the unsanitized error string.
func TestStatsHandlerServesStructuredPKs(t *testing.T) {
	c, src, dst := collectorWithFailure(t)
	allowed, _ := cipher.GenerateKeyPair()

	rec := requestAs(statsHandler(c, []cipher.PubKey{allowed}), allowed)
	require.Equal(t, http.StatusOK, rec.Code)

	var snap setupmetrics.StatsSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &snap))

	require.Len(t, snap.RecentFailures, 1)
	require.Equal(t, src.Hex(), snap.RecentFailures[0].SrcPK)
	require.Equal(t, dst.Hex(), snap.RecentFailures[0].DstPK)
	require.NotEmpty(t, snap.TopDestinations)
	require.Equal(t, dst.Hex(), snap.TopDestinations[0].PK)
	require.NotEmpty(t, snap.TopFailedDestinations)
	require.Equal(t, dst.Hex(), snap.TopFailedDestinations[0].PK)

	// The error string carried the destination key all along, which is why
	// stripping the structured fields never delivered any privacy.
	require.True(t, strings.Contains(snap.RecentFailures[0].Error, dst.Hex()),
		"error string is expected to embed the destination key")
}
