// Package preset pkg/router/policy/preset/names.go c2-net-routing
// enumerates the built-in preset names Decide / Engine.OnTick
// dispatch on, so callers (the wasm-visor's config selection, the
// CLI, tests) can validate a requested preset without importing the
// wazero-side manifest.
package preset

// Names returns the built-in preset names, in a stable order. Kept in sync
// with the switch in Decide (preset.go) and OnTick (tick.go) — the same set
// the wazero bundle's manifest.json lists.
func Names() []string {
	return []string{
		"app-mux",
		"rotating-bw",
		"latency-adaptive",
		"elastic-mux",
		"probe-and-prune",
		"coupled",
		"adaptive",
		"geo-avoid",
		"transport-diverse",
		"trust-tiered",
		"time-of-day",
		"ledbat",
	}
}

// Has reports whether name is a built-in preset. Unknown names still Decide to
// the app-mux fallback (matching the bundle), but callers that want to reject
// typos up front use this.
func Has(name string) bool {
	for _, n := range Names() {
		if n == name {
			return true
		}
	}
	return false
}
