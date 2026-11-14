//go:build tinygo

// Package presets pkg/router/policy/wasm/presets/engine_tinygo.go c2-net-routing
package presets

// The TinyGo build has no WASM engine, so presets resolve to their Starlark twins.
const engineAvailable = false
