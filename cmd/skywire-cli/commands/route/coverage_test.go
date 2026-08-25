// Package route — coverage_test.go: exercises the non-RPC helpers of the
// route subcommands — the in-memory route-finder store, hop/latency
// helpers, policy preview helpers, routing-rule + route-group renderers
// (driven via the visor mock RPC client), the RSN stats formatter, and
// the trace helpers. The cobra RunE bodies that dial a live visor are
// not covered here.
package cliroute

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	routeFinder "github.com/skycoin/skywire/pkg/deployment/rf/store"
	store "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/router/policy"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

// --- calc.go: hop + latency helpers ----------------------------------

func TestProtoHopsToRouting(t *testing.T) {
	from, to := mustPK(t), mustPK(t)
	hops := []*rpcgrpc.CalcHop{{TpId: uuid.New().String(), From: from.String(), To: to.String()}}
	out, err := protoHopsToRouting(hops)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, from, out[0].From)

	// Error branches.
	_, err = protoHopsToRouting([]*rpcgrpc.CalcHop{{TpId: "not-a-uuid"}})
	require.Error(t, err)
	_, err = protoHopsToRouting([]*rpcgrpc.CalcHop{{TpId: uuid.New().String(), From: "bad"}})
	require.Error(t, err)
	_, err = protoHopsToRouting([]*rpcgrpc.CalcHop{{TpId: uuid.New().String(), From: from.String(), To: "bad"}})
	require.Error(t, err)
}

func TestIsFallbackEligible(t *testing.T) {
	require.False(t, isFallbackEligible(nil))
	for _, msg := range []string{"grpc dial failed", "connection refused", "no such host", "transport: Error x"} {
		require.True(t, isFallbackEligible(errString(msg)))
	}
	require.False(t, isFallbackEligible(errString("no route found")))
}

type errString string

func (e errString) Error() string { return string(e) }

func TestFindConfig(t *testing.T) {
	// In the test environment neither config path is expected to exist;
	// the function must return "" without error rather than panicking.
	_ = findConfig()
}

func TestReverseHops(t *testing.T) {
	a, b, c := mustPK(t), mustPK(t), mustPK(t)
	id1, id2 := uuid.New(), uuid.New()
	fwd := []routing.Hop{{TpID: id1, From: a, To: b}, {TpID: id2, From: b, To: c}}
	rev := reverseHops(fwd)
	require.Len(t, rev, 2)
	// First reverse hop mirrors the last forward hop.
	require.Equal(t, c, rev[0].From)
	require.Equal(t, b, rev[0].To)
	require.Equal(t, id2, rev[0].TpID)
}

func TestCumulativeLatencyMS(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()
	hops := []routing.Hop{{TpID: id1}, {TpID: id2}}

	sum, all := cumulativeLatencyMS(hops, map[uuid.UUID]float64{id1: 10, id2: 5})
	require.True(t, all)
	require.InDelta(t, 15.0, sum, 0.001)

	// A missing measurement folds in +Inf and flips allMeasured false.
	sum, all = cumulativeLatencyMS(hops, map[uuid.UUID]float64{id1: 10})
	require.False(t, all)
	require.True(t, math.IsInf(sum, 1))
}

