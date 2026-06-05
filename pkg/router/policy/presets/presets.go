// Package presets pkg/router/policy/presets — curated routing-policy programs
// embedded in the skywire binary, selectable by name without writing or
// compiling a policy file. A config value of the form "preset:<name>" resolves
// to one of these (see pkg/router/policy/loader.go), so a remote visor runs the
// exact same tested policy by name — no fetching or compiling source on the
// target.
//
// Each *.star file in this directory is a preset; the preset name is the
// filename without the ".star" suffix. Adding a preset is just dropping a file
// here. Starlark presets need no build step (interpreted at load); precompiled
// TinyGo .wasm presets can be added alongside in a follow-up via a parallel
// registry keyed the same way.
package presets

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.star
var fsys embed.FS

var registry = loadRegistry()

func loadRegistry() map[string]string {
	m := map[string]string{}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return m
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".star") {
			continue
		}
		b, err := fsys.ReadFile(e.Name())
		if err != nil {
			continue
		}
		m[strings.TrimSuffix(e.Name(), ".star")] = string(b)
	}
	return m
}

// Source returns the embedded Starlark source for a preset name, if it exists.
func Source(name string) (string, bool) {
	s, ok := registry[name]
	return s, ok
}

// Names returns the available preset names, sorted.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
