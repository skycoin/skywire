// rotating-bw.wasm — bandwidth-spreading rotation policy.
// Establishes mux=4 with multi-hop, then rotates one leg per
// tick interval so the active leg set drifts across the
// eligible-peer set over time. Demonstrates the on_tick hook
// added in pkg/router/policy/wasm.
//
// Build with TinyGo:
//
//	cd docs/examples/routing-policies/wasm/rotating-bw
//	tinygo build -target=wasi -no-debug -opt=2 -o rotating-bw.wasm .
//
// Install:
//
//	sudo install -m 0644 rotating-bw.wasm /etc/skywire/policies/
//	# in skywire.json: "routing": {
//	#   "policy_per_dial": "@/etc/skywire/policies/rotating-bw.wasm"
//	# }
//	# backend dispatched by file extension.
package main

import (
	"encoding/json"
	"unsafe"
)

// Wire types — kept in sync with pkg/router/policy/wasm/abi.go.

type candidateWire struct {
	Hops           []string `json:"hops"`
	HopsGeo        []string `json:"hops_geo"`
	EstLatencyMs   int      `json:"est_latency_ms"`
	TransportKinds []string `json:"transport_kinds"`
}

type routingContextWire struct {
	App               string            `json:"app"`
	PeerPK            string            `json:"peer_pk"`
	Port              uint16            `json:"port"`
	NowUnixNano       int64             `json:"now_unix_nano"`
	CLIOverrides      map[string]string `json:"cli_overrides"`
	IsDirectDial      bool              `json:"is_direct_dial"`
	TransportKind     string            `json:"transport_kind"`
	ReverseCandidates []candidateWire   `json:"reverse_candidates"`
}

type decideInputWire struct {
	Ctx        routingContextWire `json:"ctx"`
	Candidates []candidateWire    `json:"candidates"`
}

type legInfoWire struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	LatencyMs int    `json:"latency_ms"`
	Alive     bool   `json:"alive"`
	Standby   bool   `json:"standby,omitempty"`
}

type tickInputWire struct {
	Ctx  routingContextWire `json:"ctx"`
	Legs []legInfoWire      `json:"legs"`
}

type routeSpecWire struct {
	Mux                     int    `json:"mux,omitempty"`
	MinHops                 int    `json:"min_hops,omitempty"`
	RotationIntervalSeconds int    `json:"rotation_interval_seconds,omitempty"`
	Distribution            string `json:"distribution,omitempty"`
}

type rotationActionWire struct {
	DropLegs           []int    `json:"drop_legs,omitempty"`
	AddLeg             bool     `json:"add_leg,omitempty"`
	ExcludeHops        []string `json:"exclude_hops,omitempty"`
	DemoteToStandby    []int    `json:"demote_to_standby,omitempty"`
	PromoteFromStandby []int    `json:"promote_from_standby,omitempty"`
}

//export alloc
func alloc(size uint32) uint32 {
	buf := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buf[0]))) //nolint:gosec
}

//export free
func free(_, _ uint32) {}

func readInput(ptr, length uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(length))
}

func writeOutput(data []byte) uint64 {
	if len(data) == 0 {
		return 0
	}
	ptr := alloc(uint32(len(data)))
	dst := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(data))
	copy(dst, data)
	return uint64(ptr) | (uint64(len(data)) << 32)
}

