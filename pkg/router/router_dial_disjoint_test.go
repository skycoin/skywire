// router_dial_disjoint_test.go: unit tests for the disjoint-mux
// helpers added 2026-05-20. The mux-loop accumulator + the
// fetchBestRoutes post-filter both depend on these primitives;
// keeping them coverable in isolation lets the integration tests
// stay focused on dialer-level behavior.
package router

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

func hop(from, to cipher.PubKey) routing.Hop {
	return routing.Hop{TpID: uuid.New(), From: from, To: to}
}

func TestPathTouchesIntermediate_EmptyAndDirect(t *testing.T) {
	src := mustPK(t)
	dst := mustPK(t)
	exclude := map[cipher.PubKey]struct{}{
		mustPK(t): {},
	}

	// Empty path: cannot touch anything.
	if pathTouchesIntermediate(nil, exclude) {
		t.Error("empty path: expected false, got true")
	}

	// Direct 1-hop path src->dst: only the endpoints exist, no
	// intermediates, so even a non-empty exclude set must not match.
	direct := []routing.Hop{hop(src, dst)}
	if pathTouchesIntermediate(direct, exclude) {
		t.Error("direct 1-hop: expected false (no intermediates), got true")
	}
}

func TestPathTouchesIntermediate_ExcludedIntermediateHit(t *testing.T) {
	src := mustPK(t)
	mid := mustPK(t)
	dst := mustPK(t)

	// 2-hop src->mid->dst with mid in the exclude set.
	path := []routing.Hop{hop(src, mid), hop(mid, dst)}
	exclude := map[cipher.PubKey]struct{}{mid: {}}

	if !pathTouchesIntermediate(path, exclude) {
		t.Error("2-hop with excluded intermediate: expected true, got false")
	}
}

func TestPathTouchesIntermediate_DestinationNotIntermediate(t *testing.T) {
	src := mustPK(t)
	mid := mustPK(t)
	dst := mustPK(t)

	// 2-hop path; if dst is in the exclude set, we should NOT flag
	// it — dst is the endpoint, not an intermediate.
	path := []routing.Hop{hop(src, mid), hop(mid, dst)}
	exclude := map[cipher.PubKey]struct{}{dst: {}}

	if pathTouchesIntermediate(path, exclude) {
		t.Error("dst in exclude set: expected false (dst is endpoint, not intermediate), got true")
	}
}

func TestPickDisjointPath_NoExcludeReturnsFirst(t *testing.T) {
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	dst := mustPK(t)

	fwd := [][]routing.Hop{
		{hop(src, mid1), hop(mid1, dst)},
		{hop(src, mid2), hop(mid2, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, mid1), hop(mid1, src)},
		{hop(dst, mid2), hop(mid2, src)},
	}

	f, r, ok := pickDisjointPath(fwd, rev, nil, nil)
	if !ok {
		t.Fatal("no-exclude: expected ok=true")
	}
	if f[0].To != mid1 {
		t.Errorf("no-exclude: expected first path (mid1), got %v", f[0].To)
	}
	_ = r
}

func TestPickDisjointPath_SkipsExcludedIntermediate(t *testing.T) {
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	mid3 := mustPK(t)
	dst := mustPK(t)

	// Three candidate routes: via mid1, mid2, mid3.
	// Exclude mid1 + mid2 → only mid3's path remains.
	fwd := [][]routing.Hop{
		{hop(src, mid1), hop(mid1, dst)},
		{hop(src, mid2), hop(mid2, dst)},
		{hop(src, mid3), hop(mid3, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, mid1), hop(mid1, src)},
		{hop(dst, mid2), hop(mid2, src)},
		{hop(dst, mid3), hop(mid3, src)},
	}

	f, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{mid1, mid2}, nil)
	if !ok {
		t.Fatal("exclude mid1+mid2 with mid3 available: expected ok=true")
	}
	if f[0].To != mid3 {
		t.Errorf("exclude mid1+mid2: expected mid3 path, got %v", f[0].To)
	}
}

func TestPickDisjointPath_AllExcluded(t *testing.T) {
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	dst := mustPK(t)

	fwd := [][]routing.Hop{
		{hop(src, mid1), hop(mid1, dst)},
		{hop(src, mid2), hop(mid2, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, mid1), hop(mid1, src)},
		{hop(dst, mid2), hop(mid2, src)},
	}

	_, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{mid1, mid2}, nil)
	if ok {
		t.Error("all paths excluded: expected ok=false")
	}
}

func TestPickDisjointPath_ReverseAlsoFiltered(t *testing.T) {
	src := mustPK(t)
	midF := mustPK(t)
	midR := mustPK(t)
	dst := mustPK(t)

	// Asymmetric path: forward via midF, reverse via midR.
	// If midR is excluded, the pair must be rejected even though
	// forward is clean.
	fwd := [][]routing.Hop{
		{hop(src, midF), hop(midF, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, midR), hop(midR, src)},
	}

	_, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{midR}, nil)
	if ok {
		t.Error("midR excluded but in reverse path: expected ok=false")
	}
}

