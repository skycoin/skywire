//go:build !tinygo || (js && wasm)

package router

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// rankCandTailMs is the latency every hop AFTER the first carries in these
// fixtures, so a candidate is measured end to end and the first hop is what
// varies. Tests that need an unknown remainder build the path by hand.
const rankCandTailMs = 10

// rankCandPath builds a one-intermediate forward candidate leaving over tpID,
// with every later hop measured at rankCandTailMs.
func rankCandPath(tpID uuid.UUID, hops int) []routing.Hop {
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	out := []routing.Hop{{TpID: tpID, From: src, To: mid}}
	for i := 1; i < hops; i++ {
		out = append(out, routing.Hop{TpID: uuid.New(), From: mid, To: dst, Latency: rankCandTailMs})
	}
	return out
}

func firstTpIDs(paths [][]routing.Hop) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(paths))
	for _, p := range paths {
		out = append(out, p[0].TpID)
	}
	return out
}

// TestRankByPathLatency_OrdersUnusedFirstHops is the unit proof of the
// campaign's criterion: of the routes whose first hop no sibling tunnel holds,
// the extra tunnel must take the FASTEST, not merely the first the route-finder
// happened to return. The live rig's numbers are used verbatim — Sydney 470ms
// was what an unranked diversify dial took while Atlanta 39ms sat free.
func TestRankByPathLatency_OrdersUnusedFirstHops(t *testing.T) {
	sydney, atlanta, amsterdam, unmeasured := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	lat := map[uuid.UUID]float64{sydney: 470, atlanta: 39, amsterdam: 148}
	latencyFor := func(id uuid.UUID) float64 { return lat[id] }

	// Input order is the route-finder's, deliberately worst-first.
	cands := [][]routing.Hop{
		rankCandPath(sydney, 2),
		rankCandPath(atlanta, 2),
		rankCandPath(amsterdam, 2),
		rankCandPath(unmeasured, 2),
	}

	ranked := rankByPathLatency(cands, latencyFor)
	require.Equal(t,
		[]uuid.UUID{atlanta, amsterdam, sydney, unmeasured},
		firstTpIDs(ranked),
		"candidates must rank by measured first-hop latency, unmeasured last")

	// The decision trail must name the ranking so a bench run can read it back
	// out of the dial_decision mux event.
	trail := pathLatencyTrail(ranked, latencyFor, nil)
	require.Equal(t,
		atlanta.String()[:8]+"=39+10ms, "+amsterdam.String()[:8]+"=148+10ms, "+
			sydney.String()[:8]+"=470+10ms, "+unmeasured.String()[:8]+"=unmeasured",
		trail)
}

// TestRankByPathLatency_UsedFirstHopExcluded proves the ranking composes
// with (and never undoes) the sibling exclusion: filterDisjointFirstHop drops
// the first hop an existing group already holds, and the survivors are what
// gets ranked — a used hop can never be chosen however fast it is.
func TestRankByPathLatency_UsedFirstHopExcluded(t *testing.T) {
	used, atlanta, sydney := uuid.New(), uuid.New(), uuid.New()
	lat := map[uuid.UUID]float64{used: 1, atlanta: 39, sydney: 470}
	latencyFor := func(id uuid.UUID) float64 { return lat[id] }

	cands := [][]routing.Hop{rankCandPath(used, 2), rankCandPath(sydney, 2), rankCandPath(atlanta, 2)}

	disjoint := filterDisjointFirstHop(cands, []uuid.UUID{used})
	require.Len(t, disjoint, 2)

	ranked := rankByPathLatency(disjoint, latencyFor)
	require.Equal(t, []uuid.UUID{atlanta, sydney}, firstTpIDs(ranked))
	require.NotContains(t, firstTpIDs(ranked), used, "an already-used first hop is never a candidate")
}

// TestRankByPathLatency_TieBreaks: equal (or absent) latency falls back to
// fewer hops, then to the route-finder's own order — the ranking must never
// shuffle candidates it has no evidence about.
func TestRankByPathLatency_TieBreaks(t *testing.T) {
	long, short, a, b := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	latencyFor := func(id uuid.UUID) float64 {
		if id == long || id == short {
			return 100
		}
		return 0
	}

	ranked := rankByPathLatency([][]routing.Hop{
		rankCandPath(long, 3),
		rankCandPath(short, 2),
		rankCandPath(a, 2),
		rankCandPath(b, 2),
	}, latencyFor)

	require.Equal(t, []uuid.UUID{short, long, a, b}, firstTpIDs(ranked),
		"equal latency prefers fewer hops; unmeasured keeps the input order, last")
}

