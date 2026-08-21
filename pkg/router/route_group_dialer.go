// Package router pkg/router/route_group_dialer.go c2-net-routing
package router

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

//go:generate mockery --name RouteGroupDialer --case underscore --inpackage

// RouteGroupDialer is an interface for RouteGroup dialers.
//
// It lives in this net/http-free, untagged file (separate from the
// route-setup-side implementations in wrappers.go, which are //go:build
// !tinygo) so the Config field that references it compiles under the TinyGo
// js/wasm target. On TinyGo, NewSetupNodeDialer() returns a stub whose Dial
// reports that route dialing is unavailable (an edge-only browser visor
// receives routes but does not initiate route setup).
type RouteGroupDialer interface {
	Dial(
		ctx context.Context,
		log *logging.Logger,
		dmsgC *dmsg.Client,
		setupNodes []cipher.PubKey,
		req routing.BidirectionalRoute,
	) (routing.EdgeRules, cipher.PubKey, error) // Returns rules and the connected setup node PK
}

// forceLegacyCtxKey carries a per-dial request to skip the source-driven
// cascade and use the classic trusted-setup-node path (the RSN dials each
// hop, including the destination, over dmsg). It is threaded through the dial
// context rather than DialOptions so it can be flipped on mid-retry inside
// DialRoutes without touching the RouteGroupDialer interface.
type forceLegacyCtxKey struct{}

// WithForceLegacyRouteSetup returns a context that instructs a
// cascade-capable RouteGroupDialer to bypass the source-driven cascade and use
// the classic setup-node path for this dial.
//
// Rationale: the source-driven cascade installs rules at the destination over
// transports, verified by the destination's CascadeHandler via the RSN
// signature. A destination that does NOT run cascade route-setup only trusts
// the classic setup nodes (route_setup_nodes) and rejects a cascade-installed
// route — the route group's reciprocal handshake then never completes and the
// dial fails. The classic path routes setup through an RSN the destination
// already trusts, so escalating to it recovers the dial against a mixed fleet.
func WithForceLegacyRouteSetup(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceLegacyCtxKey{}, true)
}

// forceLegacyRouteSetup reports whether ctx requests the classic setup-node
// path (see WithForceLegacyRouteSetup).
func forceLegacyRouteSetup(ctx context.Context) bool {
	v, _ := ctx.Value(forceLegacyCtxKey{}).(bool)
	return v
}
