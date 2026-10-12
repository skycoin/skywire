// Package visorconfig pkg/visor/visorconfig/deployment_services.go c3-vis-core
package visorconfig

import (
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
)

// ApplyDeploymentServices updates the deployment-managed fields of v1 from
// next, the deployment's current services config, and returns the JSON
// names of the fields it changed.
//
// A service address or the STUN list is deployment-managed when the config
// holds nothing, the value prev held (the services config the visor last
// applied), or the embedded deployment default. Anything else was set by
// the operator and is left alone, so pointing one service at your own
// instance survives a refresh. An empty field is filled only when next
// differs from the embedded default, since empty already resolves to it.
//
// The deployment key sets (route_setup_nodes, transport_setup,
// survey_whitelist) are replaced outright, as before: operators' own keys
// live in the user_* fields beside them and are merged at use time.
//
// prev may be nil, meaning nothing has been applied yet.
func (v1 *V1) ApplyDeploymentServices(next, prev *Services) []string {
	if next == nil {
		return nil
	}
	if prev == nil {
		prev = &Services{}
	}
	emb := &deployment.Prod

	v1.mu.Lock()
	defer v1.mu.Unlock()

	var changed []string
	str := func(name string, cur *string, nextV, prevV, embV string) {
		if nextV == "" {
			return
		}
		eff := *cur
		if eff == "" {
			eff = embV
		}
		if eff == nextV {
			return
		}
		if *cur == "" || *cur == prevV || *cur == embV {
			*cur = nextV
			changed = append(changed, name)
		}
	}
	keys := func(name string, cur *[]cipher.PubKey, nextV []cipher.PubKey) {
		if len(nextV) == 0 || sameKeys(*cur, nextV) {
			return
		}
		*cur = nextV
		changed = append(changed, name)
	}

	if d := v1.Dmsg; d != nil {
		str("dmsg.discovery", &d.Discovery, next.DmsgDiscovery, prev.DmsgDiscovery, emb.DmsgDiscovery)
		str("dmsg.discovery_dmsg", &d.DiscoveryDmsg, next.DmsgDiscoveryDmsg, prev.DmsgDiscoveryDmsg, emb.DmsgDiscoveryDmsg)
	}
	if t := v1.Transport; t != nil {
		str("transport.discovery", &t.Discovery, next.TransportDiscovery, prev.TransportDiscovery, emb.TransportDiscovery)
		str("transport.discovery_dmsg", &t.DiscoveryDmsg, next.TransportDiscoveryDmsg, prev.TransportDiscoveryDmsg, emb.TransportDiscoveryDmsg)
		str("transport.address_resolver", &t.AddressResolver, next.AddressResolver, prev.AddressResolver, emb.AddressResolver)
		str("transport.address_resolver_dmsg", &t.AddressResolverDmsg, next.AddressResolverDmsg, prev.AddressResolverDmsg, emb.AddressResolverDmsg)
		keys("transport.transport_setup", &t.TransportSetupPKs, next.TransportSetupPKs)
	}
	if r := v1.Routing; r != nil {
		str("routing.route_finder", &r.RouteFinder, next.RouteFinder, prev.RouteFinder, emb.RouteFinder)
		str("routing.route_finder_dmsg", &r.RouteFinderDmsg, next.RouteFinderDmsg, prev.RouteFinderDmsg, emb.RouteFinderDmsg)
		keys("routing.route_setup_nodes", &r.RouteSetupNodes, next.RouteSetupNodes)
	}
	if l := v1.Launcher; l != nil {
		str("launcher.service_discovery", &l.ServiceDisc, next.ServiceDiscovery, prev.ServiceDiscovery, emb.ServiceDiscovery)
		str("launcher.service_discovery_dmsg", &l.ServiceDiscDmsg, next.ServiceDiscoveryDmsg, prev.ServiceDiscoveryDmsg, emb.ServiceDiscoveryDmsg)
	}
	str("reward_system", &v1.RewardSystem, next.RewardSystem, prev.RewardSystem, emb.RewardSystem)
	str("reward_system_dmsg", &v1.RewardSystemDmsg, next.RewardSystemDmsg, prev.RewardSystemDmsg, emb.RewardSystemDmsg)
	str("conf_service_dmsg", &v1.ConfServiceDmsg, next.ConfDmsg, prev.ConfDmsg, emb.ConfDmsg)
	str("geoip", &v1.GeoIP, next.GeoIP, prev.GeoIP, emb.GeoIP)

	if len(next.StunServers) > 0 && !sameStrings(v1.StunServers, next.StunServers) &&
		(len(v1.StunServers) == 0 || sameStrings(v1.StunServers, prev.StunServers) ||
			sameStrings(v1.StunServers, emb.StunServers)) {
		v1.StunServers = next.StunServers
		changed = append(changed, "stun_servers")
	}

	// The phone deliberately generates an empty survey whitelist; refreshing
	// it there would put back what generation left out.
	if UseDeploymentSurveyWhitelist() {
		keys("survey_whitelist", &v1.SurveyWhitelist, next.SurveyWhitelist)
	}

	return changed
}

// sameKeys reports whether a and b hold the same set of keys.
func sameKeys(a, b []cipher.PubKey) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[cipher.PubKey]struct{}, len(a))
	for _, k := range a {
		set[k] = struct{}{}
	}
	for _, k := range b {
		if _, ok := set[k]; !ok {
			return false
		}
	}
	return true
}

// sameStrings reports whether a and b hold the same strings in order.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
