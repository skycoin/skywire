// Package clirpc cmd/skywire-cli/commands/rpc/routingsession.go c4-vis-cli
package clirpc

import (
	"errors"
	"fmt"

	"github.com/skycoin/skywire/pkg/visor"
)

// RoutingSessionOpts carries the routing-session options shared by the app-start
// surfaces (`cli proxy start`, `cli vpn start`, `cli visor app start`). Every
// field is a pointer so the caller applies ONLY what it wants set — a nil field
// leaves that visor setting untouched. This lets each command keep its own
// flag/Changed semantics while the actual RPC sequence (and its quirks) lives in
// exactly one place, so the three surfaces can't drift.
type RoutingSessionOpts struct {
	ExistingTP *bool   // SetExistingTPOnly
	LocalRoute *bool   // SetForceLocalRoutes
	MuxMode    *string // SetMuxMode — "" / "auto" skipped (router default)
	MinHops    *uint16 // SetMinHops — 0 is rejected (disables routing)
}

// ApplyRoutingSession applies the non-nil options in canonical order via the
// visor RPC:
//   - mux-mode "" / "auto" → skipped (router's own default weighting).
//   - min-hops 0 → error (0 disables routing).
//
// There is deliberately no route-COUNT option here. It used to set a visor-wide
// mux_routes, which was wrong twice over. Mechanically, a per-invocation flag
// mutated persistent visor state (router runtime, the live networker's dial
// default, and a config flush), so `proxy start --mux 2` left every later plain
// `proxy start` on that visor silently multiplexed, across restarts. Worse, the
// visor-global default was then applied to every dial including --direct ones,
// which suppressed the 0-hop AppDirectMux shortcut and pushed dials that should
// never touch route setup through the setup node.
//
// The deeper reason is that a route count is not a static property to configure
// at all. How many routes a flow should hold depends on its bandwidth demand and
// on which transports and intermediates exist between those two visors at that
// moment — and transports come and go. That is a decision for the routing
// policy, which already computes a live degree and holds warm standby legs
// (preset.AdaptRevActive()+AdaptStandbyMax(), re-capped each tick via
// router.SelfHealTargeter). A fixed dial-time number fought it: the self-heal
// storm toward a stale dial-time degree is exactly that conflict. Reshape a live
// session with `cli proxy mux width/standby` instead.
func ApplyRoutingSession(rc visor.API, o RoutingSessionOpts) error {
	if o.ExistingTP != nil {
		if err := rc.SetExistingTPOnly(*o.ExistingTP); err != nil {
			return fmt.Errorf("failed to set existing-transport-only mode: %w", err)
		}
	}
	if o.LocalRoute != nil {
		if err := rc.SetForceLocalRoutes(*o.LocalRoute); err != nil {
			return fmt.Errorf("failed to set force-local-routes mode: %w", err)
		}
	}
	if o.MuxMode != nil && *o.MuxMode != "" && *o.MuxMode != "auto" {
		if err := rc.SetMuxMode(*o.MuxMode); err != nil {
			return fmt.Errorf("failed to set mux mode: %w", err)
		}
	}
	if o.MinHops != nil {
		if *o.MinHops == 0 {
			return errors.New("--min-hops=0 disables routing; pick at least 1")
		}
		if err := rc.SetMinHops(*o.MinHops); err != nil {
			return fmt.Errorf("failed to set min-hops: %w", err)
		}
	}
	return nil
}
