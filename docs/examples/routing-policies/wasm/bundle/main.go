// bundle.wasm — the COMBINED routing-policy bundle (#3942).
//
// A single TinyGo module that carries EVERY compiled preset and
// dispatches by name at call time, instead of shipping one
// multi-hundred-KB module per preset (each of which re-embeds the whole
// TinyGo runtime). The host stamps the active preset name into the
// decide/tick input wire (ctx.preset); this guest switches on it. The
// runtime is paid once; each extra preset adds only its own few KB of
// logic.
//
// This is the module embedded at pkg/router/policy/wasm/presets/
// bundle.wasm and selected by config as "preset:<name>" (see
// pkg/visor/policy_loader.go). The per-preset standalone examples next
// to this dir (app-mux/, rotating-bw/) stay as pedagogical single-preset
// modules; this one is their union.
//
// Build with TinyGo:
//
//	cd docs/examples/routing-policies/wasm/bundle
//	tinygo build -target=wasi -no-debug -opt=2 -o bundle.wasm .
//
// then copy it over pkg/router/policy/wasm/presets/bundle.wasm.
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

// decideInputWire carries the Preset name the host stamped so this
// bundle can dispatch. A single-preset module would omit Preset; here
// it selects which preset's logic runs.
type decideInputWire struct {
	Ctx        routingContextWire `json:"ctx"`
	Candidates []candidateWire    `json:"candidates"`
	Preset     string             `json:"preset"`
}

type legInfoWire struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	LatencyMs int    `json:"latency_ms"`
	Alive     bool   `json:"alive"`
}

type tickInputWire struct {
	Ctx    routingContextWire `json:"ctx"`
	Legs   []legInfoWire      `json:"legs"`
	Preset string             `json:"preset"`
}

type routeSpecWire struct {
	Chosen                  *candidateWire `json:"chosen,omitempty"`
	ReverseChosen           *candidateWire `json:"reverse_chosen,omitempty"`
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

type rotationActionWire struct {
	DropLegs    []int    `json:"drop_legs,omitempty"`
	AddLeg      bool     `json:"add_leg,omitempty"`
	ExcludeHops []string `json:"exclude_hops,omitempty"`
}

// Required: host-driven memory management.

//export alloc
func alloc(size uint32) uint32 {
	buf := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buf[0]))) //nolint:gosec
}

//export free
func free(_, _ uint32) {}

// readInput reads `length` bytes from linear memory at `ptr`.
func readInput(ptr, length uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(length))
}

// writeOutput allocates a buffer, copies data in, and returns the
// packed (ptr | len<<32) pair the host can decode.
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
	var spec routeSpecWire
	switch input.Preset {
	case "rotating-bw":
		spec = decideRotatingBW(input.Ctx)
	case "app-mux":
		spec = decideAppMux(input.Ctx)
	default:
		// Empty / unknown preset: fall back to the per-app app-mux
		// behavior so a bare bundle still does something sensible.
		spec = decideAppMux(input.Ctx)
	}
	out, err := json.Marshal(spec)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// decideAppMux is the verbatim app-mux preset logic: per-app static mux
// + min_hops; latency-sensitive apps stay single-route, bandwidth apps
// get parallel legs.
func decideAppMux(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client":
		return routeSpecWire{Mux: 4, MinHops: 2}
	case "skychat":
		// Chat is latency-sensitive — single route, lowest mux.
		return routeSpecWire{Mux: 1}
	default:
		// Everything else: visor defaults (empty spec).
		return routeSpecWire{}
	}
}

// decideRotatingBW is the verbatim rotating-bw preset logic: mux=4 over
// multi-hop with a leg rotated every 90s for bandwidth-heavy apps.
func decideRotatingBW(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		// min_hops=2 already says "no direct transport" — direct is 0
		// intermediates, so requiring at least 1 intermediate rules it
		// out. The visor's policy layer treats min_hops>=2 as an
		// implicit avoid_direct signal on the direct-dial bridge, so
		// the dial flows to the overlay path where rotation can act on
		// the resulting route group.
		return routeSpecWire{
			Mux:                     4,
			MinHops:                 2,
			RotationIntervalSeconds: 90,
			Distribution:            "weighted: 1, 1, 1, 1",
		}
	}
	return routeSpecWire{}
}

// targetMux is the mux size the rotating-bw policy aims to maintain.
// Kept in sync with the Mux value returned from decideRotatingBW so
// on_tick can reason about "are we at target?"
const targetMux = 4

//export on_tick
func onTick(inPtr, inLen uint32) uint64 {
	var input tickInputWire
	if err := json.Unmarshal(readInput(inPtr, inLen), &input); err != nil {
		return 0
	}
	// Only rotating-bw has tick logic; app-mux and unknown presets are
	// static, so they take no rotation action.
	if input.Preset != "rotating-bw" {
		return 0
	}
	return tickRotatingBW(input)
}

// tickRotatingBW is the verbatim rotating-bw rotation logic.
//
//	alive_count >= target_mux  → drop oldest + add  (steady-state
//	                             rotation: one in, one out, leg set
//	                             drifts across the eligible-peer set)
//	alive_count <  target_mux  → add only           (recover toward
//	                             target without shrinking further)
//	alive_count == 0           → no-op              (defensive; nothing
//	                             to rotate yet)
//
// Gating drop on alive_count >= target_mux means failed adds just delay
// rotation; the group never shrinks below the target.
func tickRotatingBW(input tickInputWire) uint64 {
	aliveCount := 0
	oldestAliveIdx := -1
	for _, l := range input.Legs {
		if l.Alive {
			aliveCount++
			if oldestAliveIdx == -1 || l.Index < oldestAliveIdx {
				oldestAliveIdx = l.Index
			}
		}
	}
	if aliveCount == 0 {
		return 0
	}
	var action rotationActionWire
	if aliveCount >= targetMux {
		action = rotationActionWire{
			DropLegs: []int{oldestAliveIdx},
			AddLeg:   true,
		}
	} else {
		action = rotationActionWire{
			AddLeg: true,
		}
	}
	out, err := json.Marshal(action)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// main is required by the WASI target but isn't called by the host at
// decide-time. It runs once at module instantiation.
func main() {}
