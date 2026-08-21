package preset

import (
	"reflect"
	"testing"
)

func TestDecide_ShapePresets(t *testing.T) {
	cases := []struct {
		name string
		ctx  Context
		want Spec
	}{
		{"app-mux/vpn", Context{App: "vpn-client"}, Spec{Mux: 4, MinHops: 2}},
		{"app-mux/skychat", Context{App: "skychat"}, Spec{Mux: 1}},
		{"app-mux/other", Context{App: "x"}, Spec{}},
		{"rotating-bw", Context{App: "skysocks-client"}, Spec{Mux: 5, MinHops: 2, RotationIntervalSeconds: 90, Distribution: "round-robin"}},
		{"rotating-bw/chat", Context{App: "skychat"}, Spec{Mux: 1}},
		{"latency-adaptive", Context{App: "vpn-client"}, Spec{Mux: 5, MinHops: 2, RotationIntervalSeconds: 30, Distribution: "auto"}},
		{"elastic-mux", Context{App: "skynet-client"}, Spec{Mux: 2, MinHops: 2, RotationIntervalSeconds: 20, Distribution: "auto"}},
		{"probe-and-prune", Context{App: "skynet-client"}, Spec{Mux: 3, MinHops: 2, RotationIntervalSeconds: 30, Distribution: "auto"}},
		{"adaptive", Context{App: "vpn-client"}, Spec{Mux: 1, RotationIntervalSeconds: 20, Distribution: "auto"}},
	}
	for _, tc := range cases {
		presetName, _, _ := splitName(tc.name)
		got := Decide(presetName, tc.ctx, nil)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: Decide=%+v want %+v", tc.name, got, tc.want)
		}
	}
}

// splitName maps a "preset/variant" test label to the preset name.
func splitName(label string) (string, string, bool) {
	for i := 0; i < len(label); i++ {
		if label[i] == '/' {
			return label[:i], label[i+1:], true
		}
	}
	return label, "", false
}

func TestDecide_GeoAvoid(t *testing.T) {
	cands := []Candidate{
		{Hops: []string{"a"}, HopsGeo: []string{"US"}, EstLatencyMs: 10},
		{Hops: []string{"b"}, HopsGeo: []string{"DE"}, EstLatencyMs: 50},
	}
	got := Decide("geo-avoid", Context{CLIOverrides: map[string]string{"avoid_geo": "US"}}, cands)
	if got.Chosen == nil || got.Chosen.HopsGeo[0] != "DE" {
		t.Fatalf("geo-avoid should pick the clean DE route; got %+v", got.Chosen)
	}
	// No clean candidate → defer.
	got = Decide("geo-avoid", Context{CLIOverrides: map[string]string{"avoid_geo": "US,DE"}}, cands)
	if got.Chosen != nil {
		t.Fatalf("geo-avoid should defer when all violate; got %+v", got.Chosen)
	}
}

// TestDecide_Adaptive covers the transport-diversity refinement folded into the
// adaptive composite default: it seeds the most transport-diverse forward route
// when real kind metadata is present, but falls back to the router's pick (nil
// Chosen) with no candidates or only empty/unknown kinds — the wasm/NopProvider
// no-regression path. The seed shape (mux/rotation/distribution) and MinHops=0
// are unchanged, and non-client apps stay on the empty spec.
func TestDecide_Adaptive(t *testing.T) {
	diverse := []Candidate{
		{Hops: []string{"a"}, EstLatencyMs: 10, TransportKinds: []string{"stcpr"}},
		{Hops: []string{"b"}, EstLatencyMs: 50, TransportKinds: []string{"stcpr", "sudph"}},
		{Hops: []string{"c"}, EstLatencyMs: 30, TransportKinds: []string{"sudph"}},
	}

	// Real metadata present → pick the max-distinct-kinds candidate (b), and keep
	// the seed shape otherwise (mux=1, rotation=20, auto, MinHops=0).
	got := Decide("adaptive", Context{App: "skysocks-client"}, diverse)
	if got.Chosen == nil || got.Chosen.Hops[0] != "b" {
		t.Fatalf("adaptive should seed the most transport-diverse route (b); got %+v", got.Chosen)
	}
	if got.Mux != 1 || got.RotationIntervalSeconds != 20 || got.Distribution != "auto" || got.MinHops != 0 {
		t.Errorf("adaptive seed shape changed: %+v", got)
	}

	// Tiebreak on latency among equal diversity: a (lat 10) beats c (lat 30).
	oneKind := []Candidate{
		{Hops: []string{"c"}, EstLatencyMs: 30, TransportKinds: []string{"sudph"}},
		{Hops: []string{"a"}, EstLatencyMs: 10, TransportKinds: []string{"stcpr"}},
	}
	if got := Decide("adaptive", Context{App: "vpn-client"}, oneKind); got.Chosen == nil || got.Chosen.Hops[0] != "a" {
		t.Fatalf("adaptive tie should break to lowest latency (a); got %+v", got.Chosen)
	}

	// No candidates → fall back to the router's pick (nil Chosen), seed unchanged.
	if got := Decide("adaptive", Context{App: "skynet-client"}, nil); got.Chosen != nil {
		t.Fatalf("adaptive with no candidates must not force a route; got %+v", got.Chosen)
	}

	// Candidates present but every kind empty/unknown (wasm/NopProvider) → no
	// refinement, Chosen stays nil (no regression).
	unknown := []Candidate{
		{Hops: []string{"a"}, EstLatencyMs: 10, TransportKinds: []string{""}},
		{Hops: []string{"b"}, EstLatencyMs: 50},
	}
	if got := Decide("adaptive", Context{App: "skysocks-client"}, unknown); got.Chosen != nil {
		t.Fatalf("adaptive must not refine on empty/unknown kinds; got %+v", got.Chosen)
	}

	// Non-client app → empty spec regardless of candidates.
	if got := Decide("adaptive", Context{App: "skychat"}, diverse); !reflect.DeepEqual(got, Spec{}) {
		t.Errorf("adaptive for a non-client app should be the empty spec; got %+v", got)
	}
}

func TestDecide_TimeOfDay(t *testing.T) {
	const h = int64(3600) * 1_000_000_000
	if got := Decide("time-of-day", Context{NowUnixNano: 11 * h}, nil); got.Mux != 1 {
		t.Errorf("business hours should be lean (mux 1); got %+v", got)
	}
	off := Decide("time-of-day", Context{NowUnixNano: 3 * h}, nil)
	if off.Mux != 4 || off.Distribution != "round-robin" || off.RotationIntervalSeconds == 0 {
		t.Errorf("off-hours should be a wide rotating mux; got %+v", off)
	}
}

func TestEngine_OnTick_UnknownIsNoop(t *testing.T) {
	e := New()
	if got := e.OnTick("app-mux", []LegInfo{{Index: 0, Alive: true}}); !reflect.DeepEqual(got, RotationAction{}) {
		t.Errorf("app-mux has no tick logic; want no-op, got %+v", got)
	}
}

func TestEngine_OnTick_RotatingBWParksFragile(t *testing.T) {
	e := New()
	// A reliable active leg + a fragile (webrtc) active leg → park the fragile.
	legs := []LegInfo{
		{Index: 0, TransportID: "t0", Kind: "stcpr", Alive: true},
		{Index: 1, TransportID: "t1", Kind: "webrtc", Alive: true},
	}
	got := e.OnTick("rotating-bw", legs)
	want := RotationAction{DemoteToStandby: []int{1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rotating-bw should park the fragile active leg; got %+v want %+v", got, want)
	}
}
