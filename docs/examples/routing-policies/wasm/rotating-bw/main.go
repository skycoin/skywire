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
	AvoidDirect             bool   `json:"avoid_direct,omitempty"`
}

type rotationActionWire struct {
	DropLegs    []int    `json:"drop_legs,omitempty"`
	AddLeg      bool     `json:"add_leg,omitempty"`
	ExcludeHops []string `json:"exclude_hops,omitempty"`
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
	// mux=4 over multi-hop, rotation every 90s.
	// Round-robin distribution spreads bytes evenly across legs.
	// Bandwidth-heavy apps (vpn-client, skysocks-client) get the
	// rotation treatment; everything else gets defaults.
	var spec routeSpecWire
	switch input.Ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		spec = routeSpecWire{
			Mux:                     4,
			MinHops:                 2,
			RotationIntervalSeconds: 90,
			Distribution:            "weighted: 1, 1, 1, 1",
			// AvoidDirect: true is implied by MinHops=2, but
			// stating it explicitly documents intent — even if a
			// direct stcpr exists to the remote, we want the
			// overlay path so the rotation hook has a route group
			// to act on.
			AvoidDirect: true,
		}
	}
	out, err := json.Marshal(spec)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

//export on_tick
func onTick(inPtr, inLen uint32) uint64 {
	var input tickInputWire
	if err := json.Unmarshal(readInput(inPtr, inLen), &input); err != nil {
		return 0
	}
	// Rotation: drop the oldest leg (lowest index of an alive leg)
	// and add a fresh one. ExcludeHops carries the dropped leg's
	// transport kind as a (toy) signal — a real policy would pass
	// the dropped leg's peer-PK chain so the new leg picks a
	// disjoint intermediate set.
	//
	// This example doesn't have access to per-leg hop PKs from
	// the wire (LegInfo is index/kind/latency/alive only). A
	// future ABI extension could expand LegInfoWire with
	// hops/hops_geo so policies can construct precise excludes.
	var oldestAliveIdx int = -1
	for _, l := range input.Legs {
		if l.Alive {
			if oldestAliveIdx == -1 || l.Index < oldestAliveIdx {
				oldestAliveIdx = l.Index
			}
		}
	}
	if oldestAliveIdx == -1 {
		// No live legs to rotate.
		return 0
	}
	action := rotationActionWire{
		DropLegs: []int{oldestAliveIdx},
		AddLeg:   true,
	}
	out, err := json.Marshal(action)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

func main() {}
