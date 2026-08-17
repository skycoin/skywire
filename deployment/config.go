// Package deployment github.com/skycoin/skywire/deployment/config.go
//
// The init() that populates Prod / Test / ProdConf / TestConf lives
// in two build-tag-gated files: config_native.go (//go:build !js)
// unmarshals the embedded services-config.json via encoding/json
// with SKYDEPLOY override support; config_js.go (//go:build js)
// copies from static Go literals in data_static_js.go (generated
// from the same JSON by deployment/internal/gen — see that
// package's doc for the why-and-how). Reason for the split:
// encoding/json pulls reflect.unsafe_New / mapassign / etc. that
// TinyGo's stdlib doesn't provide, which blocked TinyGo from
// compiling the install-page WASM (pkg/skywireconfig/genvisor +
// autoconfigcmd).
package deployment

//go:generate go run ./cmd/gen

import (
	_ "embed"
	"encoding/json"
	"net/url"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

/*
Embedded Deployment Defaults

services-config.json contains the complete deployment configuration including
both HTTP and DMSG endpoints. The _dmsg suffixed fields contain dmsg:// URLs
for the same services, and dmsg_servers lists the DMSG servers for bootstrapping.

Set SKYDEPLOY=/path/to/config.json to override the embedded defaults with a
custom deployment configuration (e.g., for private networks or testing).

*/

// ServicesJSON is the deployment configuration. By default this is the embedded
// services-config.json. If the SKYDEPLOY environment variable is set to a file
// path, that file is loaded instead at init time.
//
//go:embed services-config.json
var ServicesJSON []byte

// EnvServices is the wrapper struct for the outer JSON — i.e. the 'prod' or
// 'test' deployment config. It lives in this untagged file (importing
// encoding/json, which compiles under TinyGo 0.41) so consumers like
// pkg/dmsg/dmsg's InitConfig can unmarshal the embedded config on every target,
// including a TinyGo dmsg client. The native init() in config_native.go and the
// static-literal init() in config_js.go both populate Prod/Test; this type is
// just the shared shape they (and dmsg) decode into.
type EnvServices struct {
	Test json.RawMessage `json:"test"`
	Prod json.RawMessage `json:"prod"`
}

// EnvServices is defined in config_native.go (lives there because
// its json.RawMessage fields need encoding/json, which is
// build-tag-gated off the WASM path). The type is still exported
// for callers under the !js build tag that need to read JSON
// fragments — see config_native.go for the definition.

// DmsgServerEntry represents a DMSG server with its public key and address.
// This is a simplified representation that avoids importing dmsg/disc.
// Use ToDiscEntries() to convert to []*disc.Entry when needed.
type DmsgServerEntry struct {
	Static string `json:"static"`
	Server struct {
		Address string `json:"address"`
		// AddressWS is the optional full ws:// or wss:// WebSocket URL (including
		// the /dmsg path, e.g. "wss://dmsg1.example.net/dmsg") a browser client
		// seeds from. It is SEPARATE from Address (IP:port, TCP): native visors
		// dial Address and never look at AddressWS, so advertising a wss:// domain
		// here adds no DNS/TLS dependency for them. Required for a wasm-visor
		// served over HTTPS, whose browser blocks plain ws:// (mixed content); a
		// wss domain's CA cert is stable, so it lives safely in the embedded config.
		AddressWS string `json:"address_ws,omitempty"`
	} `json:"server"`
}

// HasDmsgServers returns true if the deployment has DMSG server entries.
func (s *Services) HasDmsgServers() bool {
	return len(s.DmsgServers) > 0
}

// IsKnownDmsgServer reports whether pk is one of the deployment's known
// (official) dmsg servers. Used to gate fleet-wide defaults — e.g. the
// WSSDomainSuffix — to servers this deployment actually runs DNS/TLS for, so a
// third party running the same binary doesn't self-advertise the deployment's
// wss domain for a PK that has no DNS record.
func (s *Services) IsKnownDmsgServer(pk cipher.PubKey) bool {
	for i := range s.DmsgServers {
		var spk cipher.PubKey
		if spk.Set(s.DmsgServers[i].Static) == nil && spk == pk {
			return true
		}
	}
	return false
}

// DmsgServerEntriesToDisc converts a list of DmsgServerEntry to
// []*disc.Entry suitable for direct.NewClient / direct.GetAllEntries.
// Entries whose Static field is not a valid public key are skipped
// silently — the deployment file is the source of truth and a single
// malformed entry should not abort the whole preload.
func DmsgServerEntriesToDisc(in []DmsgServerEntry) []*disc.Entry {
	if len(in) == 0 {
		return nil
	}
	entries := make([]*disc.Entry, 0, len(in))
	for _, srv := range in {
		var pk cipher.PubKey
		if err := pk.Set(srv.Static); err != nil {
			continue
		}
		entries = append(entries, &disc.Entry{
			Static: pk,
			Server: &disc.Server{Address: srv.Server.Address},
		})
	}
	return entries
}

// ToDiscEntries is the method form of DmsgServerEntriesToDisc, scoped
// to the receiver's DmsgServers list.
func (s *Services) ToDiscEntries() []*disc.Entry {
	return DmsgServerEntriesToDisc(s.DmsgServers)
}

// HasDmsgEndpoints returns true if the deployment has DMSG service endpoints.
func (s *Services) HasDmsgEndpoints() bool {
	return s.DmsgDiscoveryDmsg != ""
}

// pkFromDmsgURL extracts the public key from a `dmsg://<pk>:<port>` URL.
// Returns the zero PK on empty / malformed / non-PK input.
func pkFromDmsgURL(raw string) cipher.PubKey {
	if raw == "" {
		return cipher.PubKey{}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return cipher.PubKey{}
	}
	var pk cipher.PubKey
	if pk.Set(u.Hostname()) != nil {
		return cipher.PubKey{}
	}
	return pk
}

// EmbeddedServersForDiscoveryDmsg returns the embedded dmsg-server transit
// entries belonging to the embedded deployment whose dmsg-discovery matches
// discoveryDmsgURL (compared by public key), or nil when nothing matches or the
// URL is empty/malformed.
//
// This lets a dmsg-server omit its `servers` list yet still bootstrap, by
// borrowing the binary's embedded server set — but ONLY for a recognized
// discovery, so transit servers stay coupled to the discovery they belong to.
// An unknown discovery yields nil (the server keeps an empty list), never a
// mismatched set from a different deployment.
func EmbeddedServersForDiscoveryDmsg(discoveryDmsgURL string) []*disc.Entry {
	return EmbeddedServersForDiscoveryPK(pkFromDmsgURL(discoveryDmsgURL))
}

// EmbeddedServersForDiscoveryPK is EmbeddedServersForDiscoveryDmsg keyed by an
// already-parsed discovery public key, for callers that resolved the PK before
// reaching here. The zero key matches nothing.
//
// The coupling this enforces is the whole point: embedded servers are only ever
// handed out for the discovery they were published alongside. A caller holding a
// private or test deployment's discovery gets nil rather than the production
// fleet, so no deployment silently dials servers belonging to another one.
func EmbeddedServersForDiscoveryPK(want cipher.PubKey) []*disc.Entry {
	if (want == cipher.PubKey{}) {
		return nil
	}
	for _, s := range []Services{Prod, Test} {
		if len(s.DmsgServers) == 0 {
			continue
		}
		if pkFromDmsgURL(s.DmsgDiscoveryDmsg) == want {
			return s.ToDiscEntries()
		}
	}
	return nil
}

// Services are URLs, IP addresses, and public keys of the skywire services as deployed.
// HTTP fields contain plain HTTP URLs, _dmsg fields contain dmsg:// URLs for the same services.
type Services struct {
	// HTTP endpoints
	DmsgDiscovery      string          `json:"dmsg_discovery,omitempty"`
	TransportDiscovery string          `json:"transport_discovery,omitempty"`
	AddressResolver    string          `json:"address_resolver,omitempty"`
	RouteFinder        string          `json:"route_finder,omitempty"`
	RouteSetupNodes    []cipher.PubKey `json:"route_setup_nodes,omitempty"`
	TransportSetupPKs  []cipher.PubKey `json:"transport_setup,omitempty"`
	UptimeTracker      string          `json:"uptime_tracker,omitempty"`
	ServiceDiscovery   string          `json:"service_discovery,omitempty"`
	StunServers        []string        `json:"stun_servers,omitempty"`
	DNSServer          string          `json:"dns_server,omitempty"`
	GeoIP              string          `json:"geoip,omitempty"` // HTTP only (dmsg doesn't preserve client IP)
	SurveyWhitelist    []cipher.PubKey `json:"survey_whitelist,omitempty"`
	// DMSG endpoints (dmsg:// URLs for the same services)
	ConfDmsg    string            `json:"conf_dmsg,omitempty"`
	DmsgServers []DmsgServerEntry `json:"dmsg_servers,omitempty"`
	// WSSDomainSuffix is the deployment-wide DNS suffix the official dmsg servers
	// self-derive their wss:// AddressWS from — "wss://<DNSLabel(PK)>.<suffix>/dmsg"
	// — so an HTTPS-served browser wasm-visor reaches them without mixed content.
	// One string for the whole fleet (each server self-labels from its own PK), so
	// the domain lives in exactly one place (this file) instead of every dmsg
	// server's local config. A dmsg server applies it only when its own PK is a
	// known fleet server (IsKnownDmsgServer) — a third party running this binary
	// never mis-advertises this domain for a PK with no DNS record.
	WSSDomainSuffix string `json:"wss_domain_suffix,omitempty"`
	// BrowseOriginSuffix is the deployment-wide domain the "real-origin" browse
	// path (pkg/visor/meshproxy.go, the wasm SW browse origin, and the hosted
	// Caddy front) serves untrusted mesh content under — e.g. ".haltingstate.net"
	// (a SEPARATE eTLD+1 from the visor app on WSSDomainSuffix, so untrusted
	// browsed content is cookie/origin-isolated from the visor identity). Lives
	// here so the domain is defined in exactly one place instead of being
	// hardcoded across the serve flags / config-gen / docs; consumers read it via
	// deployment.Prod.BrowseOriginSuffix. Empty (the local default) means the
	// browse origin uses ".mesh.localhost" (loopback, secure-context, no cert).
	BrowseOriginSuffix     string `json:"browse_origin_suffix,omitempty"`
	DmsgDiscoveryDmsg      string `json:"dmsg_discovery_dmsg,omitempty"`
	TransportDiscoveryDmsg string `json:"transport_discovery_dmsg,omitempty"`
	AddressResolverDmsg    string `json:"address_resolver_dmsg,omitempty"`
	RouteFinderDmsg        string `json:"route_finder_dmsg,omitempty"`
	UptimeTrackerDmsg      string `json:"uptime_tracker_dmsg,omitempty"`
	ServiceDiscoveryDmsg   string `json:"service_discovery_dmsg,omitempty"`
	// Reward system
	RewardSystem     string `json:"reward_system,omitempty"`
	RewardSystemDmsg string `json:"reward_system_dmsg,omitempty"`
}

// BackfillClearnetFromDmsg copies each *_dmsg deployment-service URL into its
// historically-clearnet sibling field when that field is empty. services-config.json
// no longer carries plain-HTTP deployment URLs (only geoip — the one allowed clearnet
// deployment endpoint, since dmsg can't preserve the client IP a geoip lookup needs),
// so the HTTP-named fields would otherwise be empty. In a dmsg-only deployment the
// correct value for them IS the dmsg:// URL, so every existing consumer of the
// HTTP-named fields keeps working unchanged — getHTTPClient builds a dmsghttp client
// for the dmsg:// scheme. The single source of truth stays services-config.json; this
// derives, it does not hardcode. GeoIP is deliberately left untouched.
func (s *Services) BackfillClearnetFromDmsg() {
	if s.DmsgDiscovery == "" {
		s.DmsgDiscovery = s.DmsgDiscoveryDmsg
	}
	if s.TransportDiscovery == "" {
		s.TransportDiscovery = s.TransportDiscoveryDmsg
	}
	if s.AddressResolver == "" {
		s.AddressResolver = s.AddressResolverDmsg
	}
	if s.RouteFinder == "" {
		s.RouteFinder = s.RouteFinderDmsg
	}
	if s.UptimeTracker == "" {
		s.UptimeTracker = s.UptimeTrackerDmsg
	}
	if s.ServiceDiscovery == "" {
		s.ServiceDiscovery = s.ServiceDiscoveryDmsg
	}
	if s.RewardSystem == "" {
		s.RewardSystem = s.RewardSystemDmsg
	}
}

// Conf is the configuration URL for the deployment which may be fetched on `skywire cli config gen`
type Conf struct {
	Conf string `json:"conf,omitempty"`
}

// Prod is the production deployment services
var Prod Services

// ProdConf is the service configuration address / URL for the skywire production deployment
var ProdConf Conf

// Test is the test deployment services
var Test Services

// TestConf is the service configuration address / URL for the skywire test deployment
var TestConf Conf

// init() that populates Prod / Test / ProdConf / TestConf lives in
// the build-tag-gated config_native.go (!js, uses encoding/json
// + SKYDEPLOY override) and config_js.go (js, copies from the
// generated static literals). See the package doc above for the
// rationale on the split.
