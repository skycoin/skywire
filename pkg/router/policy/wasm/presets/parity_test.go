package presets

// parity_test.go is the gate-1 "byte-identical decisions" property extended to
// the NATIVE-Go preset path. The compiled bundle.wasm (run here via wazero) and
// the pure-Go pkg/router/policy/preset package are two compilations of ONE
// source of truth; the wasm-visor cannot host wazero (wasm-in-wasm) and so runs
// the native path instead. These tests assert the two agree — field-for-field
// on every decide, and step-for-step over a multi-tick on_tick sequence (the
// stateful controllers) — for representative inputs of every preset. If they
// ever diverge, the bundle was rebuilt from a different revision of the preset
// package than the one compiled into the visor, and this fails loudly.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/router/policy"
	"github.com/skycoin/skywire/pkg/router/policy/preset"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
)

// unixNano builds a UTC time.Time from a unix-nanosecond value so time-of-day
// decide cases feed both paths the same clock.
func unixNano(ns int64) time.Time { return time.Unix(0, ns).UTC() }

// normSpec is the comparable projection of a route-spec, built from either the
// wazero policy.RouteSpec or the native preset.Spec so the two can be compared
// directly (slices normalized nil==empty via the candidate projection).
type normSpec struct {
	Chosen, ReverseChosen          *normCand
	Mux, ForwardMux, ReverseMux    int
	MinHops                        int
	ForwardMinHops, ReverseMinHops int
	Fallback, Distribution         string
	RotationIntervalSeconds        int
}

type normCand struct {
	Hops           []string
	HopsGeo        []string
	EstLatencyMs   int
	TransportKinds []string
}

func normCandFromPolicy(c *policy.Candidate) *normCand {
	if c == nil {
		return nil
	}
	return &normCand{Hops: nz(c.Hops), HopsGeo: nz(c.HopsGeo), EstLatencyMs: c.EstLatencyMs, TransportKinds: nz(c.TransportKinds)}
}

func normCandFromPreset(c *preset.Candidate) *normCand {
	if c == nil {
		return nil
	}
	return &normCand{Hops: nz(c.Hops), HopsGeo: nz(c.HopsGeo), EstLatencyMs: c.EstLatencyMs, TransportKinds: nz(c.TransportKinds)}
}

func specFromPolicy(s policy.RouteSpec) normSpec {
	return normSpec{
		Chosen: normCandFromPolicy(s.Chosen), ReverseChosen: normCandFromPolicy(s.ReverseChosen),
		Mux: s.Mux, ForwardMux: s.ForwardMux, ReverseMux: s.ReverseMux,
		MinHops: s.MinHops, ForwardMinHops: s.ForwardMinHops, ReverseMinHops: s.ReverseMinHops,
		Fallback: s.Fallback, Distribution: s.Distribution, RotationIntervalSeconds: s.RotationIntervalSeconds,
	}
}

func specFromPreset(s preset.Spec) normSpec {
	return normSpec{
		Chosen: normCandFromPreset(s.Chosen), ReverseChosen: normCandFromPreset(s.ReverseChosen),
		Mux: s.Mux, ForwardMux: s.ForwardMux, ReverseMux: s.ReverseMux,
		MinHops: s.MinHops, ForwardMinHops: s.ForwardMinHops, ReverseMinHops: s.ReverseMinHops,
		Fallback: s.Fallback, Distribution: s.Distribution, RotationIntervalSeconds: s.RotationIntervalSeconds,
	}
}

// normAct is the comparable projection of a rotation action (slices normalized
// nil==empty so the wazero JSON round-trip and the native path compare equal).
type normAct struct {
	DropLegs           []int
	AddLeg             bool
	ExcludeHops        []string
	DemoteToStandby    []int
	PromoteFromStandby []int
}

func actFromPolicy(a policy.RotationAction) normAct {
	return normAct{DropLegs: nzi(a.DropLegs), AddLeg: a.AddLeg, ExcludeHops: nz(a.ExcludeHops), DemoteToStandby: nzi(a.DemoteToStandby), PromoteFromStandby: nzi(a.PromoteFromStandby)}
}

func actFromPreset(a preset.RotationAction) normAct {
	return normAct{DropLegs: nzi(a.DropLegs), AddLeg: a.AddLeg, ExcludeHops: nz(a.ExcludeHops), DemoteToStandby: nzi(a.DemoteToStandby), PromoteFromStandby: nzi(a.PromoteFromStandby)}
}