// TestFirstHopLatencyMs_FallsBackToRouteFinderHop: when this visor has no
// sample of its own for the first hop, the per-hop Latency the route-finder
// attached to the candidate still ranks it. 0 means unmeasured everywhere.
func TestFirstHopLatencyMs_FallsBackToRouteFinderHop(t *testing.T) {
	id := uuid.New()
	path := rankCandPath(id, 2)
	path[0].Latency = 148

	require.Equal(t, float64(148), firstHopLatencyMs(path, nil))
	require.Equal(t, float64(148), firstHopLatencyMs(path, func(uuid.UUID) float64 { return 0 }))
	require.Equal(t, float64(39), firstHopLatencyMs(path, func(uuid.UUID) float64 { return 39 }),
		"a local measurement outranks the route-finder's")
	require.Equal(t, float64(0), firstHopLatencyMs(rankCandPath(id, 2), nil))
	require.Equal(t, float64(0), firstHopLatencyMs(nil, nil))
}

// TestNotePathRanking_RecordsTheTrail: the chosen route and the ranking
// that produced it land on the dial's decision trail (MuxEventDialDecision), so
// a bench run can attribute a tunnel's throughput to the hop it took.
func TestNotePathRanking_RecordsTheTrail(t *testing.T) {
	fast, slow := uuid.New(), uuid.New()
	lat := map[uuid.UUID]float64{fast: 39, slow: 470}
	latencyFor := func(id uuid.UUID) float64 { return lat[id] }

	opts := &DialOptions{}
	ranked := notePathRanking(opts, [][]routing.Hop{rankCandPath(slow, 2), rankCandPath(fast, 2)}, latencyFor, nil)

	require.Equal(t, fast, ranked[0][0].TpID)
	require.Len(t, opts.dialNotes, 1)
	require.Equal(t,
		"ranked by path latency: "+fast.String()[:8]+"=39+10ms, "+slow.String()[:8]+"=470+10ms; chose "+fast.String()[:8],
		opts.dialNotes[0])

	// Nil-safe: a dial with no options still ranks.
	require.Equal(t, fast, notePathRanking(nil, [][]routing.Hop{rankCandPath(slow, 2), rankCandPath(fast, 2)}, latencyFor, nil)[0][0].TpID)
}

// mkLocalLat builds one of the source's own transports to peer with a measured
// first-hop latency (ms) — the same field ManagedTransport.GetLatency() and a
// leg snapshot's latency_ms report.
func mkLocalLat(src, peer cipher.PubKey, tpType tptypes.Type, latencyMs float64) oracleLocalTp {
	tp := mkLocal(src, peer, tpType)
	tp.latencyMs = latencyMs
	return tp
}

// TestComputeDisjoint2HopRoutes_RanksByPathLatency is the oracle-side proof.
// The RSN oracle — not the route-finder — is the candidate selection that wins a
// diversify dial on a fleet where every first hop is stcpr: its old ranking tied
// on transport-type preference and broke the tie on intermediate-PK STRING, an
// arbitrary order that put a 470ms first hop ahead of a 39ms one. Measured
// first-hop latency is now the primary key, unmeasured last.
func TestComputeDisjoint2HopRoutes_RanksByPathLatency(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	sydney, _ := cipher.GenerateKeyPair()
	amsterdam, _ := cipher.GenerateKeyPair()
	atlanta, _ := cipher.GenerateKeyPair()
	unknown, _ := cipher.GenerateKeyPair()

	localTps := []oracleLocalTp{
		mkLocalLat(src, sydney, tptypes.STCPR, 470),
		mkLocalLat(src, amsterdam, tptypes.STCPR, 148),
		mkLocalLat(src, atlanta, tptypes.STCPR, 39),
		mkLocalLat(src, unknown, tptypes.STCPR, 0), // never sampled
	}
	dstEntries := []*transport.Entry{
		mkDstEntryLat(dst, sydney, tptypes.STCPR, rankCandTailMs),
		mkDstEntryLat(dst, amsterdam, tptypes.STCPR, rankCandTailMs),
		mkDstEntryLat(dst, atlanta, tptypes.STCPR, rankCandTailMs),
		mkDstEntryLat(dst, unknown, tptypes.STCPR, rankCandTailMs),
	}

	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	require.NoError(t, err)
	require.Len(t, legs, 4)

	got := make([]cipher.PubKey, 0, len(legs))
	for _, l := range legs {
		got = append(got, l.Intermediate)
	}
	require.Equal(t, []cipher.PubKey{atlanta, amsterdam, sydney, unknown}, got,
		"the oracle must return legs best-first by measured first-hop latency")

	// The leg's first hop carries the same latency the ranking used, so the
	// caller (and any leg snapshot built from it) reads one number.
	require.Equal(t, float64(39), legs[0].Forward[0].Latency)

	// Capped to one leg, the oracle's single answer is the BEST one — this is
	// what oracle2HopRoutes returns to a diversify dial.
	best, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 1)
	require.NoError(t, err)
	require.Len(t, best, 1)
	require.Equal(t, atlanta, best[0].Intermediate)
}

