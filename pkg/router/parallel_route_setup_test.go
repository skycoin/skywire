// parallel_route_setup_test.go — unit tests for the PARALLEL candidate
// route-group setup (the steady-connection fix). These cover the pure /
// injectable pieces so the race logic is verified without standing up real
// transports or a peer visor:
//
//   - raceCandidateSetup: winner selection (first handshake to complete wins),
//     handshake-failure fall-through to the next reserved candidate, loser
//     penalty arming, all-fail, and K=1 single-candidate (sequential-equivalent).
//   - suspectHopCache: arm / isSuspect / TTL eviction / suspectCount.
//   - effectiveParallelK: opts override, config default, clamping.
//   - rankCandidatePaths: suspect deprioritization + disjoint preference.
package router

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// testRouter builds a minimal router sufficient for the injectable race /
// ranking helpers (no transports, no dmsg).
func testRouter() *router {
	return &router{
		conf:     &Config{},
		logger:   logging.MustGetLogger("parallel_test"),
		suspects: newSuspectHopCache(5 * time.Second),
	}
}

// twoHop builds a forward path src -> mid -> dst so the intermediate is `mid`.
func twoHop(src, mid, dst cipher.PubKey) []routing.Hop {
	return []routing.Hop{
		{TpID: uuid.New(), From: src, To: mid},
		{TpID: uuid.New(), From: mid, To: dst},
	}
}

func candidate(fwd []routing.Hop) routing.BidirectionalRoute {
	return routing.BidirectionalRoute{Forward: fwd, Reverse: fwd}
}

// nullNode is returned by fake dials so raceCandidateSetup skips ReorderSetupNodes.
var nullNode = cipher.PubKey{}