func nz(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func nzi(s []int) []int {
	if len(s) == 0 {
		return nil
	}
	return s
}

// decideCase is one representative decide input shared by both paths.
type decideCase struct {
	name   string
	preset string
	rctx   policy.RoutingContext
	cands  []policy.Candidate
}

func toPresetCtx(r policy.RoutingContext) preset.Context {
	return preset.Context{
		App: r.App, PeerPK: r.PeerPK, Port: r.Port, NowUnixNano: r.Now.UnixNano(),
		CLIOverrides: r.CLIOverrides, IsDirectDial: r.IsDirectDial, TransportKind: r.TransportKind,
		ReverseCandidates: toPresetCands(r.ReverseCandidates),
	}
}

func toPresetCands(cs []policy.Candidate) []preset.Candidate {
	if cs == nil {
		return nil
	}
	out := make([]preset.Candidate, len(cs))
	for i, c := range cs {
		out[i] = preset.Candidate{Hops: c.Hops, HopsGeo: c.HopsGeo, EstLatencyMs: c.EstLatencyMs, TransportKinds: c.TransportKinds}
	}
	return out
}

// TestDecideParity_NativeMatchesWazero asserts preset.Decide == the wazero
// bundle's Decide for representative inputs of every preset.
func TestDecideParity_NativeMatchesWazero(t *testing.T) {
	cands := []policy.Candidate{
		{Hops: []string{"a"}, HopsGeo: []string{"US"}, EstLatencyMs: 10, TransportKinds: []string{"stcpr"}},
		{Hops: []string{"b"}, HopsGeo: []string{"DE"}, EstLatencyMs: 50, TransportKinds: []string{"stcpr", "sudph"}},
		{Hops: []string{"trustA", "trustB"}, HopsGeo: []string{"FR", "NL"}, EstLatencyMs: 30, TransportKinds: []string{"sudph"}},
	}
	const oneHour = int64(3600) * 1_000_000_000
	cases := []decideCase{
		{"app-mux/vpn", "app-mux", policy.RoutingContext{App: "vpn-client"}, nil},
		{"app-mux/skychat", "app-mux", policy.RoutingContext{App: "skychat"}, nil},
		{"app-mux/other", "app-mux", policy.RoutingContext{App: "unknown"}, nil},
		{"rotating-bw/proxy", "rotating-bw", policy.RoutingContext{App: "skysocks-client"}, nil},
		{"rotating-bw/chat", "rotating-bw", policy.RoutingContext{App: "skychat"}, nil},
		{"latency-adaptive", "latency-adaptive", policy.RoutingContext{App: "vpn-client"}, nil},
		{"latency-adaptive/other", "latency-adaptive", policy.RoutingContext{App: "skychat"}, nil},
		{"elastic-mux", "elastic-mux", policy.RoutingContext{App: "skysocks-client"}, nil},
		{"probe-and-prune", "probe-and-prune", policy.RoutingContext{App: "skynet-client"}, nil},
		{"coupled", "coupled", policy.RoutingContext{App: "skysocks-client"}, nil},
		{"coupled/chat", "coupled", policy.RoutingContext{App: "skychat"}, nil},
		{"adaptive", "adaptive", policy.RoutingContext{App: "vpn-client"}, nil},
		// adaptive with real transport-kind metadata: both paths must seed the
		// most transport-diverse forward candidate (cand b, 2 distinct kinds).
		{"adaptive/diverse", "adaptive", policy.RoutingContext{App: "skysocks-client"}, cands},
		{"geo-avoid", "geo-avoid", policy.RoutingContext{App: "skysocks-client", CLIOverrides: map[string]string{"avoid_geo": "US"}}, cands},
		{"geo-avoid/noclean", "geo-avoid", policy.RoutingContext{App: "skysocks-client", CLIOverrides: map[string]string{"avoid_geo": "US,DE,FR"}}, cands},
		{"transport-diverse", "transport-diverse", policy.RoutingContext{App: "skysocks-client"}, cands},
		{"trust-tiered", "trust-tiered", policy.RoutingContext{App: "skysocks-client", CLIOverrides: map[string]string{"trusted_pks": "trustA,trustB"}}, cands},
		{"time-of-day/biz", "time-of-day", policy.RoutingContext{App: "skysocks-client", Now: unixNano(11 * oneHour)}, nil},
		{"time-of-day/off", "time-of-day", policy.RoutingContext{App: "skysocks-client", Now: unixNano(3 * oneHour)}, nil},
		{"time-of-day/window", "time-of-day", policy.RoutingContext{App: "skysocks-client", Now: unixNano(23 * oneHour), CLIOverrides: map[string]string{"business_hours": "22-6"}}, nil},
		{"ledbat", "ledbat", policy.RoutingContext{App: "skysocks-client"}, nil},
		{"ledbat/chat", "ledbat", policy.RoutingContext{App: "skychat"}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := policywasm.NewLoaderBytes(tc.preset, Bundle(), policywasm.WithPreset(tc.preset))
			if err != nil {
				t.Fatalf("NewLoaderBytes: %v", err)
			}
			defer l.Close() //nolint:errcheck

			wz, err := l.Decide(context.Background(), tc.rctx, tc.cands)
			if err != nil {
				t.Fatalf("wazero Decide: %v", err)
			}
			nat := preset.Decide(tc.preset, toPresetCtx(tc.rctx), toPresetCands(tc.cands))

			if got, want := specFromPreset(nat), specFromPolicy(wz); !reflect.DeepEqual(got, want) {
				t.Errorf("decide parity mismatch for %s:\n native: %+v\n wazero: %+v", tc.preset, got, want)
			}
		})
	}
}

