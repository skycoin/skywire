// Package clirpc cmd/skywire-cli/commands/rpc/routingsession.go c4-vis-cli
package clirpc

import (
	"errors"
	"fmt"
	"math"

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
	MuxRoutes  *int    // SetMuxRoutes — raw flag value; 0=unlimited, 1=disabled (skipped), 2+=N
	MuxMode    *string // SetMuxMode — "" / "auto" skipped (router default)
	MinHops    *uint16 // SetMinHops — 0 is rejected (disables routing)
}

// ApplyRoutingSession applies the non-nil options in canonical order via the
// visor RPC. It preserves the exact semantics the proxy-start path established:
//   - mux 0 → MaxInt32 ("unlimited", establishMuxRoutes stops at the discoverable
//     path count); mux 1 → skipped (single-route default); 2+ → N parallel routes.
//   - mux-mode "" / "auto" → skipped (router's own default weighting).
//   - min-hops 0 → error (0 disables routing).
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
	if o.MuxRoutes != nil {
		n := *o.MuxRoutes
		if n == 0 {
			n = math.MaxInt32
		}
		if n > 1 {
			if err := rc.SetMuxRoutes(n); err != nil {
				return fmt.Errorf("failed to set mux routes: %w", err)
			}
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