func TestMemoryStore(t *testing.T) {
	a, b, c := mustPK(t), mustPK(t), mustPK(t)
	entries := []*transport.Entry{
		{ID: uuid.New(), Edges: [2]cipher.PubKey{a, b}, Type: tptypes.STCPR},
		{ID: uuid.New(), Edges: [2]cipher.PubKey{b, c}, Type: tptypes.SUDPH},
		nil, // skipped
	}
	s := newMemoryStoreFromEntries(entries)
	ctx := context.Background()

	tps, err := s.GetTransportsByEdge(ctx, b)
	require.NoError(t, err)
	require.Len(t, tps, 2)

	tps, err = s.GetTransportsByEdgeNoLatency(ctx, a)
	require.NoError(t, err)
	require.Len(t, tps, 1)

	_, err = s.GetTransportsByEdge(ctx, mustPK(t))
	require.Error(t, err) // ErrTransportNotFound

	all, err := s.GetAllTransports(ctx, true)
	require.NoError(t, err)
	require.Len(t, all, 3)

	// Exercise the inert store.Store stubs (must not panic).
	id := uuid.New()
	require.NoError(t, s.RegisterTransport(ctx, a, nil))
	require.NoError(t, s.RegisterTransportsBatch(ctx, a, nil))
	require.NoError(t, s.DeregisterTransport(ctx, id))
	_, _ = s.GetTransportByID(ctx, id)  //nolint
	_, _ = s.GetNumberOfTransports(ctx) //nolint
	require.NoError(t, s.UpdateBandwidth(ctx, "tp", a, 1, 2))
	require.NoError(t, s.UpdateLatency(ctx, "tp", 1, 2, 3))
	_, _ = s.GetTransportBandwidth(ctx, id, "h", 1) //nolint
	_, _ = s.GetVisorBandwidth(ctx, a, "h", 1)      //nolint
	_, _ = s.GetAllVisorSummaries(ctx, true, true)  //nolint
	require.NoError(t, s.RecordHeartbeat(ctx, a, "h"))
	_ = s.GetDailyTimeline(ctx, "h", time.Now()) //nolint
	require.NoError(t, s.RecordTransportHeartbeat(ctx, id, "h", time.Now()))
	require.NoError(t, s.IngestTransportTimeline(ctx, id, "h", nil))
	_, _ = s.GetTransportUptimeSummaries(ctx, nil, true, true) //nolint
	_, _ = s.GetTransportUptimeByVisor(ctx, a, true, true)     //nolint
	_ = s.GetTransportDailyTimeline(ctx, "h", time.Now())      //nolint
	require.NoError(t, s.BackupAndCleanOldBandwidth(ctx, "h"))
	_, _ = s.GetNetworkMetrics(ctx, store0())                //nolint
	_, _ = s.GetVisorAggregateMetrics(ctx, nil, store0())    //nolint
	_, _ = s.GetAllTransportMetrics(ctx, store0())           //nolint
	_, _ = s.GetTransportMetricsByIDs(ctx, nil, store0())    //nolint
	_, _ = s.GetTransportMetricsByVisors(ctx, nil, store0()) //nolint
	s.Close()
}

// --- policy.go helpers -----------------------------------------------

func TestDialJSONToContext(t *testing.T) {
	// Explicit RFC3339 time.
	d := dialJSON{App: "vpn", PeerPK: "pk", Port: 3, Now: "2026-05-29T10:00:00Z"}
	rctx, err := d.toContext()
	require.NoError(t, err)
	require.Equal(t, "vpn", rctx.App)
	require.Equal(t, 2026, rctx.Now.Year())

	// Friday convenience.
	rctx, err = dialJSON{Friday: true, Hour: 9}.toContext()
	require.NoError(t, err)
	require.Equal(t, time.Friday, rctx.Now.Weekday())

	// Default (now).
	_, err = dialJSON{}.toContext()
	require.NoError(t, err)

	// Invalid time → error.
	_, err = dialJSON{Now: "not-a-time"}.toContext()
	require.Error(t, err)
}

func TestFixedClockAndSpecJSON(t *testing.T) {
	now := time.Now()
	require.Equal(t, now, fixedClock{t: now}.Now())
	js := specToJSON(policy.RouteSpec{Mux: 2, MinHops: 1, Fallback: "drop"})
	require.Equal(t, 2, js.Mux)
	require.Equal(t, "drop", js.Fallback)
}

func TestPercentile(t *testing.T) {
	require.Zero(t, percentile(nil, 50))
	samples := []time.Duration{5, 1, 3, 2, 4}
	require.Equal(t, time.Duration(3), percentile(samples, 50))
	require.Equal(t, time.Duration(5), percentile(samples, 100)) // clamps to last
}

// --- route.go: rules + render via mock RPC ---------------------------

func newMock(t *testing.T) visor.API {
	t.Helper()
	_, rc, err := visor.NewMockRPCClient(rand.New(rand.NewSource(1)), 5, 5) //nolint:gosec
	require.NoError(t, err)
	return rc
}

func TestGetNextAvailableRouteID(t *testing.T) {
	rc := newMock(t)
	rules, err := rc.RoutingRules()
	require.NoError(t, err)
	next := getNextAvailableRouteID(rules...)
	require.Positive(t, uint32(next))
}

func TestPrintRoutingRules(t *testing.T) {
	rc := newMock(t)
	rules, err := rc.RoutingRules()
	require.NoError(t, err)

	// printRoutingRules writes to stdout via PrintOutput; redirect it.
	restore := muteStdout(t)
	defer restore()

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	printRoutingRules(fs, rules...)
}

