package policy

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/router"
)

// selfHealEngine is a minimal Engine that also reports a self-heal target,
// modeling the adaptive wasm Loader (which forwards Evaluator.SelfHealTarget).
type selfHealEngine struct {
	target int
	ok     bool
}

func (selfHealEngine) Decide(context.Context, RoutingContext, []Candidate) (RouteSpec, error) {
	return RouteSpec{}, nil
}
func (selfHealEngine) OnLegChange(context.Context, RoutingContext, []LegInfo, LegChange) (RouteSpec, error) {
	return RouteSpec{}, nil
}
func (selfHealEngine) OnTick(context.Context, RoutingContext, []LegInfo) (RotationAction, error) {
	return RotationAction{}, nil
}
func (selfHealEngine) IsActive() bool                { return true }
func (selfHealEngine) Source() string                { return "<fake>" }
func (selfHealEngine) Close() error                  { return nil }
func (e selfHealEngine) SelfHealTarget() (int, bool) { return e.target, e.ok }

// TestHookSelfHealTarget_ForwardsFromEngine is the regression for the
// wasm-preset self-heal storm: the production RotationHook is *policy.Hook, so it
// MUST satisfy router.SelfHealTargeter and forward the engine's live target —
// otherwise the route group's per-tick re-cap is skipped and the pool storms
// back toward its dial-time degree (513) regardless of `cli proxy mux standby`.
func TestHookSelfHealTarget_ForwardsFromEngine(t *testing.T) {
	h := NewHook(selfHealEngine{target: 41, ok: true})

	var st router.SelfHealTargeter = h // *Hook must satisfy the optional interface
	got, ok := st.SelfHealTarget()
	if !ok || got != 41 {
		t.Fatalf("Hook.SelfHealTarget = (%d,%v), want (41,true)", got, ok)
	}
}

// TestHookSelfHealTarget_NoTarget: engines that report no target (non-adaptive
// preset) or don't implement the optional interface, and a nil-engine hook, all
// leave the dial-time target in place (ok=false).
func TestHookSelfHealTarget_NoTarget(t *testing.T) {
	if _, ok := NewHook(selfHealEngine{ok: false}).SelfHealTarget(); ok {
		t.Fatal("engine reporting ok=false should yield ok=false")
	}
	if _, ok := NewHook(nil).SelfHealTarget(); ok {
		t.Fatal("nil-engine hook should yield ok=false")
	}
}