func TestRaceCandidateSetup_FirstToCompleteWins(t *testing.T) {
	r := testRouter()
	src, dst := mustPK(t), mustPK(t)
	// Three candidates; index 1 dials fastest and its handshake succeeds.
	cands := []routing.BidirectionalRoute{
		candidate(twoHop(src, mustPK(t), dst)),
		candidate(twoHop(src, mustPK(t), dst)),
		candidate(twoHop(src, mustPK(t), dst)),
	}

	winnerNRG := &NoiseRouteGroup{}
	dial := func(ctx context.Context, c routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
		// Candidate 1 returns immediately; the others are slow so candidate 1's
		// reservation result is consumed first.
		if !sameFwd(c.Forward, cands[1].Forward) {
			select {
			case <-ctx.Done():
			case <-time.After(60 * time.Millisecond):
			}
		}
		return routing.EdgeRules{}, nullNode, nil
	}
	handshake := func(ctx context.Context, c routing.BidirectionalRoute, _ routing.EdgeRules) (*NoiseRouteGroup, error) {
		return winnerNRG, nil
	}

	nrg, _, winIdx, err := r.raceCandidateSetup(context.Background(), r.logger, cands, dial, handshake, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if winIdx != 1 {
		t.Fatalf("winIdx=%d, want 1 (fastest reservation)", winIdx)
	}
	if nrg != winnerNRG {
		t.Fatalf("returned nrg is not the handshake winner")
	}
}

func TestRaceCandidateSetup_HandshakeFailFallsThrough(t *testing.T) {
	r := testRouter()
	src, dst := mustPK(t), mustPK(t)
	deadMid := mustPK(t)
	goodMid := mustPK(t)
	// Candidate 0 reserves fastest but its handshake FAILS (ACKed reservation,
	// won't forward). Candidate 1 reserves slightly slower and succeeds.
	cands := []routing.BidirectionalRoute{
		candidate(twoHop(src, deadMid, dst)),
		candidate(twoHop(src, goodMid, dst)),
	}

	dial := func(ctx context.Context, c routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
		if sameFwd(c.Forward, cands[1].Forward) {
			select {
			case <-ctx.Done():
			case <-time.After(40 * time.Millisecond):
			}
		}
		return routing.EdgeRules{}, nullNode, nil
	}
	winnerNRG := &NoiseRouteGroup{}
	handshake := func(ctx context.Context, c routing.BidirectionalRoute, _ routing.EdgeRules) (*NoiseRouteGroup, error) {
		if sameFwd(c.Forward, cands[0].Forward) {
			return nil, errors.New("handshake timeout: dead intermediate")
		}
		return winnerNRG, nil
	}

	var mu sync.Mutex
	var lost []cipher.PubKey
	onLoser := func(c routing.BidirectionalRoute) {
		// Mirror the production onLoser: arm suspects + record for assertion.
		r.suspects.armAll(intermediatePKsOfPath(c.Forward, src, dst))
		mu.Lock()
		lost = append(lost, intermediatePKsOfPath(c.Forward, src, dst)...)
		mu.Unlock()
	}

	nrg, _, winIdx, err := r.raceCandidateSetup(context.Background(), r.logger, cands, dial, handshake, onLoser)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if winIdx != 1 || nrg != winnerNRG {
		t.Fatalf("winIdx=%d nrg=%p, want winIdx=1 with winner nrg", winIdx, nrg)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lost) != 1 || lost[0] != deadMid {
		t.Fatalf("onLoser intermediates=%v, want exactly [deadMid]", lost)
	}
	// The dead intermediate must now be suspect for the next dial.
	if !r.suspects.isSuspect(deadMid, time.Now()) {
		t.Fatalf("deadMid not armed suspect after handshake failure")
	}
}

func TestRaceCandidateSetup_AllFail(t *testing.T) {
	r := testRouter()
	src, dst := mustPK(t), mustPK(t)
	cands := []routing.BidirectionalRoute{
		candidate(twoHop(src, mustPK(t), dst)),
		candidate(twoHop(src, mustPK(t), dst)),
	}
	dialErr := errors.New("reserve failed: unreachable")
	dial := func(ctx context.Context, c routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
		return routing.EdgeRules{}, nullNode, dialErr
	}
	handshake := func(ctx context.Context, c routing.BidirectionalRoute, _ routing.EdgeRules) (*NoiseRouteGroup, error) {
		t.Error("handshake must not run when every reservation fails")
		return nil, nil
	}
	var losers int
	var mu sync.Mutex
	onLoser := func(c routing.BidirectionalRoute) { mu.Lock(); losers++; mu.Unlock() }

	nrg, _, winIdx, err := r.raceCandidateSetup(context.Background(), r.logger, cands, dial, handshake, onLoser)
	if err == nil {
		t.Fatal("want error when all candidates fail, got nil")
	}
	if nrg != nil || winIdx != -1 {
		t.Fatalf("nrg=%p winIdx=%d, want nil/-1", nrg, winIdx)
	}
	mu.Lock()
	defer mu.Unlock()
	if losers != len(cands) {
		t.Fatalf("onLoser called %d times, want %d", losers, len(cands))
	}
}

func TestRaceCandidateSetup_SingleCandidateSequentialEquivalent(t *testing.T) {
	// K resolving to 1 candidate must behave exactly like the sequential path:
	// one dial, one handshake, that candidate is the winner.
	r := testRouter()
	src, dst := mustPK(t), mustPK(t)
	cands := []routing.BidirectionalRoute{candidate(twoHop(src, mustPK(t), dst))}

	var dials, handshakes int
	dial := func(ctx context.Context, c routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
		dials++
		return routing.EdgeRules{}, nullNode, nil
	}
	win := &NoiseRouteGroup{}
	handshake := func(ctx context.Context, c routing.BidirectionalRoute, _ routing.EdgeRules) (*NoiseRouteGroup, error) {
		handshakes++
		return win, nil
	}
	nrg, _, winIdx, err := r.raceCandidateSetup(context.Background(), r.logger, cands, dial, handshake, nil)
	if err != nil || winIdx != 0 || nrg != win {
		t.Fatalf("single-candidate race: nrg=%p winIdx=%d err=%v", nrg, winIdx, err)
	}
	if dials != 1 || handshakes != 1 {
		t.Fatalf("dials=%d handshakes=%d, want 1/1 (sequential-equivalent)", dials, handshakes)
	}
}

func TestRaceCandidateSetup_CtxCanceled(t *testing.T) {
	r := testRouter()
	src, dst := mustPK(t), mustPK(t)
	cands := []routing.BidirectionalRoute{candidate(twoHop(src, mustPK(t), dst))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dial := func(ctx context.Context, c routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
		<-ctx.Done()
		return routing.EdgeRules{}, nullNode, ctx.Err()
	}
	handshake := func(ctx context.Context, c routing.BidirectionalRoute, _ routing.EdgeRules) (*NoiseRouteGroup, error) {
		return &NoiseRouteGroup{}, nil
	}
	_, _, _, err := r.raceCandidateSetup(ctx, r.logger, cands, dial, handshake, nil)
	if err == nil {
		t.Fatal("want ctx error, got nil")
	}
}

func TestEffectiveParallelK(t *testing.T) {
	r := testRouter()
	r.conf.ParallelRouteSetup = 3

	if got := r.effectiveParallelK(nil); got != 3 {
		t.Errorf("nil opts: k=%d, want 3 (config)", got)
	}
	if got := r.effectiveParallelK(&DialOptions{ParallelRouteSetup: 1}); got != 1 {
		t.Errorf("opts override 1: k=%d, want 1", got)
	}
	if got := r.effectiveParallelK(&DialOptions{ParallelRouteSetup: 99}); got != maxParallelRouteSetup {
		t.Errorf("opts override 99: k=%d, want clamp %d", got, maxParallelRouteSetup)
	}
	r.conf.ParallelRouteSetup = 0
	if got := r.effectiveParallelK(nil); got != 1 {
		t.Errorf("unset config: k=%d, want 1 (safe default at read time)", got)
	}
}

func TestSuspectHopCache(t *testing.T) {
	c := newSuspectHopCache(30 * time.Millisecond)
	pk := cipher.PubKey{1, 2, 3}
	if c.isSuspect(pk, time.Now()) {
		t.Fatal("fresh cache reports suspect")
	}
	c.arm(pk)
	if !c.isSuspect(pk, time.Now()) {
		t.Fatal("armed pk not suspect")
	}
	// TTL eviction.
	if c.isSuspect(pk, time.Now().Add(time.Second)) {
		t.Fatal("pk still suspect past TTL")
	}
	// Null PK is a no-op.
	c.arm(cipher.PubKey{})
	if c.isSuspect(cipher.PubKey{}, time.Now()) {
		t.Fatal("null pk should never be suspect")
	}
}

func TestSuspectCount(t *testing.T) {
	c := newSuspectHopCache(time.Minute)
	src, mid, dst := cipher.PubKey{1}, cipher.PubKey{2}, cipher.PubKey{3}
	path := twoHop(src, mid, dst)
	if n := c.suspectCount(path, src, dst, time.Now()); n != 0 {
		t.Fatalf("no suspects: count=%d, want 0", n)
	}
	c.arm(mid)
	if n := c.suspectCount(path, src, dst, time.Now()); n != 1 {
		t.Fatalf("armed mid: count=%d, want 1", n)
	}
}

func TestRankCandidatePaths_SuspectDeprioritized(t *testing.T) {
	r := testRouter()
	src, dst := cipher.PubKey{9}, cipher.PubKey{10}
	suspectMid := cipher.PubKey{1}
	goodMid := cipher.PubKey{2}
	suspectPath := twoHop(src, suspectMid, dst)
	goodPath := twoHop(src, goodMid, dst)
	r.suspects.arm(suspectMid)

	latency := func(uuid.UUID) float64 { return 0 } // all unknown => equal base score
	// Present the suspect path FIRST; ranking must still float the good one up.
	out := r.rankCandidatePaths([][]routing.Hop{suspectPath, goodPath}, src, dst, nil, latency, nil, nil, 2)
	if len(out) != 2 {
		t.Fatalf("got %d candidates, want 2", len(out))
	}
	if !sameFwd(out[0], goodPath) {
		t.Fatalf("first candidate is not the non-suspect path")
	}
}

func TestRankCandidatePaths_HardExcludeAndDisjoint(t *testing.T) {
	r := testRouter()
	src, dst := cipher.PubKey{9}, cipher.PubKey{10}
	excl := cipher.PubKey{1}
	a := cipher.PubKey{2}
	b := cipher.PubKey{3}
	exclPath := twoHop(src, excl, dst)
	aPath := twoHop(src, a, dst)
	bPath := twoHop(src, b, dst)
	latency := func(uuid.UUID) float64 { return 100 }

	out := r.rankCandidatePaths([][]routing.Hop{exclPath, aPath, bPath}, src, dst,
		[]cipher.PubKey{excl}, latency, nil, nil, 3)
	// Excluded path dropped; two disjoint candidates remain.
	if len(out) != 2 {
		t.Fatalf("got %d, want 2 (excluded dropped)", len(out))
	}
	for _, p := range out {
		for _, ipk := range intermediatePKsOfPath(p, src, dst) {
			if ipk == excl {
				t.Fatalf("hard-excluded intermediate present in ranked output")
			}
		}
	}
}

// sameFwd reports whether two forward paths share the same intermediate PK
// sequence (TpIDs are random per construction, so compare by From/To edges).
func sameFwd(a, b []routing.Hop) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].From != b[i].From || a[i].To != b[i].To {
			return false
		}
	}
	return true
}
