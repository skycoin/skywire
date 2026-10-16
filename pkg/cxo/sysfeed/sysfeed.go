// Package sysfeed pkg/cxo/sysfeed/sysfeed.go c2-net-cxo
//
// Leaf names a visor uses for the registrations it carries on its system feed
// (dmsg port 50, the telemetry feed TPD aggregates). A deployment that runs
// dmsg discovery, the address resolver or service discovery in the same
// process as the transport discovery lists them in Services.FeedIngest, and
// the visor then publishes their registrations here instead of on a CXO feed
// of their own. See docs/design/cxo-one-feed-per-visor.md.
//
// The leaves sit at the top of the Root, next to "tp-list", so the transport
// discovery's targeted fetch reads them from the same few objects.
package sysfeed

import "strings"

// Prefix starts every registration leaf on the system feed.
const Prefix = "reg."

// Prefixes of each service's leaves. A service's leaf is its prefix followed
// by the name it uses on its own feed, so "entry" on the dmsg discovery feed
// is "reg.dmsgd.entry" here.
const (
	DmsgdPrefix = Prefix + "dmsgd."
	ARPrefix    = Prefix + "ar."
	SDPrefix    = Prefix + "sd."
)

// Names of the services in deployment.Services.FeedIngest, the JSON names of
// the services' own fields.
const (
	DmsgDiscovery    = "dmsg_discovery"
	AddressResolver  = "address_resolver"
	ServiceDiscovery = "service_discovery"
)

// PrefixOf returns the leaf prefix of the named service, or "".
func PrefixOf(service string) string {
	switch service {
	case DmsgDiscovery:
		return DmsgdPrefix
	case AddressResolver:
		return ARPrefix
	case ServiceDiscovery:
		return SDPrefix
	}
	return ""
}

// IsRegistration reports whether a top-level leaf name is a registration leaf.
func IsRegistration(name string) bool { return strings.HasPrefix(name, Prefix) }