// TestComputeDisjoint2HopRoutes_ExcludedFirstHopThenRanked: the sibling
// exclusion still removes a used first hop, and the ranking orders what is left
// — the oracle honors both, in that order.
func TestComputeDisjoint2HopRoutes_ExcludedFirstHopThenRanked(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	fastButUsed, _ := cipher.GenerateKeyPair()
	sydney, _ := cipher.GenerateKeyPair()
	atlanta, _ := cipher.GenerateKeyPair()

	used := mkLocalLat(src, fastButUsed, tptypes.STCPR, 5)
	localTps := []oracleLocalTp{
		used,
		mkLocalLat(src, sydney, tptypes.STCPR, 470),
		mkLocalLat(src, atlanta, tptypes.STCPR, 39),
	}
	dstEntries := []*transport.Entry{
		mkDstEntryLat(dst, fastButUsed, tptypes.STCPR, rankCandTailMs),
		mkDstEntryLat(dst, sydney, tptypes.STCPR, rankCandTailMs),
		mkDstEntryLat(dst, atlanta, tptypes.STCPR, rankCandTailMs),
	}

	opts := &DialOptions{DiversifyTransports: true, ExcludeTransportIDs: []uuid.UUID{used.id}}
	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, opts, 0)
	require.NoError(t, err)
	require.Len(t, legs, 2, "the sibling's first-hop transport is excluded however fast it is")
	require.Equal(t, atlanta, legs[0].Intermediate)
	require.Equal(t, sydney, legs[1].Intermediate)
}

// TestComputeDisjoint2HopRoutes_TypePreferenceStaysTheTiebreak: with no latency
// signal at all the oracle keeps its old order — transport-type preference, then
// intermediate PK. The ranking only decides what latency can decide.
func TestComputeDisjoint2HopRoutes_TypePreferenceStaysTheTiebreak(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	slowType, _ := cipher.GenerateKeyPair()
	fastType, _ := cipher.GenerateKeyPair()

	localTps := []oracleLocalTp{
		mkLocalLat(src, slowType, tptypes.SUDPH, 0),
		mkLocalLat(src, fastType, tptypes.STCPR, 0),
	}
	dstEntries := []*transport.Entry{
		mkDstEntry(dst, slowType, tptypes.STCPR),
		mkDstEntry(dst, fastType, tptypes.STCPR),
	}

	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	require.NoError(t, err)
	require.Equal(t, fastType, legs[0].Intermediate,
		"unmeasured everywhere: stcpr still outranks sudph")
}

// directCand builds a 1-hop direct candidate to dst over tpID with a measured
// latency. All of a host's direct transports (stcpr/squicr/sudph) share the
// same remote peer, which is what makes them twins.
func directCand(src, dst cipher.PubKey, tpID uuid.UUID, latencyMs float64) []routing.Hop {
	return []routing.Hop{{TpID: tpID, From: src, To: dst, Latency: latencyMs}}
}

// viaCand builds a 2-hop candidate src -> mid -> dst whose first hop is tpID.
func viaCand(src, mid, dst cipher.PubKey, tpID uuid.UUID, latencyMs float64) []routing.Hop {
	return []routing.Hop{
		{TpID: tpID, From: src, To: mid, Latency: latencyMs},
		{TpID: uuid.New(), From: mid, To: dst, Latency: rankCandTailMs},
	}
}

