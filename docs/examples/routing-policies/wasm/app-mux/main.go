// app-mux.wasm — WASM port of docs/examples/routing-policies/
// app-mux.star. Build with TinyGo:
//
//	cd docs/examples/routing-policies/wasm/app-mux
//	tinygo build -target=wasi -no-debug -opt=2 -o app-mux.wasm .
//
// Install:
//
//	sudo install -m 0644 app-mux.wasm /etc/skywire/policies/
//	# in skywire.json: "routing": {
//	#   "policy_per_dial": "@/etc/skywire/policies/app-mux.wasm"
//	# }
//	# backend dispatched by file extension.
//
// Same semantic as the skylark version: per-app mux + min_hops,
// latency-sensitive apps stay single-route, bandwidth apps get
// parallel legs.
package main

import (
	"encoding/json"
	"unsafe"
)

// Wire types (kept in sync with pkg/router/policy/wasm/abi.go).
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

type routeSpecWire struct {
	Chosen         *candidateWire `json:"chosen,omitempty"`
	ReverseChosen  *candidateWire `json:"reverse_chosen,omitempty"`
	Mux            int            `json:"mux,omitempty"`
	ForwardMux     int            `json:"forward_mux,omitempty"`
	ReverseMux     int            `json:"reverse_mux,omitempty"`
	MinHops        int            `json:"min_hops,omitempty"`
	ForwardMinHops int            `json:"forward_min_hops,omitempty"`
	ReverseMinHops int            `json:"reverse_min_hops,omitempty"`
	Fallback       string         `json:"fallback,omitempty"`
	Distribution   string         `json:"distribution,omitempty"`
}

// Required: host-driven memory management.

//export alloc
func alloc(size uint32) uint32 {
	buf := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buf[0]))) //nolint:gosec
}

//export free
func free(ptr, size uint32) {
	// TinyGo's GC reclaims; explicit free is a no-op.
	_ = ptr
	_ = size
}

// readInput reads `length` bytes from linear memory at `ptr`.
func readInput(ptr, length uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(length))
}

// writeOutput allocates a buffer, copies data in, and returns
// the packed (ptr | len<<32) pair the host can decode.
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
	switch input.Ctx.App {
	case "vpn-client":
		spec = routeSpecWire{Mux: 4, MinHops: 2}
	case "skychat":
		// Chat is latency-sensitive — single route, lowest mux.
		spec = routeSpecWire{Mux: 1}
	default:
		// Everything else: visor defaults (empty spec).
	}

	out, err := json.Marshal(spec)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// main is required by the WASI target but isn't called by the host
// at decide-time. It runs once at module instantiation.
func main() {}
