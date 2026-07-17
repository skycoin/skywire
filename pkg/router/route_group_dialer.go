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
