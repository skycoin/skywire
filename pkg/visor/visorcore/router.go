// Package visorcore pkg/visor/visorcore/router.go c3-vis-core
package visorcore

import (
	"context"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// RouterDeps is the platform-neutral input to BuildRouter: the router.Config
// fields plus the dmsg client and setup hooks. The caller builds the platform-
// specific pieces (the route-finder client over its own HTTP/dmsg transport, the
// setup-node dialer with or without an embedded RSN, and — native only — the
// routing-policy DialHook / AppLookup / setup hooks); BuildRouter owns the
// Config field mapping + New + Serve so the two visors can't drift on it.
//
// Zero values for the optional native-only fields (AppLookup, DialHook,
// RulesGCInterval, SetupHooks) are correct for a browser edge.
type RouterDeps struct {
	DmsgC              *dmsg.Client
	PubKey             cipher.PubKey
	SecKey             cipher.SecKey
	TransportManager   *transport.Manager
	RouteFinder        rfclient.Client
	RouteGroupDialer   router.RouteGroupDialer
	SetupNodes         []cipher.PubKey
	MinHops            uint16
	AwaitSetupListener *dmsg.Listener
	Logger             *logging.Logger
	MasterLogger       *logging.MasterLogger

	AppLookup       func(routing.Port) (string, bool)
	DialHook        router.DialHook
	RulesGCInterval time.Duration
	SetupHooks      []router.RouteSetupHook
}

// BuildRouter assembles router.Config from deps, creates the router, and starts
// it serving under serveCtx. router.Serve starts its goroutines and returns
// immediately, so its error is checked synchronously here — the browser edge
// previously fired it in a `go r.Serve(ctx)` goroutine that discarded the error.
// One place owns the Config field mapping + New + Serve, so the native visor and
// the wasm edge cannot drift on it (e.g. the MinHops==0 ⇒ "routing disabled"
// gotcha, or a newly-added Config field reaching only one of the two).
func BuildRouter(serveCtx context.Context, deps RouterDeps) (router.Router, error) {
	rConf := &router.Config{
		Logger:             deps.Logger,
		MasterLogger:       deps.MasterLogger,
		PubKey:             deps.PubKey,
		SecKey:             deps.SecKey,
		TransportManager:   deps.TransportManager,
		RouteFinder:        deps.RouteFinder,
		RouteGroupDialer:   deps.RouteGroupDialer,
		SetupNodes:         deps.SetupNodes,
		MinHops:            deps.MinHops,
		AwaitSetupListener: deps.AwaitSetupListener,
		AppLookup:          deps.AppLookup,
		DialHook:           deps.DialHook,
		RulesGCInterval:    deps.RulesGCInterval,
	}
	r, err := router.New(deps.DmsgC, rConf, deps.SetupHooks)
	if err != nil {
		return nil, err
	}
	if err := r.Serve(serveCtx); err != nil {
		return nil, err
	}
	return r, nil
}