// TestFreeFirstHops_DirectTwinsExcludedByPeer is the fix for the 2026-09-17
// smoke: tunnel 1 was the DIRECT route to the exit over the stcpr transport, and
// the diversify dial — excluding only that transport ID — took the squicr twin
// to the same host (same NIC, same path), so the pair split one link (50 MB up
// 2.57 MB/s, down 5.64 on a 29/21 split). Excluding the first-hop PEER takes
// every direct transport to the exit out of the running, leaving the
// intermediates, and the ranking then picks the fastest of those.
func TestFreeFirstHops_DirectTwinsExcludedByPeer(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	exit, _ := cipher.GenerateKeyPair()
	atlanta, _ := cipher.GenerateKeyPair()
	amsterdam, _ := cipher.GenerateKeyPair()

	stcpr, squicr, sudph := uuid.New(), uuid.New(), uuid.New()
	viaA, viaB := uuid.New(), uuid.New()

	cands := [][]routing.Hop{
		directCand(src, exit, stcpr, 137),  // tunnel 1's first hop
		directCand(src, exit, squicr, 138), // twin: same peer, same host
		directCand(src, exit, sudph, 139),  // twin
		viaCand(src, atlanta, exit, viaA, 39),
		viaCand(src, amsterdam, exit, viaB, 148),
	}

	r := &router{}
	// Tunnel 1 is the direct route: its transport AND its peer (the exit) are
	// what a sibling dial must avoid.
	opts := &DialOptions{
		DiversifyTransports:  true,
		ExcludeTransportIDs:  []uuid.UUID{stcpr},
		ExcludeFirstHopPeers: []cipher.PubKey{exit},
	}

	free := r.freeFirstHops(cands, opts)
	require.Len(t, free, 2, "every direct transport to the exit shares tunnel 1's first hop")
	chosen := notePathRanking(opts, free, nil, nil)[0]
	require.Equal(t, viaA, chosen[0].TpID, "the sibling must take the fastest intermediate, not a direct twin")
	require.Equal(t, atlanta, chosen[0].To)

	// Regression guard: the transport-ID exclusion ALONE — the old behavior —
	// left both twins in the running, which is how the live dial came to pick
	// one of them (the hook race won with a direct route before the
	// intermediates were ever candidates).
	idOnly := filterDisjointFirstHop(cands, opts.ExcludeTransportIDs)
	require.Len(t, idOnly, 4)
	require.Equal(t, squicr, rankByPathLatency(
		[][]routing.Hop{directCand(src, exit, squicr, 138), directCand(src, exit, sudph, 139)}, nil)[0][0].TpID,
		"with only the twins to choose from, the old filter had nothing better to offer")
}

// TestFreeFirstHops_SiblingViaIntermediate: when tunnel 1 already goes via A,
// the sibling takes the next-best remaining candidate — here intermediate B.
// Note what this does NOT assert: the direct route is not ruled out as a
// sibling of a via-A tunnel (their first hops are genuinely different links);
// it simply loses on measured latency. Only a sibling that already LEAVES over
// the exit's own peer takes every direct transport out of the running, which is
// TestFreeFirstHops_DirectTwinsExcludedByPeer.
func TestFreeFirstHops_SiblingViaIntermediate(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	exit, _ := cipher.GenerateKeyPair()
	atlanta, _ := cipher.GenerateKeyPair()
	amsterdam, _ := cipher.GenerateKeyPair()

	direct, viaA, viaB := uuid.New(), uuid.New(), uuid.New()
	cands := [][]routing.Hop{
		directCand(src, exit, direct, 260),
		viaCand(src, atlanta, exit, viaA, 39),
		viaCand(src, amsterdam, exit, viaB, 148),
	}

	r := &router{}
	opts := &DialOptions{
		DiversifyTransports:  true,
		ExcludeTransportIDs:  []uuid.UUID{viaA},
		ExcludeFirstHopPeers: []cipher.PubKey{atlanta},
	}
	free := r.freeFirstHops(cands, opts)
	require.Equal(t, viaB, notePathRanking(opts, free, nil, nil)[0][0].TpID,
		"a sibling of a via-A tunnel takes the next-best intermediate")

	// The direct route is not forbidden in itself — with no tunnel on the exit's
	// own peer it is a legitimate (and here the only) candidate.
	onlyDirect := r.freeFirstHops([][]routing.Hop{directCand(src, exit, direct, 260)}, opts)
	require.Len(t, onlyDirect, 1, "the direct route stays available when nothing leaves over it")
}

