// Package policy pkg/router/policy/evaluator_test.go — end-to-end
// tests pinning the operator-facing semantics: load a script,
// inject a clock so the test asserts time-dependent decisions
// deterministically, verify the returned RouteSpec matches what
// the policy wrote.
package policy

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeClock implements Clock with a value the test sets at
// construction. The Now() value is fixed unless the test
// explicitly updates the field.
type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }

// fridayAt is shorthand for "build a Friday at the given hour in
// UTC." Used by the Indonesia-Friday tests so the assertion
// reads as the operator would describe the scenario.
func fridayAt(hour int) time.Time {
	// 2026-05-29 is a Friday.
	return time.Date(2026, 5, 29, hour, 0, 0, 0, time.UTC)
}

func tuesdayAt(hour int) time.Time {
	// 2026-05-26 is a Tuesday.
	return time.Date(2026, 5, 26, hour, 0, 0, 0, time.UTC)
}

// indonesiaFridayPolicy is the RFC's headline example, condensed.
// At Friday 5pm only routes through Indonesia survive; otherwise
// the lowest-latency candidate wins.
const indonesiaFridayPolicy = `
def decide_route(ctx, candidates):
    t = ctx.now()
    is_friday_evening = datetime.weekday(t) == "friday" and t.hour == 17
    if is_friday_evening:
        candidates = [c for c in candidates if "ID" in c.hops_geo]
    if not candidates:
        return RouteSpec(fallback="direct")
    # lowest-latency wins
    chosen = candidates[0]
    for c in candidates[1:]:
        if c.est_latency_ms > 0 and (chosen.est_latency_ms == 0 or c.est_latency_ms < chosen.est_latency_ms):
            chosen = c
    return RouteSpec(chosen=chosen, mux=4 if ctx.app == "vpn-client" else 1)
`

// candidateThroughIDFastest et al describe the synthetic candidate
// fixtures the tests reuse. Named for what the test asserts about
// them so call sites read like prose.
var (
	candidateThroughID = Candidate{
		Hops:           []string{"pkA", "pkB"},
		HopsGeo:        []string{"SG", "ID"},
		EstLatencyMs:   100,
		TransportKinds: []string{"stcpr"},
	}
	candidateThroughUSFastest = Candidate{
		Hops:           []string{"pkC"},
		HopsGeo:        []string{"US"},
		EstLatencyMs:   30,
		TransportKinds: []string{"stcpr"},
	}
	candidateThroughEU = Candidate{
		Hops:           []string{"pkD", "pkE"},
		HopsGeo:        []string{"DE", "FR"},
		EstLatencyMs:   60,
		TransportKinds: []string{"sudph"},
	}
)

func TestIndonesiaFriday_OnFridayAt5pm_PicksIDRoute(t *testing.T) {
	clock := &fakeClock{now: fridayAt(17)}
	eval, err := NewEvaluator("test.star", indonesiaFridayPolicy, WithClock(clock))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	spec, err := eval.Decide(
		context.Background(),
		RoutingContext{App: "skychat"},
		[]Candidate{candidateThroughID, candidateThroughUSFastest, candidateThroughEU},
	)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Chosen == nil {
		t.Fatalf("expected ID-transit candidate, got nil (fallback=%q)", spec.Fallback)
	}
	if !contains(spec.Chosen.HopsGeo, "ID") {
		t.Errorf("chosen route's HopsGeo=%v, expected to include ID", spec.Chosen.HopsGeo)
	}
}

func TestIndonesiaFriday_OnTuesday_PicksLowestLatency(t *testing.T) {
	clock := &fakeClock{now: tuesdayAt(17)}
	eval, err := NewEvaluator("test.star", indonesiaFridayPolicy, WithClock(clock))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	spec, err := eval.Decide(
		context.Background(),
		RoutingContext{App: "skychat"},
		[]Candidate{candidateThroughID, candidateThroughUSFastest, candidateThroughEU},
	)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Chosen == nil {
		t.Fatalf("expected a chosen route, got nil")
	}
	if spec.Chosen.EstLatencyMs != 30 {
		t.Errorf("expected 30ms (US route), got %dms", spec.Chosen.EstLatencyMs)
	}
}

func TestIndonesiaFriday_FridayButNoIDCandidate_FallsBackToDirect(t *testing.T) {
	clock := &fakeClock{now: fridayAt(17)}
	eval, err := NewEvaluator("test.star", indonesiaFridayPolicy, WithClock(clock))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	spec, err := eval.Decide(
		context.Background(),
		RoutingContext{App: "skychat"},
		[]Candidate{candidateThroughUSFastest, candidateThroughEU}, // no ID
	)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Chosen != nil {
		t.Errorf("expected chosen=nil (fallback path), got %+v", spec.Chosen)
	}
	if spec.Fallback != "direct" {
		t.Errorf("Fallback=%q, expected \"direct\"", spec.Fallback)
	}
}

