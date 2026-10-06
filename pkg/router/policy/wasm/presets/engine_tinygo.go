//go:build tinygo

package presets

// The TinyGo build has no WASM engine, so presets resolve to their Starlark twins.
const engineAvailable = false
