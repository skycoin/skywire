// Package rfclient pkg/rfclient/types.go c2-net-routing
package rfclient

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/routing"
)

//go:generate mockery --name Client --case underscore --inpackage

// ErrTransportNotFound is returned when transport is not found.
var ErrTransportNotFound = errors.New("transport not found")

// RouteOptions represents options for FindRoutesRequest
type RouteOptions struct {
	MinHops uint16
	MaxHops uint16
	// NumRoutes is the desired number of distinct routes per edge the
	// finder should return. Zero means "use the service default"
	// (maxNumberOfRoutes). Callers requesting a multiplexed dial set
	// this to the mux degree (+ headroom) so the finder returns enough
	// disjoint legs instead of the hard-coded default — without it the
	// finder caps at 3 routes and any --forward-mux/--reverse-mux above
	// that silently degrades. See router.findRouteOptions.
	NumRoutes uint16
}

// FindRoutesRequest parses json body for /routes endpoint request
type FindRoutesRequest struct {
	Edges []routing.PathEdges
	Opts  *RouteOptions
}

// HTTPResponse represents http response struct
type HTTPResponse struct {
	Error *HTTPError  `json:"error,omitempty"`
	Data  interface{} `json:"data,omitempty"`
}

// HTTPError is included in an HTTPResponse
type HTTPError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// Client implements route finding operations.
//
// The interface and its request/response types live here, free of net/http,
// so consumers (notably pkg/router) compile under the TinyGo js/wasm target
// where net/http is unavailable. The HTTP implementation (NewHTTP) lives in
// the build-tagged client.go and is native-only.
type Client interface {
	FindRoutes(ctx context.Context, rts []routing.PathEdges, opts *RouteOptions) (map[routing.PathEdges][][]routing.Hop, error)
}
