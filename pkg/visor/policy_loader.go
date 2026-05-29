// Package visor pkg/visor/policy_loader.go — extension-dispatch
// for routing-policy backends. A single config field
// (`routing.policy_per_dial`, per-app `routing_policy`, or the
// runtime `app start --routing-policy <path>` flag) accepts
// both `.star` (Starlark) and `.wasm` (WASM) artifacts; this
// helper inspects the path and instantiates the matching
// Loader, returning the shared policy.Engine interface so
// callers don't branch on backend.
package visor

import (
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/router/policy"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
)

// policyEngine is the runtime triple a caller needs after loading
// a routing policy: the Engine itself, a Watch callback that
// installs the hot-reload watcher (no-op when the backend doesn't
// support it), and a close-stack tag identifying the backend
// (used as the prefix in v.pushCloseStack).
type policyEngine struct {
	engine policy.Engine
	watch  func(logger func(string, ...interface{})) error
	tag    string // "router.policy" or "router.policy.wasm"
}

// loadPolicyEngine reads raw and instantiates either a Starlark
// or WASM Loader by file extension. Returns nil + nil on
// empty/none (caller treats as "no policy"). Errors propagate
// from the underlying NewLoader.
func loadPolicyEngine(raw string, provider policy.Provider, logger func(string, ...interface{})) (*policyEngine, error) {
	if raw == "" || raw == "none" {
		return nil, nil
	}
	if isWasmPath(raw) {
		l, err := policywasm.NewLoader(raw,
			policywasm.WithLogger(logger),
			policywasm.WithProvider(provider))
		if err != nil {
			return nil, err
		}
		return &policyEngine{engine: l, watch: l.Watch, tag: "router.policy.wasm"}, nil
	}
	l, err := policy.NewLoader(raw,
		policy.WithLogger(logger),
		policy.WithProvider(provider))
	if err != nil {
		return nil, err
	}
	return &policyEngine{engine: l, watch: l.Watch, tag: "router.policy"}, nil
}

// isWasmPath reports whether raw refers to a `.wasm` file.
// Inline values (no leading `@`) are always Starlark — a
// compiled WASM module doesn't fit in a JSON string.
func isWasmPath(raw string) bool {
	if !strings.HasPrefix(raw, "@") {
		return false
	}
	return strings.EqualFold(filepath.Ext(strings.TrimPrefix(raw, "@")), ".wasm")
}
