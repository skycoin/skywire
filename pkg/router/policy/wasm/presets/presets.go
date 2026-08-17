// Package presets pkg/router/policy/wasm/presets/presets.go c2-net-routing
// is the WASM analog of pkg/router/policy/presets: a registry of
// precompiled TinyGo routing-policy modules embedded in the skywire
// binary and selectable by name via a config value of the form
// "preset:<name>" (see pkg/visor/policy_loader.go), exactly like the
// Starlark presets. A remote visor then runs the exact same tested
// compiled policy by name — no fetching or compiling a .wasm on the
// target.
//
// This is now the COMBINED single-module form (#3942 amortization done).
// A single embedded bundle.wasm carries every preset and dispatches by
// name at call time: the host stamps the selected preset into the
// decide/tick input wire (via policywasm.WithPreset) and the guest
// switches on it. This replaces the earlier per-file embed where each
// preset shipped its own ~500KB copy of the TinyGo runtime — the
// runtime is now paid once and each extra preset adds only its own few
// KB of logic.
//
// The manifest.json (name → one-line summary) is the source of truth
// for which preset names exist (Names/Has) and their descriptions
// (Describe). Adding a preset is: add its case to the bundle guest
// (docs/examples/routing-policies/wasm/bundle/main.go), rebuild
// bundle.wasm, copy it here, and add its manifest line.
package presets

import (
	_ "embed"
	"encoding/json"
	"sort"
)

//go:embed bundle.wasm
var bundleWASM []byte

//go:embed manifest.json
var manifestJSON []byte

var descriptions = loadManifest()

func loadManifest() map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal(manifestJSON, &m) //nolint:errcheck
	return m
}

// Bundle returns the embedded combined WASM module carrying every
// preset. Callers load it once and select a preset by stamping its name
// via policywasm.WithPreset(name); the module dispatches internally.
func Bundle() []byte { return bundleWASM }

// Has reports whether name is a known WASM preset (manifest membership).
func Has(name string) bool {
	_, ok := descriptions[name]
	return ok
}

// Describe returns the one-line manifest description for a preset name,
// if it exists.
func Describe(name string) (string, bool) {
	s, ok := descriptions[name]
	return s, ok
}

// Names returns the available WASM preset names, sorted.
func Names() []string {
	names := make([]string, 0, len(descriptions))
	for n := range descriptions {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
