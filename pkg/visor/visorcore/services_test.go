package visorcore

import (
	"testing"

	"github.com/skycoin/skywire/deployment"
)

// TestResolveServicesNilMatchesDeployment locks in that ResolveServices(nil)
// returns exactly the deployment defaults — so the wasm-visor's switch from
// reading deployment.Prod.* directly to svc.* is a behavior-preserving refactor.
func TestResolveServicesNilMatchesDeployment(t *testing.T) {
	s := ResolveServices(nil)
	d := deployment.Prod
	if s.DmsgDiscoveryDmsg != d.DmsgDiscoveryDmsg {
		t.Errorf("DmsgDiscoveryDmsg = %q, want %q", s.DmsgDiscoveryDmsg, d.DmsgDiscoveryDmsg)
	}
	if s.TransportDiscoveryDmsg != d.TransportDiscoveryDmsg {
		t.Errorf("TransportDiscoveryDmsg = %q, want %q", s.TransportDiscoveryDmsg, d.TransportDiscoveryDmsg)
	}
	if s.RouteFinderDmsg != d.RouteFinderDmsg {
		t.Errorf("RouteFinderDmsg = %q, want %q", s.RouteFinderDmsg, d.RouteFinderDmsg)
	}
	if len(s.DmsgServers) != len(d.DmsgServers) {
		t.Errorf("DmsgServers len = %d, want %d", len(s.DmsgServers), len(d.DmsgServers))
	}
	if len(s.RouteSetupNodes) != len(d.RouteSetupNodes) {
		t.Errorf("RouteSetupNodes len = %d, want %d", len(s.RouteSetupNodes), len(d.RouteSetupNodes))
	}
	if len(s.StunServers) != len(d.StunServers) {
		t.Errorf("StunServers len = %d, want %d", len(s.StunServers), len(d.StunServers))
	}
	if s.MinHops != 1 {
		t.Errorf("MinHops = %d, want 1 (origination enabled)", s.MinHops)
	}
	// The dmsg service URLs the native visor preloads (dmsgServicePKs) must each
	// match the deployment default for a nil config.
	for _, c := range []struct {
		name, got, want string
	}{
		{"TransportDiscoveryDmsg", s.TransportDiscoveryDmsg, d.TransportDiscoveryDmsg},
		{"AddressResolverDmsg", s.AddressResolverDmsg, d.AddressResolverDmsg},
		{"ServiceDiscoveryDmsg", s.ServiceDiscoveryDmsg, d.ServiceDiscoveryDmsg},
		{"ConfDmsg", s.ConfDmsg, d.ConfDmsg},
		{"UptimeTrackerDmsg", s.UptimeTrackerDmsg, d.UptimeTrackerDmsg},
		{"RewardSystemDmsg", s.RewardSystemDmsg, d.RewardSystemDmsg},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}
