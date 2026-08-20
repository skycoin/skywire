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