// TestFreeFirstHops_HookRaceDeclinesDirectTwin: the hook race's direct route is
// admitted only through the same test. With a direct tunnel already up, every
// direct candidate is excluded, so the race concedes and waits for the ranked
// oracle / finder candidates instead of winning instantly with a twin.
func TestFreeFirstHops_HookRaceDeclinesDirectTwin(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	exit, _ := cipher.GenerateKeyPair()
	stcpr, squicr, sudph := uuid.New(), uuid.New(), uuid.New()

	r := &router{}
	opts := &DialOptions{
		DiversifyTransports:  true,
		ExcludeTransportIDs:  []uuid.UUID{stcpr},
		ExcludeFirstHopPeers: []cipher.PubKey{exit},
	}

	// What directRoutes hands the hook race: every live direct transport to the
	// exit, ranked. The type-preference winner is the excluded one.
	directs := [][]routing.Hop{
		directCand(src, exit, stcpr, 137),
		directCand(src, exit, squicr, 138),
		directCand(src, exit, sudph, 139),
	}
	require.True(t, r.firstHopExcluded(directs[0], opts), "the sibling's own transport is taken")
	require.Empty(t, r.freeFirstHops(directs, opts),
		"no direct transport to the exit is a free first hop once a direct tunnel exists")

	// Without a sibling on that peer the race may still win with a direct route.
	lone := &DialOptions{DiversifyTransports: true}
	require.Len(t, r.freeFirstHops(directs, lone), 3)
}

// TestFreeFirstHops_ExcludedByRemoteIP: the same host reached under a second PK
// is caught by the remote-IP list. Without a transport manager the IP of a
// candidate cannot be resolved, so the PK list is what applies here; this pins
// that an empty exclusion set never filters anything.
func TestFreeFirstHops_ExcludedByRemoteIP(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	exit, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()

	r := &router{}
	cands := [][]routing.Hop{viaCand(src, mid, exit, uuid.New(), 39)}
	require.Len(t, r.freeFirstHops(cands, &DialOptions{}), 1, "no exclusions, no filtering")
	require.Len(t, r.freeFirstHops(cands, nil), 1)
	require.Empty(t, r.filterDisjointFirstHopPeer(cands, []cipher.PubKey{mid}, nil))
	require.Len(t, r.filterDisjointFirstHopPeer(cands, nil, []string{"1.2.3.4"}), 1,
		"an IP exclusion cannot judge a hop whose transport this visor does not hold")
}

// fakeProber answers a probe after delay with latencyMs, or fails when
// latencyMs is 0 — a stand-in for ManagedTransport.ProbeLatency (one ping, wait
// for the pong) with no link behind it.
type fakeProber struct {
	latencyMs float64
	delay     time.Duration
	calls     atomic.Int64
}

func (f *fakeProber) ProbeLatency(ctx context.Context) (float64, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.latencyMs <= 0 {
		return 0, errors.New("no measurement")
	}
	return f.latencyMs, nil
}