func TestPerApp_VPNGetsMux4_SkychatGetsMux1(t *testing.T) {
	clock := &fakeClock{now: tuesdayAt(10)}
	eval, err := NewEvaluator("test.star", indonesiaFridayPolicy, WithClock(clock))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	specSkychat, _ := eval.Decide(context.Background(), RoutingContext{App: "skychat"},
		[]Candidate{candidateThroughUSFastest})
	if specSkychat.Mux != 1 {
		t.Errorf("skychat: mux=%d, want 1", specSkychat.Mux)
	}
	specVPN, _ := eval.Decide(context.Background(), RoutingContext{App: "vpn-client"},
		[]Candidate{candidateThroughUSFastest})
	if specVPN.Mux != 4 {
		t.Errorf("vpn-client: mux=%d, want 4", specVPN.Mux)
	}
}

func TestLoadFailure_MissingDecideRoute(t *testing.T) {
	_, err := NewEvaluator("bad.star", `x = 1`)
	if err == nil {
		t.Fatal("expected an error for missing decide_route, got nil")
	}
	if !strings.Contains(err.Error(), "missing decide_route") {
		t.Errorf("error %q doesn't mention missing decide_route", err)
	}
}

func TestLoadFailure_SyntaxError(t *testing.T) {
	_, err := NewEvaluator("bad.star", `def decide_route(`)
	if err == nil {
		t.Fatal("expected a syntax error, got nil")
	}
}

func TestRuntimeFailure_FallbackMode_ReturnsEmptySpec(t *testing.T) {
	// A policy that divides by zero blows up at runtime; with
	// the default fallback mode the evaluator returns a clean
	// empty RouteSpec rather than propagating the error.
	src := `
def decide_route(ctx, candidates):
    return 1 / 0
`
	var logged string
	eval, err := NewEvaluator("boom.star", src,
		WithLogger(func(format string, args ...interface{}) {
			logged += format // capture the first format string
		}))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	spec, callErr := eval.Decide(context.Background(), RoutingContext{App: "x"}, nil)
	if callErr != nil {
		t.Errorf("expected nil error in fallback mode, got %v", callErr)
	}
	if spec.Chosen != nil || spec.Fallback != "" {
		t.Errorf("expected empty fallback spec, got %+v", spec)
	}
	if logged == "" {
		t.Error("expected the failure to be logged")
	}
}

func TestRuntimeFailure_DropMode_ReturnsError(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    return 1 / 0
`
	eval, err := NewEvaluator("boom.star", src, WithFailureMode(FailureDrop))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	spec, callErr := eval.Decide(context.Background(), RoutingContext{App: "x"}, nil)
	if callErr == nil {
		t.Errorf("expected an error in drop mode, got nil")
	}
	if spec.Fallback != "drop" {
		t.Errorf("Fallback=%q, expected \"drop\"", spec.Fallback)
	}
}

func TestTimeout_InfiniteLoop_TerminatesAndFallsBack(t *testing.T) {
	// A policy with a runaway loop hits the step ceiling and
	// gets canceled. Asserts the evaluator returns within a
	// bounded time rather than hanging the test goroutine.
	src := `
def decide_route(ctx, candidates):
    n = 0
    for i in range(10000000):
        n = n + i
    return RouteSpec(chosen=Candidate(hops=[]))
`
	eval, err := NewEvaluator("loop.star", src,
		WithMaxSteps(10000), // tight cap so the loop trips quickly
	)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	start := time.Now()
	spec, callErr := eval.Decide(context.Background(), RoutingContext{App: "x"}, nil)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Decide took %v, expected to terminate quickly", elapsed)
	}
	if callErr != nil {
		t.Errorf("expected fallback-on-failure path, got error %v", callErr)
	}
	if spec.Chosen != nil {
		t.Errorf("expected nil chosen on timeout fallback, got %+v", spec.Chosen)
	}
}

func TestExplicitNone_ReturnsEmptySpec(t *testing.T) {
	// A policy that explicitly returns None means "I have no
	// preference, use the visor's default."
	src := `
def decide_route(ctx, candidates):
    return None
`
	eval, err := NewEvaluator("none.star", src)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	spec, err := eval.Decide(context.Background(), RoutingContext{App: "x"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Chosen != nil || spec.Fallback != "" {
		t.Errorf("expected empty spec on explicit None, got %+v", spec)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