func TestIntermediatesOfHops(t *testing.T) {
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	dst := mustPK(t)

	cases := []struct {
		name string
		hops []routing.Hop
		want []cipher.PubKey
	}{
		{
			name: "empty",
			hops: nil,
			want: nil,
		},
		{
			name: "direct 1-hop",
			hops: []routing.Hop{hop(src, dst)},
			want: nil,
		},
		{
			name: "2-hop",
			hops: []routing.Hop{hop(src, mid1), hop(mid1, dst)},
			want: []cipher.PubKey{mid1},
		},
		{
			name: "3-hop",
			hops: []routing.Hop{hop(src, mid1), hop(mid1, mid2), hop(mid2, dst)},
			want: []cipher.PubKey{mid1, mid2},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := intermediatesOfHops(c.hops, src, dst)
			if len(got) != len(c.want) {
				t.Fatalf("%s: want %d intermediates, got %d (%v)", c.name, len(c.want), len(got), got)
			}
			for i, pk := range got {
				if pk != c.want[i] {
					t.Errorf("%s: index %d want %v got %v", c.name, i, c.want[i], pk)
				}
			}
		})
	}
}

func TestPathLatencyScore_KnownAndUnknown(t *testing.T) {
	src := mustPK(t)
	mid := mustPK(t)
	dst := mustPK(t)

	h1 := hop(src, mid)
	h2 := hop(mid, dst)

	// All hops known: score is the simple sum.
	lat := map[uuid.UUID]float64{h1.TpID: 50, h2.TpID: 30}
	score := pathLatencyScore([]routing.Hop{h1, h2}, func(id uuid.UUID) float64 { return lat[id] })
	if score != 80.0 {
		t.Errorf("known-only: want 80.0, got %.1f", score)
	}

	// One hop unknown (latency 0): unknown penalty applied.
	// Penalty constant (1000ms) ensures known-good paths outrank
	// any path with an unknown hop unless the known path is also slow.
	lat2 := map[uuid.UUID]float64{h1.TpID: 50}
	score2 := pathLatencyScore([]routing.Hop{h1, h2}, func(id uuid.UUID) float64 { return lat2[id] })
	if score2 != 1050.0 {
		t.Errorf("one-unknown: want 1050.0 (50 known + 1000 unknown penalty), got %.1f", score2)
	}
}

func TestPickDisjointPath_LatencyRanking(t *testing.T) {
	src := mustPK(t)
	midFast := mustPK(t)
	midSlow := mustPK(t)
	dst := mustPK(t)

	// Two acceptable candidates (no exclude). First is via the slow
	// intermediate, second is via the fast one. Pre-latency-rank
	// behavior would return the first. New behavior should return
	// the SECOND (lower total latency).
	hSlow := hop(src, midSlow)
	hSlowToDst := hop(midSlow, dst)
	hFast := hop(src, midFast)
	hFastToDst := hop(midFast, dst)

	fwd := [][]routing.Hop{
		{hSlow, hSlowToDst},
		{hFast, hFastToDst},
	}
	rev := [][]routing.Hop{
		{hop(dst, midSlow), hop(midSlow, src)},
		{hop(dst, midFast), hop(midFast, src)},
	}

	latency := map[uuid.UUID]float64{
		hSlow.TpID: 300, hSlowToDst.TpID: 250, // 550ms via slow
		hFast.TpID: 40, hFastToDst.TpID: 30, // 70ms via fast
	}
	// Reverse legs get the same per-tp latency lookup; the slow
	// intermediate path scores worse regardless.
	for _, p := range rev[0] {
		latency[p.TpID] = 300
	}
	for _, p := range rev[1] {
		latency[p.TpID] = 40
	}

	f, _, ok := pickDisjointPath(fwd, rev, nil, func(id uuid.UUID) float64 { return latency[id] })
	if !ok {
		t.Fatal("expected ok=true")
	}
	if f[0].To != midFast {
		t.Errorf("expected ranker to pick fast intermediate (%v), got %v", midFast, f[0].To)
	}
}

func TestPickDisjointPath_LatencyRankingWithExclude(t *testing.T) {
	// Three candidates: [excluded, slow-but-acceptable, fast-but-acceptable].
	// Ranker must skip the excluded one AND prefer fast over slow
	// among the remaining acceptable candidates.
	src := mustPK(t)
	midBad := mustPK(t)
	midSlow := mustPK(t)
	midFast := mustPK(t)
	dst := mustPK(t)

	fwd := [][]routing.Hop{
		{hop(src, midBad), hop(midBad, dst)},
		{hop(src, midSlow), hop(midSlow, dst)},
		{hop(src, midFast), hop(midFast, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, midBad), hop(midBad, src)},
		{hop(dst, midSlow), hop(midSlow, src)},
		{hop(dst, midFast), hop(midFast, src)},
	}

	// Assign latencies: bad (excluded, irrelevant), slow, fast.
	latency := map[uuid.UUID]float64{}
	for _, p := range append(fwd[1], rev[1]...) {
		latency[p.TpID] = 250
	}
	for _, p := range append(fwd[2], rev[2]...) {
		latency[p.TpID] = 40
	}

	f, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{midBad}, func(id uuid.UUID) float64 { return latency[id] })
	if !ok {
		t.Fatal("expected ok=true")
	}
	if f[0].To != midFast {
		t.Errorf("expected fast (%v) post-exclude, got %v", midFast, f[0].To)
	}
}

