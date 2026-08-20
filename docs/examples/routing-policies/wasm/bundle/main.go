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
// This file is now a THIN SHIM. The preset decide/tick logic itself lives
// ONCE, as pure Go, in github.com/skycoin/skywire/pkg/router/policy/preset
// (the single source of truth). This shim keeps only the wasm ABI: the
// JSON wire types, the alloc/free/read/write linear-memory glue, and the
// //export decide_route / on_tick entry points. Each entry point unmarshals
// the wire input, converts it to the shared preset types, calls into the
// preset package, and marshals the result back out. The NATIVE visor runs
// the compiled bundle.wasm via wazero; the WASM (browser/TinyGo) visor
// compiles the SAME preset package in directly (it cannot host wazero —
// wasm-in-wasm), so both paths make byte-identical decisions.
//
// This is the module embedded at pkg/router/policy/wasm/presets/
// bundle.wasm and selected by config as "preset:<name>" (see
// pkg/visor/policy_loader.go). The per-preset standalone examples next
// to this dir (app-mux/, rotating-bw/) stay as pedagogical single-preset
// modules; this one is their union.
//
// Build with TinyGo (from this directory):
//
//	tinygo build -target=wasi -no-debug -opt=2 \
//	  -o ../../../../pkg/router/policy/wasm/presets/bundle.wasm .
package main

import (
	"encoding/json"
	"unsafe"

	"github.com/skycoin/skywire/pkg/router/policy/preset"
)

// Wire types — kept in sync with pkg/router/policy/wasm/abi.go. These are the
// JSON envelopes the host (wazero) marshals across linear memory; the guest
// converts them to/from the pure preset.* types.

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
	TransportID string   `json:"transport_id"`
	LatencyMs   int      `json:"latency_ms"`
	Alive       bool     `json:"alive"`
	Standby     bool     `json:"standby,omitempty"`
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
	DropLegs           []int    `json:"drop_legs,omitempty"`
	AddLeg             bool     `json:"add_leg,omitempty"`
	ExcludeHops        []string `json:"exclude_hops,omitempty"`
	DemoteToStandby    []int    `json:"demote_to_standby,omitempty"`
	PromoteFromStandby []int    `json:"promote_from_standby,omitempty"`
}

// engine holds the adaptive tick controllers' per-transport_id state for this
// module instance. One package-global instance mirrors the bundle's original
// package-global tick state (the host instantiates one wazero module per policy
// load, so this is created fresh per load); the native visor constructs its own
// preset.Engine per evaluator the same way.
var engine = preset.New()

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
	spec := preset.Decide(input.Preset, ctxToPreset(input.Ctx), candsToPreset(input.Candidates))
	out, err := json.Marshal(specToWire(spec))
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
	action := engine.OnTick(input.Preset, legsToPreset(input.Legs))
	out, err := json.Marshal(actionToWire(action))
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// --- wire <-> preset conversions ---

func ctxToPreset(c routingContextWire) preset.Context {
	return preset.Context{
		App:               c.App,
		PeerPK:            c.PeerPK,
		Port:              c.Port,
		NowUnixNano:       c.NowUnixNano,
		CLIOverrides:      c.CLIOverrides,
		IsDirectDial:      c.IsDirectDial,
		TransportKind:     c.TransportKind,
		ReverseCandidates: candsToPreset(c.ReverseCandidates),
	}
}

func candsToPreset(cs []candidateWire) []preset.Candidate {
	if cs == nil {
		return nil
	}
	out := make([]preset.Candidate, len(cs))
	for i, c := range cs {
		out[i] = candToPreset(c)
	}
	return out
}

func candToPreset(c candidateWire) preset.Candidate {
	return preset.Candidate{
		Hops:           c.Hops,
		HopsGeo:        c.HopsGeo,
		EstLatencyMs:   c.EstLatencyMs,
		TransportKinds: c.TransportKinds,
	}
}

func legsToPreset(ls []legInfoWire) []preset.LegInfo {
	if ls == nil {
		return nil
	}
	out := make([]preset.LegInfo, len(ls))
	for i, l := range ls {
		out[i] = preset.LegInfo{
			Index:       l.Index,
			Kind:        l.Kind,
			TransportID: l.TransportID,
			LatencyMs:   l.LatencyMs,
			Alive:       l.Alive,
			Standby:     l.Standby,
			SentBytes:   l.SentBytes,
			RecvBytes:   l.RecvBytes,
			Retransmits: l.Retransmits,
			Hops:        l.Hops,
		}
	}
	return out
}

func specToWire(s preset.Spec) routeSpecWire {
	w := routeSpecWire{
		Mux:                     s.Mux,
		ForwardMux:              s.ForwardMux,
		ReverseMux:              s.ReverseMux,
		MinHops:                 s.MinHops,
		ForwardMinHops:          s.ForwardMinHops,
		ReverseMinHops:          s.ReverseMinHops,
		Fallback:                s.Fallback,
		Distribution:            s.Distribution,
		RotationIntervalSeconds: s.RotationIntervalSeconds,
	}
	if s.Chosen != nil {
		c := candToWire(*s.Chosen)
		w.Chosen = &c
	}
	if s.ReverseChosen != nil {
		c := candToWire(*s.ReverseChosen)
		w.ReverseChosen = &c
	}
	return w
}

func candToWire(c preset.Candidate) candidateWire {
	return candidateWire{
		Hops:           c.Hops,
		HopsGeo:        c.HopsGeo,
		EstLatencyMs:   c.EstLatencyMs,
		TransportKinds: c.TransportKinds,
	}
}

func actionToWire(a preset.RotationAction) rotationActionWire {
	return rotationActionWire{
		DropLegs:           a.DropLegs,
		AddLeg:             a.AddLeg,
		ExcludeHops:        a.ExcludeHops,
		DemoteToStandby:    a.DemoteToStandby,
		PromoteFromStandby: a.PromoteFromStandby,
	}
}

// --- thin per-preset wrappers used by main_test.go (the conditional-preset
// constraint tests). They delegate to the shared preset package so the tests
// exercise the single source of truth through the wire types. ---

func decideGeoAvoid(ctx routingContextWire, cands []candidateWire) routeSpecWire {
	return specToWire(preset.Decide("geo-avoid", ctxToPreset(ctx), candsToPreset(cands)))
}

func decideTransportDiverse(ctx routingContextWire, cands []candidateWire) routeSpecWire {
	return specToWire(preset.Decide("transport-diverse", ctxToPreset(ctx), candsToPreset(cands)))
}

func decideTrustTiered(ctx routingContextWire, cands []candidateWire) routeSpecWire {
	return specToWire(preset.Decide("trust-tiered", ctxToPreset(ctx), candsToPreset(cands)))
}

func decideTimeOfDay(ctx routingContextWire) routeSpecWire {
	return specToWire(preset.Decide("time-of-day", ctxToPreset(ctx), nil))
}

// distinctCount counts unique (case-folded) entries — used by main_test.go to
// assert transport-diverse picked the most-diverse route.
func distinctCount(xs []string) int {
	seen := map[string]bool{}
	for _, x := range xs {
		seen[toLower(x)] = true
	}
	return len(seen)
}

// toLower is a tiny ASCII lowercaser so distinctCount stays dependency-free.
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// main is required by the WASI target but isn't called by the host at
// decide-time. It runs once at module instantiation.
func main() {}
