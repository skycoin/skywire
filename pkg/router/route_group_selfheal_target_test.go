package router

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeSelfHealHook is a RotationHook that also implements SelfHealTargeter, so
// the rotation loop can push a live self-heal target the way the adaptive preset
// hook does. OnTick is a no-op (this test only exercises the target plumbing).
type fakeSelfHealHook struct {
	target int
	ok     bool
}

func (f fakeSelfHealHook) OnTick(_ DialInfo, _ []LegInfo) RotationAction { return RotationAction{} }
func (f fakeSelfHealHook) SelfHealTarget() (int, bool)                   { return f.target, f.ok }

// TestRotationServiceFnTracksSelfHealTarget is the fix for the runtime standby
// retune not reaching a RUNNING route group: the self-heal target is fixed at
// dial time (SetSelfHeal), but an adaptive group's pool size is a live tunable.
// The rotation loop must re-read it each tick (via SelfHealTargeter) so lowering
// the reserve at runtime actually drops maybeSelfHeal's effective target instead
// of leaving it dialing back toward the stale, larger dial-time value.
func TestRotationServiceFnTracksSelfHealTarget(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 1)

	// Dial-time target: the adaptive default (AdaptRevActive+AdaptStandbyMax=513).
	rg.SetSelfHeal(func(_ []string) {}, 513)
	rg.mu.Lock()
	got := rg.selfHealTarget
	rg.mu.Unlock()
	require.Equal(t, 513, got, "precondition: dial-time self-heal target")

	// A retune lowers the live pool to 9; the adaptive hook reports it. One tick
	// of the rotation loop must re-cap the running group's self-heal target.
	rg.mu.Lock()
	rg.rotationHook = fakeSelfHealHook{target: 9, ok: true}
	rg.mu.Unlock()
	rg.rotationServiceFn(0)
	rg.mu.Lock()
	got = rg.selfHealTarget
	rg.mu.Unlock()
	require.Equal(t, 9, got, "self-heal target must track the live runtime tunable pushed by the hook")

	// A non-adaptive hook (ok=false) must leave the dial-time target untouched —
	// its policy width is a fixed dial-time figure, not a live tunable.
	rg.mu.Lock()
	rg.rotationHook = fakeSelfHealHook{target: 0, ok: false}
	rg.mu.Unlock()
	rg.rotationServiceFn(0)
	rg.mu.Lock()
	got = rg.selfHealTarget
	rg.mu.Unlock()
	require.Equal(t, 9, got, "a non-adaptive hook (ok=false) must not overwrite the self-heal target")
}