// tickCase is a preset plus a fixed sequence of leg snapshots fed to both the
// wazero on_tick and the native Engine.OnTick tick-by-tick.
type tickCase struct {
	name   string
	preset string
	steps  [][]policy.LegInfo
}

func toPresetLegs(ls []policy.LegInfo) []preset.LegInfo {
	if ls == nil {
		return nil
	}
	out := make([]preset.LegInfo, len(ls))
	for i, l := range ls {
		out[i] = preset.LegInfo{
			Index: l.Index, Kind: l.Kind, TransportID: l.TransportID, LatencyMs: l.LatencyMs,
			Alive: l.Alive, Standby: l.Standby, SentBytes: l.SentBytes, RecvBytes: l.RecvBytes,
			Retransmits: l.Retransmits, Hops: l.Hops,
		}
	}
	return out
}

// TestTickParity_NativeMatchesWazero drives the SAME leg-snapshot sequence
// through the wazero on_tick and the native Engine.OnTick and asserts each
// step's RotationAction is identical — proving the stateful controllers
// (EWMA smoothing, AIMD peak/idle counters, probe state machine) evolve
// byte-identically across the two compilations.
func TestTickParity_NativeMatchesWazero(t *testing.T) {
	leg := func(idx int, tid, kind string, lat int, alive, standby bool, recv uint64, hops ...string) policy.LegInfo {
		return policy.LegInfo{Index: idx, TransportID: tid, Kind: kind, LatencyMs: lat, Alive: alive, Standby: standby, RecvBytes: recv, Hops: hops}
	}

	// rotating-bw: a reliable + fragile active mix, converging toward reliable-only.
	rbw := [][]policy.LegInfo{
		{leg(0, "t0", "stcpr", 50, true, false, 0), leg(1, "t1", "webrtc", 80, true, false, 0)},
		{leg(0, "t0", "stcpr", 50, true, false, 0), leg(1, "t1", "webrtc", 80, true, true, 0)},
		{leg(0, "t0", "stcpr", 50, true, false, 0)},
	}
	// latency-adaptive: an outlier leg that should eventually be swapped.
	la := [][]policy.LegInfo{
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 45, true, false, 0), leg(2, "c", "stcpr", 50, true, false, 0), leg(3, "d", "stcpr", 900, true, false, 0)},
		{leg(0, "a", "stcpr", 42, true, false, 0), leg(1, "b", "stcpr", 44, true, false, 0), leg(2, "c", "stcpr", 51, true, false, 0), leg(3, "d", "stcpr", 950, true, false, 0)},
		{leg(0, "a", "stcpr", 41, true, false, 0), leg(1, "b", "stcpr", 46, true, false, 0), leg(2, "c", "stcpr", 49, true, false, 0), leg(3, "d", "stcpr", 980, true, false, 0)},
	}
	// elastic-mux: growing then idle throughput.
	em := [][]policy.LegInfo{
		{leg(0, "a", "stcpr", 30, true, false, 1000), leg(1, "b", "stcpr", 30, true, false, 1000)},
		{leg(0, "a", "stcpr", 30, true, false, 9000), leg(1, "b", "stcpr", 30, true, false, 9000)},
		{leg(0, "a", "stcpr", 30, true, false, 20000), leg(1, "b", "stcpr", 30, true, false, 20000)},
		{leg(0, "a", "stcpr", 30, true, false, 20001), leg(1, "b", "stcpr", 30, true, false, 20001)},
		{leg(0, "a", "stcpr", 30, true, false, 20001), leg(1, "b", "stcpr", 30, true, false, 20001)},
		{leg(0, "a", "stcpr", 30, true, false, 20001), leg(1, "b", "stcpr", 30, true, false, 20001)},
	}
	// probe-and-prune: steady 3-wide established set to trigger an explore add.
	pp := [][]policy.LegInfo{
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 45, true, false, 0), leg(2, "c", "stcpr", 50, true, false, 0)},
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 45, true, false, 0), leg(2, "c", "stcpr", 50, true, false, 0), leg(3, "d", "stcpr", 30, true, false, 0)},
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 45, true, false, 0), leg(2, "c", "stcpr", 50, true, false, 0), leg(3, "d", "stcpr", 30, true, false, 0)},
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 45, true, false, 0), leg(2, "c", "stcpr", 50, true, false, 0), leg(3, "d", "stcpr", 30, true, false, 0)},
	}
	// adaptive: mixed load + latency to exercise the arbitration priority.
	ad := [][]policy.LegInfo{
		{leg(0, "a", "stcpr", 40, true, false, 1000)},
		{leg(0, "a", "stcpr", 40, true, false, 9000)},
		{leg(0, "a", "stcpr", 40, true, false, 20000), leg(1, "b", "stcpr", 45, true, false, 20000)},
		{leg(0, "a", "stcpr", 41, true, false, 30000), leg(1, "b", "stcpr", 900, true, false, 30000)},
		{leg(0, "a", "stcpr", 42, true, false, 30001), leg(1, "b", "stcpr", 950, true, false, 30001)},
		{leg(0, "a", "stcpr", 43, true, false, 30001), leg(1, "b", "stcpr", 980, true, false, 30001)},
	}

	// ledbat: leg 2's delay climbs above its base while legs 0,1 stay near theirs
	// (the scavenger backs off and parks leg 2); a final recovered snapshot with
	// parked spares exercises the re-grow path — every step must agree.
	lb := [][]policy.LegInfo{
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 40, true, false, 0), leg(2, "c", "stcpr", 40, true, false, 0)},
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 40, true, false, 0), leg(2, "c", "stcpr", 300, true, false, 0)},
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 40, true, false, 0), leg(2, "c", "stcpr", 300, true, false, 0)},
		{leg(0, "a", "stcpr", 40, true, false, 0), leg(1, "b", "stcpr", 40, true, true, 0), leg(2, "c", "stcpr", 40, true, true, 0)},
	}

	// coupled: a clean 4-wide set, then rising loss on one leg (shed), a couple of
	// cooldown ticks, then a clean below-ceiling set with a warm spare (cautious
	// promote) — exercises both coupled-decrease and coupled-increase.
	rl := func(idx int, tid string, lat int, retrans uint64) policy.LegInfo {
		return policy.LegInfo{Index: idx, TransportID: tid, Kind: "stcpr", LatencyMs: lat, Alive: true, Retransmits: retrans}
	}
	sb := func(idx int, tid string, lat int) policy.LegInfo {
		return policy.LegInfo{Index: idx, TransportID: tid, Kind: "stcpr", LatencyMs: lat, Alive: true, Standby: true}
	}
	cpl := [][]policy.LegInfo{
		{rl(0, "a", 50, 0), rl(1, "b", 50, 0), rl(2, "c", 50, 0), rl(3, "d", 50, 0)},
		{rl(0, "a", 50, 0), rl(1, "b", 50, 0), rl(2, "c", 50, 500), rl(3, "d", 50, 0)},
		{rl(0, "a", 50, 0), rl(1, "b", 50, 0), rl(3, "d", 50, 0), sb(2, "c", 50)},
		{rl(0, "a", 50, 0), rl(1, "b", 50, 0), rl(3, "d", 50, 0), sb(2, "c", 50)},
		{rl(0, "a", 50, 0), rl(1, "b", 50, 0), rl(3, "d", 50, 0), sb(2, "c", 50)},
		{rl(0, "a", 50, 0), rl(1, "b", 50, 0), rl(3, "d", 50, 0), sb(2, "c", 50)},
	}

	cases := []tickCase{
		{"rotating-bw", "rotating-bw", rbw},
		{"latency-adaptive", "latency-adaptive", la},
		{"elastic-mux", "elastic-mux", em},
		{"probe-and-prune", "probe-and-prune", pp},
		{"coupled", "coupled", cpl},
		{"adaptive", "adaptive", ad},
		{"ledbat", "ledbat", lb},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := policywasm.NewLoaderBytes(tc.preset, Bundle(), policywasm.WithPreset(tc.preset))
			if err != nil {
				t.Fatalf("NewLoaderBytes: %v", err)
			}
			defer l.Close() //nolint:errcheck
			eng := preset.New()

			for i, legs := range tc.steps {
				wz, err := l.OnTick(context.Background(), policy.RoutingContext{App: "skysocks-client"}, legs)
				if err != nil {
					t.Fatalf("wazero OnTick step %d: %v", i, err)
				}
				nat := eng.OnTick(tc.preset, toPresetLegs(legs))
				if got, want := actFromPreset(nat), actFromPolicy(wz); !reflect.DeepEqual(got, want) {
					t.Errorf("tick parity mismatch %s step %d:\n native: %+v\n wazero: %+v", tc.preset, i, got, want)
				}
			}
		})
	}
}
