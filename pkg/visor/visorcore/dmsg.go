// Package visorcore pkg/visor/visorcore/dmsg.go c3-vis-core
package visorcore

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// DmsgServicePKs returns the dmsg public keys of the deployment services that
// run as dmsg DIRECT clients — dmsg-discovery, transport-discovery, address-
// resolver, route-finder, service-discovery, config, uptime-tracker — parsed
// from their `dmsg://<pk>:<port>` service URLs (empty/unparseable entries are
// skipped).
//
// These services never publish to the dmsg-discovery, so a visor MUST seed
// their entries into its dmsg client's cache (native: seedDmsgServiceEntries;
// wasm: StartDmsgSeeded's servicePKs) or a DialStream to them 404s ("entry is
// not found in discovery"). Both the native visor and the wasm edge derive the
// set here, from the same visorcore.Services, so they can't drift on it — the
// omission that once left the wasm edge's network-view / services-health /
// uptime aggregation empty while the native visor worked.
//
// Route-setup nodes are NOT included: they are seeded separately and the two
// shells differ on whether they add them here (the wasm edge appends
// svc.RouteSetupNodes after this call). Keep that decision at the call site.
func DmsgServicePKs(svc Services) cipher.PubKeys {
	dmsgURLs := []string{
		svc.DmsgDiscoveryDmsg,
		svc.TransportDiscoveryDmsg,
		svc.AddressResolverDmsg,
		svc.RouteFinderDmsg,
		svc.ServiceDiscoveryDmsg,
		svc.ConfDmsg,
		svc.UptimeTrackerDmsg,
	}
	var pks cipher.PubKeys
	for _, rawURL := range dmsgURLs {
		if rawURL == "" {
			continue
		}
		trimmed := rawURL
		if len(trimmed) > 7 && trimmed[:7] == "dmsg://" {
			trimmed = trimmed[7:]
		}
		var addr dmsg.Addr
		if err := addr.Set(trimmed); err != nil {
			continue
		}
		pks = append(pks, addr.PK)
	}
	return pks
}
