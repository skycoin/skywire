//go:build js && wasm

// Package main — routingpolicy_js.go: the wasm visor's live-switchable routing
// policy and its JS control surface. A browser visor gets the composite
// "adaptive" default out of the box (matching the native config generator), and
// the UI can switch the policy at runtime — to any preset, or "none" (the
// legacy no-policy behavior) — via the `routingPolicy` JS binding, without a
// reload. A switch takes effect on the NEXT dial; existing route groups keep
// their setup until re-dialed.
package main

import (
	"context"
	"sync"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/policy/preset"
	"github.com/skycoin/skywire/pkg/router/policy/presethook"
)

// switchableRoutingHook is the router's DialHook for the browser edge. It
// delegates to the currently-selected presethook.Hook, or behaves as a no-op
// (router-default routing) when the selection is "none". It implements every
// hook interface the router type-asserts for (DialHook, RouteSelectingHook,
// RotationHook) so a switch to "none" cleanly reverts to built-in behavior.
type switchableRoutingHook struct {
	mu   sync.RWMutex
	name string           // "none" or a preset name
	cur  *presethook.Hook // nil when name == "none"
}

// newSwitchableRoutingHook builds the hook with an initial selection.
func newSwitchableRoutingHook(name string) *switchableRoutingHook {
	s := &switchableRoutingHook{}
	s.set(name)
	return s
}

// set changes the active policy. "", "none" and "off" mean no policy (nil hook,
// router-default routing). An unknown preset name is ignored (keeps current).
func (s *switchableRoutingHook) set(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case "", "none", "off":
		s.name, s.cur = "none", nil
	default:
		if !preset.Has(name) {
			return s.name // ignore unknown; report unchanged selection
		}
		// nil Provider ⇒ Nop provider ⇒ adaptive (and the other transport-
		// diversity presets) gracefully degrade to their no-per-hop-metadata
		// shape in the browser, exactly as intended for the wasm edge.
		s.name, s.cur = name, presethook.New(name, nil)
	}
	return s.name
}

func (s *switchableRoutingHook) hook() *presethook.Hook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *switchableRoutingHook) current() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

// BeforeDial implements router.DialHook.
func (s *switchableRoutingHook) BeforeDial(ctx context.Context, info router.DialInfo) (router.DialAdjustment, error) {
	if h := s.hook(); h != nil {
		return h.BeforeDial(ctx, info)
	}
	return router.DialAdjustment{}, nil // no policy ⇒ no adjustment
}

// SelectRoute implements router.RouteSelectingHook.
func (s *switchableRoutingHook) SelectRoute(ctx context.Context, info router.DialInfo, forward, reverse []router.CandidateInfo) (router.RouteSelection, error) {
	if h := s.hook(); h != nil {
		return h.SelectRoute(ctx, info, forward, reverse)
	}
	return router.RouteSelection{Chosen: -1, ReverseChosen: -1}, nil // defer to router's built-in pick
}

// OnTick implements router.RotationHook.
func (s *switchableRoutingHook) OnTick(info router.DialInfo, legs []router.LegInfo) router.RotationAction {
	if h := s.hook(); h != nil {
		return h.OnTick(info, legs)
	}
	return router.RotationAction{} // no policy ⇒ no rotation action
}

// registerRoutingPolicyJS exposes the selector to the UI as a single callable:
//
//	routingPolicy()            → current selection (string, e.g. "adaptive")
//	routingPolicy("list")      → ["none", <preset names…>] for a dropdown
//	routingPolicy("get")       → current selection
//	routingPolicy("set", name) → applies name, returns the resulting selection
//
// The UI renders a dropdown from routingPolicy("list") (default-selecting the
// value of routingPolicy("get")) and calls routingPolicy("set", choice) on
// change. "none" is always the first option (the no-policy / A-B baseline).
func registerRoutingPolicyJS(hook *switchableRoutingHook) {
	js.Global().Set("routingPolicy", js.FuncOf(func(_ js.Value, args []js.Value) any {
		action := "get"
		if len(args) > 0 {
			action = args[0].String()
		}
		switch action {
		case "list":
			names := preset.Names()
			out := make([]any, 0, len(names)+1)
			out = append(out, "none")
			for _, n := range names {
				out = append(out, n)
			}
			return js.ValueOf(out)
		case "set":
			if len(args) > 1 {
				return hook.set(args[1].String())
			}
			return hook.current()
		default: // "get" / current
			return hook.current()
		}
	}))
}
