// Package visor pkg/visor/api_routing_policies.go — read-side
// surface for the hypervisor UI's routing-policy panel. Walks
// the router's installed DialHook (when it's a *policy.Hook)
// and projects its visor-wide default + per-app overrides into
// a JSON-friendly shape.
//
// Read-only by design — runtime policy installation lives on the
// SetAppRoutingPolicy path (separate PR). This file only
// surfaces what's currently configured.
package visor

import (
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/router/policy"
)

// RoutingPolicyInfo describes one installed engine for the UI.
type RoutingPolicyInfo struct {
	// Source is the path or inline marker the engine was
	// constructed from — file path, "<inline>", or "<noop>".
	Source string `json:"source"`

	// Active reports whether an evaluator is currently loaded
	// (false on a noop / failed-to-load engine).
	Active bool `json:"active"`

	// Backend is "skylark" or "wasm", derived from the source
	// file extension. Inline policies are always skylark.
	// Empty string when Source is "<noop>" / "<inline>" — UI
	// can fall back to Source for display.
	Backend string `json:"backend,omitempty"`
}

// RoutingPoliciesSummary is the JSON shape returned by
// /visors/{pk}/routing-policies. Default may be nil when no
// visor-wide policy is configured; PerApp is empty (not nil) so
// the JSON encodes as {} rather than null for missing data.
type RoutingPoliciesSummary struct {
	Default *RoutingPolicyInfo            `json:"default,omitempty"`
	PerApp  map[string]*RoutingPolicyInfo `json:"per_app"`
}

// RoutingPolicies projects the router's installed DialHook into
// a summary the hypervisor UI can render. Returns an empty
// summary (with no default + empty PerApp) when no hook is
// installed or the installed hook isn't a *policy.Hook.
func (v *Visor) RoutingPolicies() (*RoutingPoliciesSummary, error) {
	out := &RoutingPoliciesSummary{PerApp: map[string]*RoutingPolicyInfo{}}
	if v.router == nil {
		return out, nil
	}
	hook, ok := v.router.DialHook().(*policy.Hook)
	if !ok || hook == nil {
		return out, nil
	}
	snap := hook.Snapshot()
	if snap.Default != nil {
		out.Default = &RoutingPolicyInfo{
			Source:  snap.Default.Source,
			Active:  snap.Default.Active,
			Backend: backendFromSource(snap.Default.Source),
		}
	}
	for name, eng := range snap.PerApp {
		out.PerApp[name] = &RoutingPolicyInfo{
			Source:  eng.Source,
			Active:  eng.Active,
			Backend: backendFromSource(eng.Source),
		}
	}
	return out, nil
}

// backendFromSource derives "skylark" / "wasm" / "" from the
// engine's Source identifier. File paths get the extension
// check; inline / noop markers return "" so the UI shows the
// raw source string.
func backendFromSource(source string) string {
	if source == "" || source == "<noop>" || source == "<inline>" {
		return ""
	}
	if strings.EqualFold(filepath.Ext(source), ".wasm") {
		return "wasm"
	}
	return "skylark"
}