//export decide_route
func decideRoute(inPtr, inLen uint32) uint64 {
	var input decideInputWire
	if err := json.Unmarshal(readInput(inPtr, inLen), &input); err != nil {
		return 0
	}
	// rotating-bw is OPT-IN per app — apply it to WHATEVER app/session it is
	// active for, not just the built-in binary names. A named proxy session
	// dials under its SESSION name (e.g. "g8"), not "skysocks-client", so gating
	// on the binary name made the policy a NO-OP for custom-named sessions.
	// Skip only latency-sensitive apps. min_hops=2 keeps the dial on the overlay
	// (avoid_direct) so the mux/distribution isn't dropped by the direct path.
	var spec routeSpecWire
	switch input.Ctx.App {
	case "skychat", "skychat-client":
		spec = routeSpecWire{Mux: 1}
	default:
		spec = routeSpecWire{
			Mux:                     targetMux + 1,
			MinHops:                 2,
			RotationIntervalSeconds: 90,
			Distribution:            "round-robin",
		}
	}
	out, err := json.Marshal(spec)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// targetMux is the mux size the policy aims to maintain. Kept in
// sync with the Mux value returned from decideRoute so on_tick
// can reason about "are we at target?"
const targetMux = 4

// reliableMuxKind reports whether a transport type is reliable enough to ANCHOR
// a mux active set. stcpr/sudph/squicr/stcp are direct and well-behaved for
// sustained multiplexed throughput; webrtc/ws/wt and the dmsg relay are not —
// webrtc especially drops under load, so a mux made entirely of webrtc legs
// collapses. They are still usable as bonus capacity (promoted for throughput),
// just never the anchor.
func reliableMuxKind(kind string) bool {
	switch kind {
	case "stcpr", "sudph", "squicr", "stcp":
		return true
	}
	return false
}

//export on_tick
func onTick(inPtr, inLen uint32) uint64 {
	var input tickInputWire
	if err := json.Unmarshal(readInput(inPtr, inLen), &input); err != nil {
		return 0
	}
	// Manage the mux by STANDBY, never by dropping. Legs, once established, are
	// never torn down — the tick only flips each leg's standby flag (no teardown,
	// no setup round-trip, in-flight bytes drain). Keep ~targetMux legs ACTIVE
	// anchored on reliable transport types and PARK the rest on warm standby;
	// promote a webrtc/other leg only when the aggregate throughput needs it (not
	// enough reliable legs to reach targetMux). An all-reliable anchor keeps the
	// group alive even as fragile bonus legs come and go. Decisions are on
	// measurement: leg Kind + standby come from host per-leg telemetry.
	var relAct, relSb, fragAct, fragSb []int
	for _, l := range input.Legs {
		if !l.Alive {
			continue
		}
		rel := reliableMuxKind(l.Kind)
		switch {
		case l.Standby && rel:
			relSb = append(relSb, l.Index)
		case l.Standby:
			fragSb = append(fragSb, l.Index)
		case rel:
			relAct = append(relAct, l.Index)
		default:
			fragAct = append(fragAct, l.Index)
		}
	}
	active := len(relAct) + len(fragAct)
	alive := active + len(relSb) + len(fragSb)
	if alive == 0 {
		return 0
	}
	lo := func(s []int) int {
		m := s[0]
		for _, v := range s {
			if v < m {
				m = v
			}
		}
		return m
	}
	hi := func(s []int) int {
		m := s[0]
		for _, v := range s {
			if v > m {
				m = v
			}
		}
		return m
	}
	// NEVER emit AddLeg/DropLegs — growth + death-replacement are the route
	// group's self-heal job (aliveLegCount counts standby, so parking never
	// re-grows). on_tick only picks ACTIVE vs parked — pure standby flips, no
	// churn. Prefer a reliable-only active set so the stream never rides a
	// flapping webrtc leg.
	var action rotationActionWire
	switch {
	case len(relAct) >= 1 && len(fragAct) > 0:
		action = rotationActionWire{DemoteToStandby: []int{hi(fragAct)}}
	case len(fragAct) > 0 && len(relSb) > 0:
		action = rotationActionWire{PromoteFromStandby: []int{lo(relSb)}, DemoteToStandby: []int{hi(fragAct)}}
	case len(relAct) < targetMux && len(relSb) > 0:
		action = rotationActionWire{PromoteFromStandby: []int{lo(relSb)}}
	case len(relAct) == 0 && len(fragAct) == 0 && len(fragSb) > 0:
		action = rotationActionWire{PromoteFromStandby: []int{lo(fragSb)}}
	case len(relAct) > targetMux:
		action = rotationActionWire{DemoteToStandby: []int{hi(relAct)}}
	case len(fragAct) == 0 && len(relSb) > 0 && len(relAct) > 0:
		action = rotationActionWire{PromoteFromStandby: []int{lo(relSb)}, DemoteToStandby: []int{lo(relAct)}}
	default:
		return 0
	}
	out, err := json.Marshal(action)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

func main() {}
