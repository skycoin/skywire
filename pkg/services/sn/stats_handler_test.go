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

// decodeStats parses a /stats response body.
func decodeStats(t *testing.T, rec *httptest.ResponseRecorder) setupmetrics.StatsSnapshot {
	t.Helper()
	var s setupmetrics.StatsSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	return s
}

// TestStatsHandlerPublicAggregateGatedDetail is the contract: the health half
// is public, the topology half is not.
//
// Anyone on the network depends on this shared route-setup layer, so "is it
// working, how fast, failing for what reason" must be answerable without
// privilege — a setup node silently failing 100% of requests for a month is
// exactly what an ungated health surface exists to catch. But the RSN
// participates in EVERY route setup, so its record of which visors set up
// routes to which is unusually complete, and publishing that is
// traffic-analysis material.
func TestStatsHandlerPublicAggregateGatedDetail(t *testing.T) {
	c, _, dst := collectorWithFailure(t)
	allowed, _ := cipher.GenerateKeyPair()
	stranger, _ := cipher.GenerateKeyPair()

	h := statsHandler(c, []cipher.PubKey{allowed})

	// Public caller: 200, aggregate present, topology absent.
	pub := requestAs(h, stranger)
	require.Equal(t, http.StatusOK, pub.Code)
	ps := decodeStats(t, pub)
	require.Equal(t, uint64(1), ps.TotalRequests, "aggregate counters must be public")
	require.NotEmpty(t, ps.FailuresByReason, "failure reasons must be public")
	require.Empty(t, ps.RecentFailures, "per-failure detail must not be public")
	require.Empty(t, ps.TopDestinations, "destination keys must not be public")
	require.Empty(t, ps.TopFailedDestinations, "destination keys must not be public")

	// The public body must not carry the destination key ANYWHERE — including
	// inside a FailureEvent.Error string, which is how the previous
	// field-blanking approach leaked it.
	require.NotContains(t, pub.Body.String(), dst.Hex(),
		"public body must not contain a destination public key in any form")

	// Whitelisted caller: the detail is there.
	priv := requestAs(h, allowed)
	require.Equal(t, http.StatusOK, priv.Code)
	require.NotEmpty(t, decodeStats(t, priv).RecentFailures)
}

// TestStatsHandlerEmptyWhitelistServesPublicOnly — an unconfigured whitelist
// must not fail OPEN into the privileged view. Nobody is whitelisted, so
// everybody gets the public aggregate and nobody gets topology.
func TestStatsHandlerEmptyWhitelistServesPublicOnly(t *testing.T) {
	c, _, dst := collectorWithFailure(t)
	caller, _ := cipher.GenerateKeyPair()

	rec := requestAs(statsHandler(c, nil), caller)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, decodeStats(t, rec).RecentFailures)
	require.NotContains(t, rec.Body.String(), dst.Hex())
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

// The fix for the empty structured keys: a caller that is NOT on the survey
// whitelist — which is every ordinary visor, the whitelist holds seven
// deployment keys — used to get `"top_destinations": null` and
// `"recent_failures": null`, so pk / src_pk / dst_pk were empty for everyone
// who could actually have used them. It now gets its OWN rows, which tell it
// nothing it did not already know, and still nobody else's.
func TestStatsHandlerServesTheCallersOwnRows(t *testing.T) {
	c := setupmetrics.NewCollector(setupmetrics.CollectorConfig{})
	caller, _ := cipher.GenerateKeyPair()
	stranger, _ := cipher.GenerateKeyPair()
	mine, _ := cipher.GenerateKeyPair()
	theirs, _ := cipher.GenerateKeyPair()

	myErr := errors.New("failed to reserve route ids: reserve routeID from " + mine.Hex() + " failed: context deadline exceeded")
	c.RecordRouteContext(context.Background(), caller, mine, 2)(&myErr)
	theirErr := errors.New("failed to reserve route ids: reserve routeID from " + theirs.Hex() + " failed: context deadline exceeded")
	c.RecordRouteContext(context.Background(), stranger, theirs, 2)(&theirErr)

	allowed, _ := cipher.GenerateKeyPair()
	h := statsHandler(c, []cipher.PubKey{allowed})

	rec := requestAs(h, caller)
	require.Equal(t, http.StatusOK, rec.Code)
	snap := decodeStats(t, rec)

	require.Len(t, snap.RecentFailures, 1, "only the caller's own failure")
	require.Equal(t, caller.Hex(), snap.RecentFailures[0].SrcPK, "src_pk must not be empty")
	require.Equal(t, mine.Hex(), snap.RecentFailures[0].DstPK, "dst_pk must not be empty")

	require.Len(t, snap.TopDestinations, 1)
	require.Equal(t, mine.Hex(), snap.TopDestinations[0].PK, "pk must not be empty")
	require.Len(t, snap.TopFailedDestinations, 1)
	require.Equal(t, mine.Hex(), snap.TopFailedDestinations[0].PK)

	// The aggregate is still there, and the other visor's topology is not.
	require.EqualValues(t, 2, snap.TotalRequests)
	require.NotContains(t, rec.Body.String(), stranger.Hex(), "another visor's source key")
	require.NotContains(t, rec.Body.String(), theirs.Hex(), "another visor's destination key")
}

// A caller whose RemoteAddr does not parse gets the aggregate alone — never
// somebody else's rows by accident.
func TestStatsHandlerUnknownCallerGetsAggregateOnly(t *testing.T) {
	c, _, dst := collectorWithFailure(t)
	allowed, _ := cipher.GenerateKeyPair()

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	req.RemoteAddr = "not-a-public-key"
	rec := httptest.NewRecorder()
	statsHandler(c, []cipher.PubKey{allowed}).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	snap := decodeStats(t, rec)
	require.EqualValues(t, 1, snap.TotalRequests)
	require.Empty(t, snap.RecentFailures)
	require.Empty(t, snap.TopDestinations)
	require.NotContains(t, rec.Body.String(), dst.Hex())
}

// The setup-path counters are aggregate, so they are public: an operator on any
// visor can see whether clients are negotiating the batched form.
func TestStatsHandlerPublishesSetupKindCounters(t *testing.T) {
	c := setupmetrics.NewCollector(setupmetrics.CollectorConfig{})
	c.RecordSetupKind(setupmetrics.SetupKindSingle, 1)
	c.RecordSetupKind(setupmetrics.SetupKindBatch, 6)
	c.RecordBatch(6, 5, 11)
	c.RecordSetupKind(setupmetrics.SetupKindCascadeSign, 1)

	stranger, _ := cipher.GenerateKeyPair()
	allowed, _ := cipher.GenerateKeyPair()
	snap := decodeStats(t, requestAs(statsHandler(c, []cipher.PubKey{allowed}), stranger))

	require.EqualValues(t, 1, snap.RequestsByKind[setupmetrics.SetupKindSingle])
	require.EqualValues(t, 1, snap.RequestsByKind[setupmetrics.SetupKindBatch])
	require.EqualValues(t, 6, snap.RoutesByKind[setupmetrics.SetupKindBatch])
	require.EqualValues(t, 1, snap.RequestsByKind[setupmetrics.SetupKindCascadeSign])
	require.EqualValues(t, 1, snap.Batch.Batches)
	require.EqualValues(t, 5, snap.Batch.Installed)
	require.EqualValues(t, 1, snap.Batch.RoutesPerBatch[6])
	require.EqualValues(t, 11, snap.Batch.PerHopRPCsSaved)
}