// TestProbeFirstHopLatencies_RanksIdleTransports is the fix for the 2026-09-17
// smoke: every candidate intermediate was IDLE, so the periodic transport ping
// had never sampled it, the TPD and route-finder fallbacks were empty too, and
// the trail read "50cdb857=unmeasured, f1012467=unmeasured, …" — the tie fell
// back to the finder's order and picked the 470ms hop again. Probing the
// unmeasured first hops on demand turns that into a real ranking.
func TestProbeFirstHopLatencies_RanksIdleTransports(t *testing.T) {
	atlanta, amsterdam, sydney, dead := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	provers := map[uuid.UUID]*fakeProber{
		sydney:    {latencyMs: 470},
		amsterdam: {latencyMs: 148},
		atlanta:   {latencyMs: 39},
		dead:      {latencyMs: 0}, // never answers: stays unmeasured
	}
	probe := func(id uuid.UUID) latencyProber {
		p, ok := provers[id]
		if !ok {
			return nil
		}
		return p
	}

	// Worst-first input, and NOTHING is measured up front.
	cands := [][]routing.Hop{
		rankCandPath(sydney, 2),
		rankCandPath(dead, 2),
		rankCandPath(amsterdam, 2),
		rankCandPath(atlanta, 2),
	}

	probed := probeFirstHopLatencies(context.Background(), cands, nil, probe, firstHopProbeTimeout)
	require.Equal(t, map[uuid.UUID]float64{atlanta: 39, amsterdam: 148, sydney: 470}, probed)

	opts := &DialOptions{}
	latencyFor := mergeProbedLatency(nil, probed)
	ranked := notePathRanking(opts, cands, latencyFor, probed)
	require.Equal(t, []uuid.UUID{atlanta, amsterdam, sydney, dead}, firstTpIDs(ranked),
		"probed values rank the candidates; the one that never answered sorts last")

	// The trail says which numbers the probe supplied.
	require.Equal(t,
		"ranked by path latency: "+atlanta.String()[:8]+"=39p+10ms, "+
			amsterdam.String()[:8]+"=148p+10ms, "+sydney.String()[:8]+"=470p+10ms, "+
			dead.String()[:8]+"=unmeasured; chose "+atlanta.String()[:8],
		opts.dialNotes[0])
}

// TestProbeFirstHopLatencies_SkipsMeasuredAndUnheldHops: a first hop that
// already has a sample costs no round trip, and a hop this visor does not hold
// (probe returns nil) is never probed at all.
func TestProbeFirstHopLatencies_SkipsMeasuredAndUnheldHops(t *testing.T) {
	measured, idle, remote := uuid.New(), uuid.New(), uuid.New()
	measuredProber := &fakeProber{latencyMs: 1}
	idleProber := &fakeProber{latencyMs: 39}
	probe := func(id uuid.UUID) latencyProber {
		switch id {
		case measured:
			return measuredProber
		case idle:
			return idleProber
		}
		return nil // not held locally
	}
	known := func(id uuid.UUID) float64 {
		if id == measured {
			return 137
		}
		return 0
	}

	cands := [][]routing.Hop{rankCandPath(measured, 2), rankCandPath(idle, 2), rankCandPath(remote, 2)}
	probed := probeFirstHopLatencies(context.Background(), cands, known, probe, firstHopProbeTimeout)

	require.Equal(t, map[uuid.UUID]float64{idle: 39}, probed)
	require.Zero(t, measuredProber.calls.Load(), "a measured first hop is not re-probed")
	require.Equal(t, int64(1), idleProber.calls.Load())

	// The merged lookup keeps the known value and adds the probed one.
	lat := mergeProbedLatency(known, probed)
	require.Equal(t, float64(137), lat(measured))
	require.Equal(t, float64(39), lat(idle))
	require.Equal(t, float64(0), lat(remote))
}

// TestProbeFirstHopLatencies_BoundedByTimeout: a probe that does not answer
// inside the cap never delays the dial past it, and its candidate stays
// unmeasured (sorting last) rather than failing the dial.
func TestProbeFirstHopLatencies_BoundedByTimeout(t *testing.T) {
	slow, quick := uuid.New(), uuid.New()
	probe := func(id uuid.UUID) latencyProber {
		if id == slow {
			return &fakeProber{latencyMs: 39, delay: time.Minute}
		}
		return &fakeProber{latencyMs: 148}
	}
	cands := [][]routing.Hop{rankCandPath(slow, 2), rankCandPath(quick, 2)}

	start := time.Now()
	probed := probeFirstHopLatencies(context.Background(), cands, nil, probe, 100*time.Millisecond)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 5*time.Second, "the probe is bounded by its cap, not by its slowest peer")
	require.Equal(t, map[uuid.UUID]float64{quick: 148}, probed)
	require.Equal(t, []uuid.UUID{quick, slow},
		firstTpIDs(rankByPathLatency(cands, mergeProbedLatency(nil, probed))),
		"a first hop that did not answer the probe still sorts last")
}

// mkDstEntryLat is mkDstEntry with the destination-side hop's own measured
// latency (transport.Entry.Latency, latency_ms) — the intermediate→exit number
// the whole-path ranking needs.
func mkDstEntryLat(dst, peer cipher.PubKey, tpType tptypes.Type, latencyMs float64) *transport.Entry {
	e := mkDstEntry(dst, peer, tpType)
	e.Latency = latencyMs
	return e
}

