// Package visorcore holds the platform-neutral visor assembly shared by the
// native visor (pkg/visor) and the browser wasm-visor (cmd/wasm-visor), so the
// two stop diverging on how a visor is wired from its subsystems. It must stay
// LEAN — it may import pkg/dmsg, pkg/transport, pkg/router, pkg/app/appserver,
// deployment and visorconfig, but NOT pkg/visor itself (which pulls in the OS
// launcher / dmsgpty / HTTP-API code that does not compile under js/wasm). A
// `GOOS=js GOARCH=wasm go build ./pkg/visor/visorcore/` gate enforces that.
//
// See docs/visor-core-convergence.md for the full design + incremental plan.
package visorcore

import (
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Services is the resolved set of deployment service endpoints the subsystems
// need, with both the dmsg-HTTP ("…Dmsg") and clearnet-HTTP variants. It is the
// single shape both shells build their subsystems from — produced by exactly one
// merge function (ResolveServices), so config sourcing cannot drift.
type Services struct {
	// dmsg
	DmsgServers       []deployment.DmsgServerEntry // seed/embedded server set
	DmsgDiscovery     string                       // clearnet dmsg-discovery (native)
	DmsgDiscoveryDmsg string                       // dmsg://… dmsg-discovery (browser / dmsg-only)
	// transport discovery
	TransportDiscovery     string
	TransportDiscoveryDmsg string
	// address resolver
	AddressResolver     string
	AddressResolverDmsg string
	// routing
	RouteFinder     string
	RouteFinderDmsg string
	RouteSetupNodes []cipher.PubKey
	MinHops         uint16
	// service discovery
	ServiceDiscovery     string
	ServiceDiscoveryDmsg string
	// config-bootstrap service
	Conf     string
	ConfDmsg string
	// uptime tracker
	UptimeTracker     string
	UptimeTrackerDmsg string
	// misc
	StunServers []string
	GeoIP       string // HTTP-only (dmsg doesn't preserve client IP); no config override
}

func pick(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// ResolveServices merges an operator config (v1, nullable) over the embedded
// deployment defaults (deployment.Prod) — the single place the
// "operator-override-else-deployment-default" rule lives. The native visor's
// scattered pick() calls across init_*.go collapse onto this; the browser visor
// passes v1 == nil to get pure deployment defaults.
//
// MinHops defaults to 1 (origination enabled; a direct transport still downgrades
// to a 0-intermediate-hop path) so an edge can SET UP routes; the router treats
// MinHops==0 as "routing disabled".
func ResolveServices(v1 *visorconfig.V1) Services {
	d := deployment.Prod
	s := Services{
		DmsgServers:            d.DmsgServers,
		DmsgDiscovery:          d.DmsgDiscovery,
		DmsgDiscoveryDmsg:      d.DmsgDiscoveryDmsg,
		TransportDiscovery:     d.TransportDiscovery,
		TransportDiscoveryDmsg: d.TransportDiscoveryDmsg,
		AddressResolver:        d.AddressResolver,
		AddressResolverDmsg:    d.AddressResolverDmsg,
		RouteFinder:            d.RouteFinder,
		RouteFinderDmsg:        d.RouteFinderDmsg,
		RouteSetupNodes:        d.RouteSetupNodes,
		MinHops:                1,
		ServiceDiscovery:       d.ServiceDiscovery,
		ServiceDiscoveryDmsg:   d.ServiceDiscoveryDmsg,
		// Conf has no clearnet deployment default (deployment is dmsg-only for the
		// config-bootstrap service); a v1.ConfService override below still applies.
		ConfDmsg:          d.ConfDmsg,
		UptimeTracker:     d.UptimeTracker,
		UptimeTrackerDmsg: d.UptimeTrackerDmsg,
		StunServers:       d.StunServers,
		GeoIP:             d.GeoIP,
	}
	if v1 == nil {
		return s
	}
	if v1.Dmsg != nil {
		s.DmsgDiscovery = pick(v1.Dmsg.Discovery, s.DmsgDiscovery)
		s.DmsgDiscoveryDmsg = pick(v1.Dmsg.DiscoveryDmsg, s.DmsgDiscoveryDmsg)
		// NOTE: a v1.Dmsg.Servers override (type-mapped to DmsgServerEntry) is a
		// follow-up; the browser seeds from the deployment set today.
	}
	if v1.Transport != nil {
		s.TransportDiscovery = pick(v1.Transport.Discovery, s.TransportDiscovery)
		s.TransportDiscoveryDmsg = pick(v1.Transport.DiscoveryDmsg, s.TransportDiscoveryDmsg)
		s.AddressResolver = pick(v1.Transport.AddressResolver, s.AddressResolver)
		s.AddressResolverDmsg = pick(v1.Transport.AddressResolverDmsg, s.AddressResolverDmsg)
	}
	if v1.Routing != nil {
		s.RouteFinder = pick(v1.Routing.RouteFinder, s.RouteFinder)
		s.RouteFinderDmsg = pick(v1.Routing.RouteFinderDmsg, s.RouteFinderDmsg)
		if len(v1.Routing.RouteSetupNodes) > 0 || len(v1.Routing.UserRouteSetupNodes) > 0 {
			s.RouteSetupNodes = v1.EffectiveRouteSetupNodes()
		}
		if v1.Routing.MinHops > 0 {
			s.MinHops = v1.Routing.MinHops
		}
	}
	if v1.Launcher != nil {
		s.ServiceDiscovery = pick(v1.Launcher.ServiceDisc, s.ServiceDiscovery)
		s.ServiceDiscoveryDmsg = pick(v1.Launcher.ServiceDiscDmsg, s.ServiceDiscoveryDmsg)
	}
	s.Conf = pick(v1.ConfService, s.Conf)
	s.ConfDmsg = pick(v1.ConfServiceDmsg, s.ConfDmsg)
	if v1.UptimeTracker != nil {
		s.UptimeTracker = pick(v1.UptimeTracker.Addr, s.UptimeTracker)
		s.UptimeTrackerDmsg = pick(v1.UptimeTracker.AddrDmsg, s.UptimeTrackerDmsg)
	}
	if len(v1.StunServers) > 0 {
		s.StunServers = v1.StunServers
	}
	return s
}
