package presets

import (
	"context"
	"reflect"
	"testing"

	"github.com/skycoin/skywire/pkg/router/policy"
	"github.com/skycoin/skywire/pkg/router/policy/preset"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
)

// TestTickPerRouteGroup_Wazero: one app's policy module is ticked by every
// route group the app holds. Two groups sharing a transport_id — group A
// with a bad primary, group B healthy — must each get the controller their own history
// calls for. The committed bundle must key its state by the route group the
// host names (ctx.local_port) and so agree with per-group native Engines; a
// single shared Engine, the old behavior, decides differently on the same
// sequence, which is what makes this a regression test for the sharing.
func TestTickPerRouteGroup_Wazero(t *testing.T) {
	pair := func(tid0 string, lat0 int, tid1 string, lat1 int) []policy.LegInfo {
		return []policy.LegInfo{
			{Index: 0, TransportID: tid0, Kind: "swtr", LatencyMs: lat0, Alive: true},
			{Index: 1, TransportID: tid1, Kind: "swtr", LatencyMs: lat1, Alive: true},
		}
	}
	ctxA := policy.RoutingContext{App: "skysocks-client", PeerPK: "02aa", Port: 3, LocalPort: 49283}
	ctxB := policy.RoutingContext{App: "skysocks-client", PeerPK: "02aa", Port: 3, LocalPort: 49284}

	type step struct {
		ctx  policy.RoutingContext
		legs []policy.LegInfo
	}
	var steps []step
	// Group A: a primary far over the latency ceiling beside a healthy leg —
	// per group it swaps the primary out after adaptHysteresis ticks. Group B:
	// healthy. Under one shared Engine, B's ticks reset A's bad-primary streak
	// and the swap never comes.
	for i := 0; i < 6; i++ {
		steps = append(steps, step{ctxA, pair("a0", 3000, "a1", 40)}, step{ctxB, pair("b0", 40, "b1", 45)})
	}

	l, err := policywasm.NewLoaderBytes("adaptive", Bundle(), policywasm.WithPreset("adaptive"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	var perGroup preset.Engines
	shared := preset.New()
	differs := false
	for i, s := range steps {
		wz, err := l.OnTick(context.Background(), s.ctx, s.legs)
		if err != nil {
			t.Fatalf("step %d: wazero OnTick: %v", i, err)
		}
		pl := toPresetLegs(s.legs)
		want := perGroup.For(preset.GroupKey(s.ctx.PeerPK, s.ctx.Port, s.ctx.LocalPort)).OnTick("adaptive", pl)
		if got, w := actFromPolicy(wz), actFromPreset(want); !reflect.DeepEqual(got, w) {
			t.Fatalf("step %d (local port %d): wazero %+v, per-group native %+v", i, s.ctx.LocalPort, got, w)
		}
		if old := shared.OnTick("adaptive", toPresetLegs(s.legs)); !reflect.DeepEqual(actFromPreset(old), actFromPreset(want)) {
			differs = true
		}
	}
	if !differs {
		t.Fatal("a shared Engine decided identically on this sequence, so it cannot tell per-group state from shared state")
	}
}
