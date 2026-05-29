// Package wasm pkg/router/policy/wasm/abi.go — JSON wire format
// for the WASM-host ABI. Operators writing TinyGo / Go-WASM
// policies get the same input shape (RoutingContext + candidates
// list) and return the same RouteSpec; the host side serializes
// to/from JSON across the linear-memory boundary.
//
// The shapes are intentionally NOT the policy package's
// Candidate / RouteSpec / RoutingContext directly — those
// types pull in deps (cipher / starlark / etc.) that don't
// belong in the WASM-guest TinyGo build. The wire types here
// are flat structs with primitives + slices that marshal
// cleanly under encoding/json on both sides.
package wasm

// LegInfoWire mirrors policy.LegInfo as a JSON wire type.
type LegInfoWire struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	LatencyMs int    `json:"latency_ms"`
	Alive     bool   `json:"alive"`
}

// LegChangeWire mirrors policy.LegChange as a JSON wire type.
type LegChangeWire struct {
	Event    string `json:"event"`
	LegIndex int    `json:"leg_index"`
}

// CandidateWire mirrors policy.Candidate as a JSON wire type.
// Kept in sync with the Starlark Candidate fields so a script
// ported from .star to .wasm sees the same data.
type CandidateWire struct {
	Hops           []string `json:"hops"`
	HopsGeo        []string `json:"hops_geo"`
	EstLatencyMs   int      `json:"est_latency_ms"`
	TransportKinds []string `json:"transport_kinds"`
}

// RoutingContextWire mirrors policy.RoutingContext as a JSON
// wire type. NowUnixNano is the unix-nano timestamp instead of
// time.Time so the JSON round-trip is cheap and the guest can
// reconstruct time.Time from time.Unix(0, NowUnixNano).
type RoutingContextWire struct {
	App               string            `json:"app"`
	PeerPK            string            `json:"peer_pk"`
	Port              uint16            `json:"port"`
	NowUnixNano       int64             `json:"now_unix_nano"`
	CLIOverrides      map[string]string `json:"cli_overrides"`
	IsDirectDial      bool              `json:"is_direct_dial"`
	TransportKind     string            `json:"transport_kind"`
	ReverseCandidates []CandidateWire   `json:"reverse_candidates"`
}

// DecideInputWire is the JSON envelope the host passes into the
// guest's decide_route export: the context + the forward
// candidates list (separate from ctx.reverse_candidates so the
// guest's function signature matches Starlark's
// decide_route(ctx, candidates)).
type DecideInputWire struct {
	Ctx        RoutingContextWire `json:"ctx"`
	Candidates []CandidateWire    `json:"candidates"`
}

// LegChangeInputWire is the JSON envelope the host passes into
// the guest's on_leg_change export. Same ctx shape; the legs +
// change carry the post-setup leg event.
type LegChangeInputWire struct {
	Ctx    RoutingContextWire `json:"ctx"`
	Legs   []LegInfoWire      `json:"legs"`
	Change LegChangeWire      `json:"change"`
}

// RouteSpecWire mirrors policy.RouteSpec as a JSON wire type.
// Pointers are encoded as nil-or-struct; numeric zero defaults
// match the policy.RouteSpec semantics (Mux=0 means "no
// override", etc.).
type RouteSpecWire struct {
	Chosen                  *CandidateWire `json:"chosen,omitempty"`
	ReverseChosen           *CandidateWire `json:"reverse_chosen,omitempty"`
	Mux                     int            `json:"mux,omitempty"`
	ForwardMux              int            `json:"forward_mux,omitempty"`
	ReverseMux              int            `json:"reverse_mux,omitempty"`
	MinHops                 int            `json:"min_hops,omitempty"`
	ForwardMinHops          int            `json:"forward_min_hops,omitempty"`
	ReverseMinHops          int            `json:"reverse_min_hops,omitempty"`
	Fallback                string         `json:"fallback,omitempty"`
	Distribution            string         `json:"distribution,omitempty"`
	RotationIntervalSeconds int            `json:"rotation_interval_seconds,omitempty"`
}

// TickInputWire is the JSON envelope the host passes into the
// guest's on_tick export. Same ctx shape as DecideInputWire; the
// legs carry the current leg snapshot at tick time.
type TickInputWire struct {
	Ctx  RoutingContextWire `json:"ctx"`
	Legs []LegInfoWire      `json:"legs"`
}

// RotationActionWire mirrors policy.RotationAction as a JSON wire
// type. Returned by the guest's on_tick export. Zero-value
// (no_drops + add_leg=false) is the "no-op this tick" signal.
//
// Indices in DropLegs reference the legs slice the host passed
// in — they are not stable across ticks. Policies must re-read
// the legs slice each tick.
type RotationActionWire struct {
	DropLegs    []int    `json:"drop_legs,omitempty"`
	AddLeg      bool     `json:"add_leg,omitempty"`
	ExcludeHops []string `json:"exclude_hops,omitempty"`
}