func TestPickDisjointPath_NoLatencyRankerLegacyBehavior(t *testing.T) {
	// When latencyFor is nil AND exclude is non-empty, the function
	// should return the first acceptable candidate — preserving the
	// pre-latency-rank semantics for callers that don't opt in.
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	dst := mustPK(t)

	fwd := [][]routing.Hop{
		{hop(src, mid1), hop(mid1, dst)},
		{hop(src, mid2), hop(mid2, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, mid1), hop(mid1, src)},
		{hop(dst, mid2), hop(mid2, src)},
	}

	// Non-empty exclude (just to make the function iterate), but no
	// ranker. Result should be the first-after-exclude — here, mid1
	// is not excluded so [0] passes.
	f, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{mustPK(t)}, nil)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if f[0].To != mid1 {
		t.Errorf("nil-ranker + non-empty exclude: want first acceptable (mid1), got %v", f[0].To)
	}
}

func TestPickDisjointPath_AsymmetricForwardReverse(t *testing.T) {
	// Forward and reverse should be ranked INDEPENDENTLY. Constructed
	// case: fwd[0] is slow, fwd[1] is fast; rev[0] is fast, rev[1] is
	// slow. Pre-unpair behavior would have paired (fwd[i], rev[i]) and
	// picked whichever index minimized the SUM — here either pair has
	// the same sum (slow+fast), so paired-by-index would have returned
	// the first one. Post-unpair: picks fwd[1] AND rev[0], which is
	// the strictly-better choice (fast in both directions).
	src := mustPK(t)
	midFastFwd := mustPK(t)
	midSlowFwd := mustPK(t)
	midFastRev := mustPK(t)
	midSlowRev := mustPK(t)
	dst := mustPK(t)

	hSlowFwd0 := hop(src, midSlowFwd)
	hSlowFwd1 := hop(midSlowFwd, dst)
	hFastFwd0 := hop(src, midFastFwd)
	hFastFwd1 := hop(midFastFwd, dst)
	hFastRev0 := hop(dst, midFastRev)
	hFastRev1 := hop(midFastRev, src)
	hSlowRev0 := hop(dst, midSlowRev)
	hSlowRev1 := hop(midSlowRev, src)

	fwd := [][]routing.Hop{
		{hSlowFwd0, hSlowFwd1}, // index 0 slow
		{hFastFwd0, hFastFwd1}, // index 1 fast
	}
	rev := [][]routing.Hop{
		{hFastRev0, hFastRev1}, // index 0 fast
		{hSlowRev0, hSlowRev1}, // index 1 slow
	}

	latency := map[uuid.UUID]float64{
		hSlowFwd0.TpID: 300, hSlowFwd1.TpID: 300, // 600ms total
		hFastFwd0.TpID: 30, hFastFwd1.TpID: 30, // 60ms total
		hFastRev0.TpID: 30, hFastRev1.TpID: 30, // 60ms total
		hSlowRev0.TpID: 300, hSlowRev1.TpID: 300, // 600ms total
	}

	f, r, ok := pickDisjointPath(fwd, rev, nil, func(id uuid.UUID) float64 { return latency[id] })
	if !ok {
		t.Fatal("expected ok=true")
	}
	if f[0].To != midFastFwd {
		t.Errorf("forward: expected midFastFwd (%v), got %v", midFastFwd, f[0].To)
	}
	if r[0].To != midFastRev {
		t.Errorf("reverse: expected midFastRev (%v), got %v", midFastRev, r[0].To)
	}
}

func TestPickDisjointPath_AsymmetricExcludeIntersection(t *testing.T) {
	// Exclude check applies per-direction. If midX is excluded:
	//   - any forward path passing through midX is rejected
	//   - any reverse path passing through midX is rejected independently
	// So a path pair where forward[i] is acceptable but every reverse[j]
	// touches the exclude set should yield ok=false.
	src := mustPK(t)
	mid := mustPK(t)
	dst := mustPK(t)

	// Forward has one good option (no excluded intermediate).
	fwd := [][]routing.Hop{
		{hop(src, mustPK(t)), hop(mustPK(t), dst)},
	}
	// Reverse: every candidate touches `mid` (which is excluded).
	rev := [][]routing.Hop{
		{hop(dst, mid), hop(mid, src)},
		{hop(dst, mid), hop(mid, src)},
	}

	_, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{mid}, nil)
	if ok {
		t.Error("expected ok=false when every reverse candidate touches exclude set")
	}
}
