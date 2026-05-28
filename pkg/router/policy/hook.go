// Package policy pkg/router/policy/hook.go — adapter that turns
// a Loader (the operator's Starlark policy file) into a
// router.DialHook the router can invoke per dial.
//
// Living in the policy package keeps the router package free of
// the Starlark machinery; pkg/router only depends on the small
// DialHook interface in dial_hook.go.
package policy

import (
	"context"

	"github.com/skycoin/skywire/pkg/router"
)

// Hook adapts one default Loader plus optional per-app Loaders to
// the router.DialHook interface. Pass to router.Config.DialHook to
// plug a routing policy into the dial path.
//
// Per-app loaders take precedence over the default; an app with a
// configured RoutingPolicy gets its own evaluator, every other app
// falls through to the visor-wide default. An app whose per-app
// loader is registered but inactive still uses that loader (which
// returns a no-op spec) rather than escaping to the default — the
// per-app policy is the operator's intent for that app.
type Hook struct {
	loader *Loader
	byApp  map[string]*Loader
}

// NewHook wraps a Loader as a DialHook.
func NewHook(l *Loader) *Hook { return &Hook{loader: l} }

// RegisterApp attaches a per-app Loader that overrides the default
// for dials originating from the named app. Safe to call only
// during init (before the hook is handed to the router).
func (h *Hook) RegisterApp(appName string, l *Loader) {
	if h.byApp == nil {
		h.byApp = make(map[string]*Loader)
	}
	h.byApp[appName] = l
}

// loaderFor returns the per-app loader if one is registered for
// info.AppName, else the default.
func (h *Hook) loaderFor(appName string) *Loader {
	if h.byApp != nil {
		if l, ok := h.byApp[appName]; ok {
			return l
		}
	}
	return h.loader
}

// BeforeDial implements router.DialHook. Constructs a
// RoutingContext from the per-dial DialInfo, invokes the loader,
// projects the returned RouteSpec into a DialAdjustment the
// router can consume.
//
// The loader's failure-mode wiring (Fallback vs. Drop) determines
// what the policy script's failure looks like to the router:
// - FailureFallback (default): a broken script returns an empty
//   RouteSpec → empty DialAdjustment → router proceeds with the
//   caller's original opts. No error surfaced.
// - FailureDrop: errors propagate up here as a real error; the
//   router sees an error from BeforeDial and logs it but still
//   proceeds (failure-safe at the router layer too).
func (h *Hook) BeforeDial(ctx context.Context, info router.DialInfo) (router.DialAdjustment, error) {
	loader := h.loaderFor(info.AppName)
	if loader == nil || !loader.IsActive() {
		return router.DialAdjustment{}, nil
	}
	rctx := RoutingContext{
		App:    info.AppName,
		PeerPK: info.PeerPK.Hex(),
		Port:   uint16(info.RPort),
	}
	spec, err := loader.Decide(ctx, rctx, nil)
	if err != nil {
		return router.DialAdjustment{}, err
	}
	return router.DialAdjustment{
		MuxRoutes: spec.Mux,
		MinHops:   spec.MinHops,
		Fallback:  spec.Fallback,
	}, nil
}
