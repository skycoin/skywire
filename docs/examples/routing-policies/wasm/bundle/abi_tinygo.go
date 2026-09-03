//go:build tinygo

// The wasm ABI half of the bundle: host-driven linear-memory management and
// the //export entry points. Kept behind the tinygo tag — the unsafe
// pointer↔uintptr casts are the standard wasi guest idiom but are exactly
// what `go vet` flags, and nothing native ever runs them (the native visor
// calls pkg/router/policy/preset directly; the parity test exercises this
// module through wazero from its committed build).
package main

import (
	"encoding/json"
	"unsafe"

	"github.com/skycoin/skywire/pkg/router/policy/preset"
)

// engine holds the adaptive tick controllers' per-transport_id state for this
// module instance. One instance mirrors the bundle's original package-global
// tick state (the host instantiates one wazero module per policy load, so this
// is created fresh per load); the native visor constructs its own preset.Engine
// per evaluator the same way.
//
// It is created LAZILY on the first tick, NOT as a package-var initializer
// (`var engine = preset.New()`). TinyGo's compile-time interp pass partially
// evaluates package-var initializers and left the later map fields of the
// Engine nil, so the first write to a coupled/ledbat map nil-map-panicked in
// the guest. Constructing it at runtime through engineForTick sidesteps the
// interp pass entirely; the parity test's coupled/ledbat/adaptive tick cases
// guard against a regression.
var engine *preset.Engine

// engineForTick returns the module's Engine, constructing it on first use.
// on_tick is serialized by the host (one wazero call at a time), so no locking
// is needed.
func engineForTick() *preset.Engine {
	if engine == nil {
		engine = preset.New()
	}
	return engine
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

// applyTunables syncs this guest module's preset-package atomics to the
// host-stamped runtime widths before dispatch, so the adaptive engine (which
// reads AdaptCap / AdaptRevActive / AdaptStandbyMax) uses the operator's live /
// per-policy values instead of the guest's compiled defaults. A zero field is
// "unset" (an older host, or a single-preset module) — leave that atomic alone.
// The clamping order matches ApplyOverrideTunables (standby, then cap, then
// rev-active) so the setters compose identically on both paths.
func applyTunables(cap, revActive, standbyMax int) {
	if standbyMax > 0 {
		preset.SetAdaptStandbyMax(standbyMax)
	}
	if cap > 0 {
		preset.SetAdaptCap(cap)
	}
	if revActive > 0 {
		preset.SetAdaptRevActive(revActive)
	}
}

//export decide_route
func decideRoute(inPtr, inLen uint32) uint64 {
	var input decideInputWire
	if err := json.Unmarshal(readInput(inPtr, inLen), &input); err != nil {
		return 0
	}
	applyTunables(input.AdaptCap, input.AdaptRevActive, input.AdaptStandbyMax)
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
	applyTunables(input.AdaptCap, input.AdaptRevActive, input.AdaptStandbyMax)
	action := engineForTick().OnTick(input.Preset, legsToPreset(input.Legs))
	out, err := json.Marshal(actionToWire(action))
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

func main() {}
