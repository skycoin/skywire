//go:build !(js && wasm) && !ios

// Package visor pkg/visor/gctune_native.go c3-vis-core
package visor

import "github.com/skycoin/skywire/pkg/logging"

// applyGCTuning is a no-op off wasm and iOS: the OS reclaims a transient heap
// peak, so the default GOGC needs no platform override. See gctune_js.go and
// gctune_ios.go for why those two do. GOGC itself still works everywhere.
func applyGCTuning(*logging.Logger) {}