// TestRankByPathLatency_WholePathNotFirstHop is the 2026-09-18 fix. Ranking on
// the FIRST HOP alone put a 1ms LAN neighbour at the top of a list of ~25
// candidates — a peer on our own uplink whose own hop to the exit was unknown —
// and the tunnel delivered 3.85 MB/s on 50 MB down (2.46 on 10 MB), worse than
// the 470ms intermediate the ranking was introduced to avoid. A candidate is
// ranked by what it costs END TO END, and one whose remainder is unknown is not
// ranked at all: it sorts with the unmeasured.
func TestRankByPathLatency_WholePathNotFirstHop(t *testing.T) {
	a, b, c, d, e := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()

	mk := func(id uuid.UUID, hop1, hop2 float64) []routing.Hop {
		p := rankCandPath(id, 2)
		p[0].Latency = hop1
		p[1].Latency = hop2 // 0 = unknown remainder
		return p
	}
	cands := [][]routing.Hop{
		mk(a, 1, 0),    // fastest first hop, unknown onward → not a ranking
		mk(b, 29, 95),  // 124
		mk(c, 144, 10), // 154
		mk(d, 134, 1),  // 135
		mk(e, 199, 0),  // unknown onward
	}

	ranked := rankByPathLatency(cands, nil)
	require.Equal(t, []uuid.UUID{b, d, c, a, e}, firstTpIDs(ranked),
		"measured paths rank by their total; partially-known ones keep input order, last")

	require.Equal(t,
		b.String()[:8]+"=29+95ms, "+d.String()[:8]+"=134+1ms, "+c.String()[:8]+"=144+10ms, "+
			a.String()[:8]+"=1ms+unknown, "+e.String()[:8]+"=199ms+unknown",
		pathLatencyTrail(ranked, nil, nil),
		"the trail spells the unknown remainder out rather than hiding it in a total")

	total, ok := pathLatencyTotalMs(cands[1], nil)
	require.True(t, ok)
	require.Equal(t, float64(124), total)
	_, ok = pathLatencyTotalMs(cands[0], nil)
	require.False(t, ok, "a path with an unmeasured hop has no total")
}

// TestFilterLANFirstHops_ExcludesNeighbours: a first hop on our own LAN is not
// diversity — it shares our uplink — so it is dropped before ranking and named
// on the dial trail. Reuses the #4253 same-LAN check (and its
// routing.exclude_same_lan_hops switch), so turning that off restores the old
// behavior here too.
func TestFilterLANFirstHops_ExcludesNeighbours(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	exit, _ := cipher.GenerateKeyPair()
	neighbour, _ := cipher.GenerateKeyPair()
	atlanta, _ := cipher.GenerateKeyPair()

	lanTp, viaA := uuid.New(), uuid.New()
	cands := [][]routing.Hop{
		viaCand(src, neighbour, exit, lanTp, 1),
		viaCand(src, atlanta, exit, viaA, 39),
	}

	// The check is driven by the transport manager's view of which peers are on
	// our LAN; with none reported (or the feature off) nothing is dropped.
	r := &router{conf: &Config{}}
	keep, dropped := r.filterLANFirstHops(cands)
	require.Len(t, keep, 2)
	require.Empty(t, dropped)

	// With the neighbour reported as same-LAN it is refused, and the survivor is
	// what gets ranked.
	r = &router{conf: &Config{}, sameLANPeersFn: func() []cipher.PubKey { return []cipher.PubKey{neighbour} }}
	keep, dropped = r.filterLANFirstHops(cands)
	require.Equal(t, []uuid.UUID{viaA}, firstTpIDs(keep))
	require.Equal(t, []uuid.UUID{lanTp}, firstTpIDs(dropped))

	opts := &DialOptions{}
	ranked := r.rankFreeFirstHops(context.Background(), opts, cands, func(uuid.UUID) float64 { return 0 })
	require.Equal(t, []uuid.UUID{viaA}, firstTpIDs(ranked))
	require.Len(t, opts.dialNotes, 2)
	require.Equal(t,
		"excluding 1 same-LAN first hop(s) (share our uplink): "+lanTp.String()[:8],
		opts.dialNotes[0])
	require.Contains(t, opts.dialNotes[1], "ranked by path latency: "+viaA.String()[:8]+"=39+10ms")
}
