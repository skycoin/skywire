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

// Hook adapts a *Loader to the router.DialHook interface. Pass
// to router.Config.DialHook to plug a routing policy into the
// dial path.
type Hook struct {
	loader *Loader
}

// NewHook wraps a Loader as a DialHook.
func NewHook(l *Loader) *Hook { return &Hook{loader: l} }

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
	if h.loader == nil || !h.loader.IsActive() {
		return router.DialAdjustment{}, nil
	}
	rctx := RoutingContext{
		App:    info.AppName,
		PeerPK: info.PeerPK.Hex(),
		Port:   uint16(info.RPort),
	}
	spec, err := h.loader.Decide(ctx, rctx, nil)
	if err != nil {
		return router.DialAdjustment{}, err
	}
	return router.DialAdjustment{
		MuxRoutes: spec.Mux,
		MinHops:   spec.MinHops,
		Fallback:  spec.Fallback,
	}, nil
}
