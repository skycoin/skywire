//go:build js && wasm

// Package visor pkg/visor/gctune_js.go c3-vis-core
package visor

import (
	"os"
	"runtime/debug"

	"github.com/skycoin/skywire/pkg/logging"
)

// wasmDefaultGCPercent is the GOGC value a browser visor runs at when the
// operator has not chosen one.
//
// WHY THIS DIFFERS FROM NATIVE. Go's wasm linear memory only ever GROWS:
// WebAssembly.Memory.grow() is one-way and the runtime never returns pages to
// the host. So on wasm the heap's PEAK is a permanent cost — the tab keeps the
// high-water mark for its whole life, however small the live set later becomes.
// Natively the same peak is transient, because the OS reclaims.
//
// GOGC sizes the heap as roughly (1 + GOGC/100) x live-after-GC, so the default
// of 100 reserves ~2x the peak live set and keeps it forever. Measured on the
// :8443 desk (2026-09-08): mem_heap_sys 848 MB against 285 MB live — a 3:1
// ratio, consistent with a ~420 MB peak at GOGC=100. At 50 the same peak
// reserves ~630 MB, ~218 MB less, permanently.
//
// 50 rather than lower: GOGC trades CPU for memory proportionally, and the
// browser visor's CPU budget is the scarcer resource of the two (a wasm visor
// shares ONE thread with nothing to steal work). 50 costs a modest increase in
// GC frequency against a large permanent memory saving; going much below it
// starts to matter on a single-threaded runtime.
//
// Deliberately NOT GOMEMLIMIT: that is a soft limit, and a live set that
// approaches it makes the collector run continuously without ever satisfying
// it — trading a memory problem for a worse CPU one on the exact runtime that
// can least afford it. GOGC has no such failure mode. memory_limit remains
// available for an operator who wants a hard ceiling.
const wasmDefaultGCPercent = 50

// applyGCTuning sets the wasm GOGC default, unless the operator already chose
// one. GOGC is read directly rather than inferred from SetGCPercent's previous
// value, so an explicit GOGC=100 is honored as a choice rather than mistaken
// for the unset default.
func applyGCTuning(log *logging.Logger) {
	if os.Getenv("GOGC") != "" {
		return
	}
	prev := debug.SetGCPercent(wasmDefaultGCPercent)
	log.Infof("GOGC set to %d for wasm (was %d): linear memory is never returned to the host, so heap PEAK is permanent",
		wasmDefaultGCPercent, prev)
}
