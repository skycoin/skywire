// Package visor pkg/visor/embedded_resolver_local.go c3-vis-core
//
// Publishing a resolving proxy as a LOCAL SERVICE: an app on this visor dials
// the visor's own PK on the resolver's port and is served in-process over
// net.Pipe, with no second SOCKS hop on 127.0.0.1 and no port to configure on
// both sides. See pkg/app/appnet/local_service.go for the table itself.
//
// The port is the resolver's own SOCKS5 port (4445, 4446, …), read as a routing
// port. Routing ports are a separate space from TCP ports, so there is no
// collision, and an app that knows which resolver it wants already knows the
// number. Two resolvers cannot share one, because ValidateResolvers already
// refuses a duplicate listener port.
//
// A local service is NOT in pkg/visor's ServiceRegistry, which is what
// handleServerConn dispatches incoming PEER connections from. That distinction
// is the whole point: a resolver answers for this visor's alias table, mints
// leaves from its MITM CA and leaves through its exit, so only this visor's own
// apps may reach it.
package visor

import (
	"math"
	"net"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

// localResolverPublisher is implemented by both embedded resolving proxies, so
// one call wires either kind.
type localResolverPublisher interface {
	setLocalPublish(publish func(svc appnet.LocalService, serve func(net.Conn)), unpublish func(port routing.Port))
}

// wireLocalResolverPublish lets rt publish itself as a local service under this
// visor's own PK — the key an app's own dial carries.
//
// The visor's PK is deliberate even for a dmsg resolver running under a
// SEPARATE identity: an app addresses its own visor, and it knows only the one
// PK its config was built with.
func (v *Visor) wireLocalResolverPublish(rt localResolverPublisher) {
	if rt == nil {
		return
	}
	rt.setLocalPublish(v.publishLocalResolver, v.unpublishLocalResolver)
}

func (v *Visor) publishLocalResolver(svc appnet.LocalService, serve func(net.Conn)) {
	if v.conf == nil || serve == nil {
		return
	}
	appnet.RegisterLocalService(v.conf.PK, svc, serve)
	// A visor assembled without a logger reaches this from a test that drives a
	// real resolver runtime, so the log line cannot assume one.
	if v.log != nil {
		v.log.WithField("port", svc.Port).WithField("service", svc.Label).WithField("suffixes", svc.Suffixes).
			Debug("Published resolving proxy as a local service for this visor's apps")
	}
}

func (v *Visor) unpublishLocalResolver(port routing.Port) {
	if v.conf == nil {
		return
	}
	appnet.UnregisterLocalService(v.conf.PK, port)
}

// setLocalPublish implements localResolverPublisher.
func (e *EmbeddedSkynetWeb) setLocalPublish(publish func(appnet.LocalService, func(net.Conn)), unpublish func(routing.Port)) {
	e.mu.Lock()
	e.publishLocal, e.unpublishLocal = publish, unpublish
	e.mu.Unlock()
}

// setLocalPublish implements localResolverPublisher.
func (e *EmbeddedDmsgWeb) setLocalPublish(publish func(appnet.LocalService, func(net.Conn)), unpublish func(routing.Port)) {
	e.mu.Lock()
	e.publishLocal, e.unpublishLocal = publish, unpublish
	e.mu.Unlock()
}

// localPublishHooks returns the Publish callback to hand the runtime and the
// cleanup to run when it stops, for a resolver serving suffix on port under
// label. Both are nil-safe: an unwired resolver (standalone tests, a visor
// built without the hooks) simply gets no local service, and so does one whose
// configured port is not a port.
func localPublishHooks(publish func(appnet.LocalService, func(net.Conn)), unpublish func(routing.Port), port uint, label, suffix string) (onPublish func(func(net.Conn)), cleanup func()) {
	if publish == nil || port == 0 || port > math.MaxUint16 {
		return nil, func() {}
	}
	svc := appnet.LocalService{Port: routing.Port(port), Label: label} //nolint:gosec // bounded above
	if suffix != "" {
		svc.Suffixes = []string{suffix}
	}
	return func(serve func(net.Conn)) {
			publish(svc, serve)
		}, func() {
			if unpublish != nil {
				unpublish(svc.Port)
			}
		}
}
