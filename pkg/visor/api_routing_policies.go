// Package visor pkg/visor/api_routing_policies.go c3-vis-core
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
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// RoutingPolicies projects the router's installed DialHook into
// a summary the hypervisor UI can render. Returns an empty
// summary (with no default + empty PerApp) when no hook is
// installed or the installed hook isn't a *policy.Hook.
func (v *Visor) RoutingPolicies() (*visorapi.RoutingPoliciesSummary, error) {
	out := &visorapi.RoutingPoliciesSummary{PerApp: map[string]*visorapi.RoutingPolicyInfo{}}
	if v.router == nil {
		return out, nil
	}
	hook, ok := v.router.DialHook().(*policy.Hook)
	if !ok || hook == nil {
		return out, nil
	}
	snap := hook.Snapshot()
	if snap.Default != nil {
		out.Default = &visorapi.RoutingPolicyInfo{
			Source:  snap.Default.Source,
			Active:  snap.Default.Active,
			Backend: backendFromSource(snap.Default.Source),
		}
	}
	for name, eng := range snap.PerApp {
		out.PerApp[name] = &visorapi.RoutingPolicyInfo{
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
