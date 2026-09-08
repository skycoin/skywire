//go:build !(js && wasm)

// Package visor pkg/visor/gctune_native.go c3-vis-core
package visor

import "github.com/skycoin/skywire/pkg/logging"

// applyGCTuning is a no-op off wasm: the OS reclaims a transient heap peak, so
// the default GOGC needs no platform override. See gctune_js.go for why wasm
// does. GOGC itself still works everywhere.
func applyGCTuning(*logging.Logger) {}
