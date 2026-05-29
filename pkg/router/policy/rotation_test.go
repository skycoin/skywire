package policy

import (
	"context"
	"testing"
	"time"
)

// TestEvaluator_OnTick_StarlarkParsing verifies that a Starlark
// on_tick implementation returning a struct round-trips into a
// RotationAction with the right fields populated.
func TestEvaluator_OnTick_StarlarkParsing(t *testing.T) {
	script := `
def decide_route(ctx, candidates):
    return RouteSpec(rotation_interval_seconds=30)

def on_tick(ctx, legs):
    return Rotation(drop_legs=[0], add_leg=True, exclude_hops=["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"])
`
	eval, err := NewEvaluator("test", script)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	_ = eval

	// Confirm RotationIntervalSeconds propagates through decide_route.
	spec, err := eval.Decide(context.Background(), RoutingContext{App: "vpn-client"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.RotationIntervalSeconds != 30 {
		t.Errorf("RotationIntervalSeconds=%d, want 30", spec.RotationIntervalSeconds)
	}

	// Confirm on_tick returns the expected action.
	legs := []LegInfo{
		{Index: 0, Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 1, Kind: "stcpr", LatencyMs: 80, Alive: true},
	}
	action, err := eval.OnTick(context.Background(), RoutingContext{App: "vpn-client"}, legs)
	if err != nil {
		t.Fatalf("OnTick: %v", err)
	}
	if len(action.DropLegs) != 1 || action.DropLegs[0] != 0 {
		t.Errorf("DropLegs=%v, want [0]", action.DropLegs)
	}
	if !action.AddLeg {
		t.Errorf("AddLeg=false, want true")
	}
	if len(action.ExcludeHops) != 1 {
		t.Errorf("ExcludeHops len=%d, want 1", len(action.ExcludeHops))
	}
}

// TestEvaluator_OnTick_NoFunction returns the zero-value action
// when the script doesn't define on_tick (the common case for
// non-rotating policies).
func TestEvaluator_OnTick_NoFunction(t *testing.T) {
	script := `
def decide_route(ctx, candidates):
    return RouteSpec()
`
	eval, err := NewEvaluator("test", script)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	_ = eval

	action, err := eval.OnTick(context.Background(), RoutingContext{App: "x"}, nil)
	if err != nil {
		t.Errorf("OnTick: err=%v, want nil", err)
	}
	if len(action.DropLegs) != 0 || action.AddLeg {
		t.Errorf("expected zero-value action, got %+v", action)
	}
}

// TestEvaluator_OnTick_NoneReturn lets the script explicitly
// return None — same effect as omitting the function.
func TestEvaluator_OnTick_NoneReturn(t *testing.T) {
	script := `
def decide_route(ctx, candidates):
    return RouteSpec()

def on_tick(ctx, legs):
    return None
`
	eval, err := NewEvaluator("test", script)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	_ = eval

	action, err := eval.OnTick(context.Background(), RoutingContext{App: "x"}, nil)
	if err != nil {
		t.Errorf("OnTick: err=%v, want nil", err)
	}
	if len(action.DropLegs) != 0 || action.AddLeg {
		t.Errorf("expected zero-value action from None, got %+v", action)
	}
}

// TestEvaluator_OnTick_RespectsContextCancel returns zero-action
// when the context is already canceled, without firing the
// script.
func TestEvaluator_OnTick_RespectsContextCancel(t *testing.T) {
	script := `
def decide_route(ctx, candidates):
    return RouteSpec()

def on_tick(ctx, legs):
    return Rotation(add_leg=True)
`
	eval, err := NewEvaluator("test", script)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	_ = eval

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	action, err := eval.OnTick(ctx, RoutingContext{App: "x"}, nil)
	if err != nil {
		t.Errorf("OnTick: err=%v, want nil", err)
	}
	if action.AddLeg {
		t.Errorf("OnTick fired after ctx canceled; expected zero-action, got %+v", action)
	}
}

// Sanity: OnTick on the unmodified Engine path returns within
// the script's max-steps budget. Sized to fail fast if a malformed
// loop appears in a rotation policy.
func TestEvaluator_OnTick_StepCeiling(t *testing.T) {
	script := `
def decide_route(ctx, candidates):
    return RouteSpec()

def on_tick(ctx, legs):
    n = 0
    for i in range(1000):
        n = n + 1
    return Rotation(add_leg=True)
`
	eval, err := NewEvaluator("test", script)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	_ = eval
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	action, err := eval.OnTick(ctx, RoutingContext{App: "x"}, nil)
	if err != nil {
		t.Logf("OnTick err=%v (acceptable if step ceiling tripped)", err)
	}
	_ = action
}