func TestRenderRoutingRulesLive(t *testing.T) {
	out, err := renderRoutingRulesLive(newMock(t))
	require.NoError(t, err)
	require.Contains(t, out, "rule(s)")
}

// rgStub embeds the mock visor.API but overrides RouteGroups so the
// renderer gets controlled data — the mock's own RouteGroups() panics
// ("invalid rule: Consume"), so it can't be used directly here.
type rgStub struct {
	visor.API
	rgs []visor.RouteGroupInfo
	err error
}

func (s rgStub) RouteGroups() ([]visor.RouteGroupInfo, error) { return s.rgs, s.err }

func TestRenderRouteGroupsLive(t *testing.T) {
	a, b := mustPK(t), mustPK(t)
	rgs := []visor.RouteGroupInfo{
		{
			Initiator: true, FwdNextTpID: "tp1", FwdRuleID: 2, ConsumeRuleID: 3,
			Desc: routing.RouteDescriptorFields{DstPK: a, SrcPK: b, DstPort: 80, SrcPort: 81},
			Hops: []visor.RouteHopInfo{{TpID: uuid.New().String(), From: a.Hex(), To: b.Hex(), TpType: "stcpr"}},
		},
		{Initiator: false}, // responder, empty FwdNextTpID → "-", no hops
	}
	stub := rgStub{API: newMock(t), rgs: rgs}

	origFilter, origHops := groupsFilter, groupsHops
	defer func() { groupsFilter, groupsHops = origFilter, origHops }()
	groupsHops = true // exercise the per-hop sub-rendering + "(not recorded)" branch

	for _, f := range []string{"all", "initiator", "responder", ""} {
		groupsFilter = f
		out, err := renderRouteGroupsLive(stub)
		require.NoError(t, err)
		require.Contains(t, out, "group(s)")
	}

	// Invalid filter → error.
	groupsFilter = "bogus"
	_, err := renderRouteGroupsLive(stub)
	require.Error(t, err)

	// Empty groups → friendly message.
	groupsFilter = "all"
	out, err := renderRouteGroupsLive(rgStub{API: newMock(t)})
	require.NoError(t, err)
	require.Contains(t, out, "No active route groups")

	// RPC error path.
	_, err = renderRouteGroupsLive(rgStub{API: newMock(t), err: errString("rpc down")})
	require.Error(t, err)
}

// --- rsn_stats.go ----------------------------------------------------

func TestCircuitOrDash(t *testing.T) {
	require.Equal(t, "-", circuitOrDash(""))
	require.Equal(t, "-", circuitOrDash("closed"))
	require.Equal(t, "open", circuitOrDash("open"))
}

func TestFormatRSNStats(t *testing.T) {
	require.Equal(t, "no stats returned\n", formatRSNStats(nil))

	now := time.Now()
	snap := &setupmetrics.StatsSnapshot{
		StartedAt: now, UptimeSec: 120, TotalRequests: 10, Successful: 8, Failed: 2,
		ConcurrencyDrops: 1, ActiveRequests: 1, SuccessRatePct: 80,
		LastSuccessAt: &now, LastFailureAt: &now,
		LatencyMs:             setupmetrics.LatencyStats{Count: 8, Min: 1, Max: 9, Mean: 4, P50: 3, P95: 8, P99: 9},
		FailuresByReason:      map[setupmetrics.FailureReason]uint64{"timeout": 2, "no_route": 1},
		RouteLengthHist:       map[int]uint64{1: 3, 2: 5},
		TopDestinations:       []setupmetrics.DestStat{{PK: "pk1", Total: 5, Failed: 1, Circuit: "open"}},
		TopFailedDestinations: []setupmetrics.DestStat{{PK: "pk2", Total: 2, Failed: 2, Circuit: "closed"}},
		RecentFailures: []setupmetrics.FailureEvent{
			{Timestamp: now, Reason: "timeout", DurationMs: 50, SrcPK: "s", DstPK: "d", HopCount: 2, Error: "boom"},
		},
	}
	out := formatRSNStats(snap)
	require.Contains(t, out, "Total requests:   10")
	require.Contains(t, out, "Failures by reason")
	require.Contains(t, out, "route length distribution")
	require.Contains(t, out, "Top destinations")
	require.Contains(t, out, "Recent failures")

	// Empty-latency branch.
	require.Contains(t, formatRSNStats(&setupmetrics.StatsSnapshot{StartedAt: now}), "no successful setups")
}

