// Package presets pkg/router/policy/wasm/presets/presets.go c2-net-routing
// is the WASM analog of pkg/router/policy/presets: a registry of
// precompiled TinyGo routing-policy modules embedded in the skywire
// binary and selectable by name via a config value of the form
// "preset:<name>" (see pkg/visor/policy_loader.go), exactly like the
// Starlark presets. A remote visor then runs the exact same tested
// compiled policy by name — no fetching or compiling a .wasm on the
// target.
//
// Each *.wasm file in this directory is a preset; the preset name is
// the filename without the ".wasm" suffix. Descriptions come from the
// embedded manifest.json (name → one-line summary). Adding a preset is
// dropping the built module here and adding its manifest line.
//
// This uses a per-file embed to keep the registry trivially inspectable
// (one .wasm == one preset). If the preset count grows enough that N
// separate multi-hundred-KB blobs become a size concern, a future
// combined-single-bundle (#3942 amortization) could replace the
// per-file embed with one bundle indexed by name — the public API here
// (Names/Module/Describe) is the seam that would stay stable.
package presets

import (
	"embed"
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.wasm
var wasmFS embed.FS

//go:embed manifest.json
var manifestJSON []byte

var (
	registry     = loadRegistry()
	descriptions = loadManifest()
)

func loadRegistry() map[string][]byte {
	m := map[string][]byte{}
	entries, err := fs.ReadDir(wasmFS, ".")
	if err != nil {
		return m
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".wasm") {
			continue
		}
		b, err := wasmFS.ReadFile(e.Name())
		if err != nil {
			continue
		}
		m[strings.TrimSuffix(e.Name(), ".wasm")] = b
	}
	return m
}

func loadManifest() map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal(manifestJSON, &m) //nolint:errcheck
	return m
}

// Module returns the embedded compiled WASM bytes for a preset name,
// if it exists.
func Module(name string) ([]byte, bool) {
	b, ok := registry[name]
	return b, ok
}

// Describe returns the one-line manifest description for a preset name,
// if it exists.
func Describe(name string) (string, bool) {
	s, ok := descriptions[name]
	return s, ok
}

// Names returns the available WASM preset names, sorted.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
