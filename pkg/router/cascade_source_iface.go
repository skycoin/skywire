// Package router pkg/router/cascade_source_iface.go
package router

import "github.com/skycoin/skywire/pkg/routing"

// These interfaces describe the optional source-driven-cascade capabilities a
// RouteGroupDialer may expose. They live in this untagged, net/http-free file
// (apart from the dialer implementations in wrappers.go, which are
// //go:build !tinygo) because router_serve.go type-asserts the configured
// RouteGroupDialer against them at Serve time. On an edge-only TinyGo build the
// dialer is the stub (which implements none of these), so the assertions simply
// fail and the source-driven-cascade wiring is skipped — the receiving cascade
// path (CascadeHandler) is unaffected.

// cascadeSourceProvider is implemented by route-group dialers that own a
// source-side CascadeBuilder. The router uses it at Serve time to share the
// builder's ack registry with the visor's single CascadeHandler.
type cascadeSourceProvider interface {
	CascadeAckRegistry() *ackRegistry
}

// cascadeOriginSetter is implemented by route-group dialers that drive
// source-driven cascade. The router calls SetCascadeOrigin at Serve time to
// hand the dialer the visor's CascadeHandler, which the dialer uses to consume
// the source-addressed outermost cascade layer locally.
type cascadeOriginSetter interface {
	SetCascadeOrigin(p cascadeOriginProcessor)
}

// cascadeOriginProcessor consumes the source-addressed outermost layer of a
// cascade and returns the collected ACK. Implemented by *CascadeHandler.
type cascadeOriginProcessor interface {
	ProcessLocalOrigin(payload []byte) (*routing.CascadeAck, error)
}
