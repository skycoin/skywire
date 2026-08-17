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
	"sort"
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
	Index       int      `json:"index"`
	Kind        string   `json:"kind"`
	LatencyMs   int      `json:"latency_ms"`
	Alive       bool     `json:"alive"`
	SentBytes   uint64   `json:"sent_bytes,omitempty"`
	RecvBytes   uint64   `json:"recv_bytes,omitempty"`
	Retransmits uint64   `json:"retransmits,omitempty"`
	Hops        []string `json:"hops,omitempty"`
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
	case "latency-adaptive":
		spec = decideLatencyAdaptive(input.Ctx)
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

// decideLatencyAdaptive is the latency-adaptive preset's decide logic: an
// ASYMMETRIC spec for bandwidth/proxy apps — a single lean, direct-ok
// upstream leg (uploads are small) paired with a 4-way multi-hop
// downstream fan-out (bulk downloads). RotationIntervalSeconds=30 keeps
// on_tick firing so the downstream set can be re-evaluated and the
// slowest leg evicted; Distribution "auto" lets the host weight bytes
// toward the faster legs. Non-target apps get the empty spec (defaults).
func decideLatencyAdaptive(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		// Symmetric mux=4: the on_tick evict-slowest logic acts on the
		// route group's forward legs (rg.tps) — the only legs the tick
		// hook sees (reverse-only legs live in rg.rvs and are invisible
		// to on_tick). An asymmetric 1-up/4-down shape would put the 4
		// adaptive legs on the reverse side where on_tick can't manage
		// them, making the eviction a no-op. Symmetric mux gives on_tick
		// 4 real legs to converge over. (The lean-upstream refinement
		// awaits a router change exposing reverse legs to the tick hook.)
		return routeSpecWire{
			Mux:                     4,
			MinHops:                 2,
			RotationIntervalSeconds: 30,
			Distribution:            "auto",
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
	// rotating-bw and latency-adaptive have tick logic; app-mux and
	// unknown presets are static, so they take no rotation action.
	switch input.Preset {
	case "rotating-bw":
		return tickRotatingBW(input)
	case "latency-adaptive":
		return tickLatencyAdaptive(input)
	default:
		return 0
	}
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

// tickLatencyAdaptive is the latency-adaptive rotation logic. It differs
// from rotating-bw in what it evicts and WHEN:
//
//	rotating-bw drops the OLDEST alive leg every single tick — an
//	unconditional churn that keeps the set drifting for
//	traffic-analysis resistance.
//
//	latency-adaptive instead drops the SLOWEST alive leg, and ONLY when
//	that leg is a clear outlier — its measured latency_ms is at least
//	1.5x the median of the alive legs. This hysteresis band means: once
//	the set has converged to a cluster of comparably-fast legs (worst <
//	1.5x median), NO leg qualifies and the policy holds — no churn. It
//	converges toward a low-latency disjoint set and then stops thrashing,
//	the opposite of rotating-bw's every-tick rotation.
//
//	alive_count == 0            → no-op (nothing measured yet)
//	alive_count <  target_mux   → add only (recover toward target; never
//	                              shrink below it)
//	alive_count >= target_mux &&
//	  worst_latency > 0 &&
//	  worst >= 1.5 * median     → drop slowest + add + exclude its hops
//	                              (evict the outlier; exclude its
//	                              intermediates so the replacement differs)
//	otherwise (converged)       → no-op (hold; do not churn)
func tickLatencyAdaptive(input tickInputWire) uint64 {
	const targetMux = 4

	aliveCount := 0
	worstIdx := -1
	worstLatency := -1
	var lat []int
	var worstHops []string
	for _, l := range input.Legs {
		if !l.Alive {
			continue
		}
		aliveCount++
		lat = append(lat, l.LatencyMs)
		if l.LatencyMs > worstLatency {
			worstLatency = l.LatencyMs
			worstIdx = l.Index
			worstHops = l.Hops
		}
	}
	if aliveCount == 0 {
		return 0
	}
	if aliveCount < targetMux {
		return writeAction(rotationActionWire{AddLeg: true})
	}

	// Median latency of the alive legs.
	sort.Ints(lat)
	var median int
	if n := len(lat); n%2 == 1 {
		median = lat[n/2]
	} else {
		median = (lat[n/2-1] + lat[n/2]) / 2
	}

	// Hysteresis: only evict a clear outlier (worst >= 1.5x median).
	// Once the set has converged (worst < 1.5x median) this is false for
	// every leg, so we hold and stop churning. Integer form of
	// worst >= 1.5*median is 2*worst >= 3*median.
	if worstLatency > 0 && worstIdx >= 0 && 2*worstLatency >= 3*median {
		return writeAction(rotationActionWire{
			DropLegs:    []int{worstIdx},
			AddLeg:      true,
			ExcludeHops: worstHops,
		})
	}
	// Converged: all alive legs comparably fast — hold.
	return 0
}

// writeAction marshals a rotation action and packs it for the host.
func writeAction(action rotationActionWire) uint64 {
	out, err := json.Marshal(action)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// main is required by the WASI target but isn't called by the host at
// decide-time. It runs once at module instantiation.
func main() {}