// --- trace.go --------------------------------------------------------

func TestShortPK(t *testing.T) {
	pk := mustPK(t)
	short := shortPK(pk)
	require.Contains(t, short, "…")
	require.Less(t, len(short), len(pk.String()))
}

func TestBestOfDmsgPings(t *testing.T) {
	// The mock's DmsgPingOnce returns (0, nil), so no sample beats zero
	// and the function reports "no successful samples".
	_, err := bestOfDmsgPings(newMock(t), mustPK(t), 3)
	require.Error(t, err)
}

// --- calc.go: --source validation + TPS-sourced transport graph ------

func TestValidCalcSource(t *testing.T) {
	for _, s := range []string{"tpd", "tps", "auto", "dht", "TPS", "Tpd"} {
		require.Truef(t, validCalcSource(s), "expected %q to be valid", s)
	}
	for _, s := range []string{"", "bogus", "http", "oracle"} {
		require.Falsef(t, validCalcSource(s), "expected %q to be invalid", s)
	}
}

// tpsStub embeds the mock visor.API and overrides only the three
// methods fetchTPSTransports consults: Overview (to learn the local
// PK), Transports (the local visor's own transports) and
// TPSGetTransports (a REMOTE visor's own transports, served via the
// setup node). Everything else defers to the mock.
type tpsStub struct {
	visor.API
	localPK cipher.PubKey
	local   []*visor.TransportSummary
	tps     map[cipher.PubKey][]visor.TPSTransportResponse
}

func (s tpsStub) Overview() (*visor.Overview, error) {
	return &visor.Overview{PubKey: s.localPK}, nil
}
func (s tpsStub) Transports(_ []string, _ []cipher.PubKey, _ bool) ([]*visor.TransportSummary, error) {
	return s.local, nil
}
func (s tpsStub) TPSGetTransports(pk cipher.PubKey) ([]visor.TPSTransportResponse, error) {
	if v, ok := s.tps[pk]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("no TPS entry for %s", pk)
}

// TestFetchTPSTransports proves the authoritative graph is assembled
// from the src visor's OWN transports (read locally) unioned with the
// dst visor's OWN transports (fetched via the setup node), that a
// setup-labeled local transport is dropped, and that the resulting
// entries share the intermediate — so a single-intermediate
// src->inter->dst route is computable without ever touching TPD.
func TestFetchTPSTransports(t *testing.T) {
	src := mustPK(t) // local visor
	inter := mustPK(t)
	dst := mustPK(t)

	stub := tpsStub{
		API:     newMock(t),
		localPK: src,
		local: []*visor.TransportSummary{
			{ID: uuid.New(), Local: src, Remote: inter, Type: tptypes.STCPR},
			{ID: uuid.New(), Local: src, Remote: inter, Type: tptypes.STCPR, IsSetup: true}, // must be dropped
		},
		tps: map[cipher.PubKey][]visor.TPSTransportResponse{
			dst: {{ID: uuid.New(), Local: dst, Remote: inter, Type: string(tptypes.STCPR)}},
		},
	}

	entries, err := fetchTPSTransports(stub, src, dst)
	require.NoError(t, err)
	// One src-side edge (setup tp dropped) + one dst-side edge.
	require.Len(t, entries, 2)

	// The assembled entries must yield a single-intermediate route.
	memStore := newMemoryStoreFromEntries(entries)
	graph, err := routeFinder.NewGraphWithDepth(context.Background(), memStore, src, 3)
	require.NoError(t, err)
	routes, err := graph.GetRoute(context.Background(), src, dst, 2, 3, 1)
	require.NoError(t, err)
	require.NotEmpty(t, routes)
	require.Equal(t, inter, routes[0].Hops[0].To) // src -> inter -> dst
}

func TestFetchTPSTransportsNilClient(t *testing.T) {
	_, err := fetchTPSTransports(nil, mustPK(t), mustPK(t))
	require.Error(t, err)
}

// --- helpers ---------------------------------------------------------

func store0() store.MetricsQuery { return store.MetricsQuery{} }

func muteStdout(t *testing.T) func() {
	t.Helper()
	orig := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	os.Stdout = devnull
	return func() { os.Stdout = orig; _ = devnull.Close() } //nolint
}
