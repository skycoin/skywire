//go:build tinygo

// Package router pkg/router/router_source_tinygo.go
//
// TinyGo (browser/wasm) stubs for the route-SOURCE side of the router.
//
// An edge-only browser visor receives routes (the rule-reception RPC server +
// packet forwarding compile and run under TinyGo) but does not initiate route
// setup: route finding (rfclient over net/http), the RSN/setup-node clients,
// the cascade build, the client pool, and mux establishment all pull
// net/http / net/rpc-shaped machinery that is //go:build !tinygo. The methods
// and helpers those files provide on native builds are stubbed here so the
// forwarding-plane files that reference them still compile, and *router keeps
// satisfying the Router interface.
//
// Every stub fails closed: a browser tab cannot originate a multi-hop route.
package router

import (
	"context"
	"errors"
	"net"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// errRouteSetupUnsupported is returned by every route-source stub.
var errRouteSetupUnsupported = errors.New("router: route setup/dialing is not supported on this build (edge-only TinyGo)")

// DialRoutes — Router interface method; route origination is source-only.
func (r *router) DialRoutes(_ context.Context, _ cipher.PubKey, _, _ routing.Port, _ *DialOptions) (net.Conn, error) {
	return nil, errRouteSetupUnsupported
}

// PingRoute — Router interface method; source-only.
func (r *router) PingRoute(_ context.Context, _ cipher.PubKey, _, _ routing.Port, _ *DialOptions) (net.Conn, error) {
	return nil, errRouteSetupUnsupported
}

// AddMuxRouteByHops — Router interface method; mux establishment is source-only.
func (r *router) AddMuxRouteByHops(_ routing.RouteDescriptor, _, _ []routing.Hop) error {
	return errRouteSetupUnsupported
}

// GrowMuxRoute — Router interface method; source-only.
func (r *router) GrowMuxRoute(_ routing.RouteDescriptor, _, _ int) (int, error) {
	return 0, errRouteSetupUnsupported
}

// RemoveMuxRouteByTransport — Router interface method; source-only.
func (r *router) RemoveMuxRouteByTransport(_ routing.RouteDescriptor, _ uuid.UUID) error {
	return errRouteSetupUnsupported
}

// establishMuxRoutes is a no-op on the edge build (mux legs are added by the
// route source, not by a receiving edge). Callers in the data plane treat it
// as best-effort.
func (r *router) establishMuxRoutes(_ context.Context, _ *NoiseRouteGroup, _ *DialOptions, _ routing.RouteDescriptor, _ uuid.UUID) {
}

// calculateLocalRoutes is source-only (it queries the route finder / local
// topology to plan a multi-hop path).
func (r *router) calculateLocalRoutes(_ context.Context, _ *logging.Logger, _, _ cipher.PubKey, _ *DialOptions) (fwd, rev []routing.Hop, err error) {
	return nil, nil, errRouteSetupUnsupported
}

// buildHopLookups is source-only; the edge never plans routes, so the lookups
// it would feed are unused. Return identity-ish stubs.
func (r *router) buildHopLookups(_ context.Context, _, _ [][]routing.Hop) (latencyFor func(uuid.UUID) float64, typeFor func(uuid.UUID) string) {
	return func(uuid.UUID) float64 { return 0 }, func(uuid.UUID) string { return "" }
}

// stubRouteGroupDialer is the TinyGo RouteGroupDialer: it cannot dial a route
// group (that requires the setup-node client, which is native-only).
type stubRouteGroupDialer struct{}

// Dial reports that route-group dialing is unavailable on this build.
func (stubRouteGroupDialer) Dial(_ context.Context, _ *logging.Logger, _ *dmsg.Client, _ []cipher.PubKey, _ routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
	return routing.EdgeRules{}, cipher.PubKey{}, errRouteSetupUnsupported
}

// NewSetupNodeDialer returns the edge-only stub dialer. The native
// implementation (wrappers.go) builds a real setup-node-backed dialer.
func NewSetupNodeDialer() RouteGroupDialer { return stubRouteGroupDialer{} }
