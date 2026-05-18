// Package dmsgserver pkg/dmsgserver/config.go
package dmsgserver

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

const (
	defaultPublicAddress = "127.0.0.1:8081"
	defaultLocalAddress  = ":8081"
	defaultHTTPAddress   = ":8082"

	// DefaultConfigPath default path of config file
	DefaultConfigPath = "config.json"
)

var defaultDiscoveryURL = dmsg.DiscAddr(false)

// DefaultDiscoverURLTest default URL for discovery in test env
var DefaultDiscoverURLTest = dmsg.DiscAddr(true)

// PeerConfig represents a peer dmsg server to connect to for server-to-server mesh.
type PeerConfig struct {
	PubKey  cipher.PubKey `json:"public_key"`
	Address string        `json:"address"`
}

// Deployment is one (dmsg-discovery, transit-servers) pair this server
// should register with. AdvertisedAddress is the address THIS server
// should advertise to THIS discovery — for example a server may
// advertise a LAN address (192.168.x.x:8081) to a local discovery and
// a public address (1.2.3.4:8081) to an internet discovery. Servers is
// the dmsg-server transit set this server uses to reach this
// discovery; the union of all deployments' Servers is preloaded into
// the server's outbound dmsg.Client so it can dial any configured
// discovery via any reachable transit.
//
// The discovery's PK is encoded in DiscoveryDmsg (form
// `dmsg://<PK>:<port>`); there is no separate PK field. Callers
// extract it on demand for dmsgfirst registration.
type Deployment struct {
	Discovery           string        `json:"discovery,omitempty"`
	DiscoveryDmsg       string        `json:"discovery_dmsg,omitempty"`
	SessionsCount       int           `json:"sessions_count,omitempty"`
	Servers             []*disc.Entry `json:"servers,omitempty"`
	AdvertisedAddress   string        `json:"advertised_address,omitempty"`
	AdvertisedAddressV6 string        `json:"advertised_address_v6,omitempty"`
}

// DmsgConfig is the per-server `dmsg` block. JSON polymorphism
// mirrors pkg/dmsgc.DmsgConfig: a single Deployment object for
// single-deployment configs, an array of Deployments for
// multi-deployment configs.
type DmsgConfig struct {
	Deployments []Deployment `json:"-"`
}

// UnmarshalJSON accepts either a single Deployment object or an array.
func (c *DmsgConfig) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(data, &c.Deployments)
	}
	var single Deployment
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	c.Deployments = []Deployment{single}
	return nil
}

// MarshalJSON emits the single-object shape for a one-element list,
// otherwise the array shape.
func (c DmsgConfig) MarshalJSON() ([]byte, error) {
	if len(c.Deployments) == 1 {
		return json.Marshal(c.Deployments[0])
	}
	return json.Marshal(c.Deployments)
}

// Config is structure of config file
type Config struct {
	Path string `json:"-"`

	PubKey cipher.PubKey `json:"public_key"`
	SecKey cipher.SecKey `json:"secret_key"`

	// Dmsg holds the discovery / servers configuration. Polymorphic
	// JSON: a single object describes a single deployment, an array
	// describes multiple. Folded from the legacy Discovery /
	// DiscoveryDmsg / PublicAddress fields by NormalizedDeployments
	// when this field is unset.
	Dmsg DmsgConfig `json:"dmsg,omitempty"`

	// Discovery, DiscoveryDmsg, and PublicAddress are the legacy
	// single-discovery fields kept so existing config files continue
	// to work without restructuring. NormalizedDeployments folds them
	// into a one-element list when Dmsg.Deployments is empty. When
	// DiscoveryDmsg is set (URL form `dmsg://<PK>:<port>`), the PK is
	// extracted automatically and used to upgrade registration from
	// plain HTTP to dmsgfirst.
	Discovery     string `json:"discovery,omitempty"`
	DiscoveryDmsg string `json:"discovery_dmsg,omitempty"`
	PublicAddress string `json:"public_address,omitempty"`
	// PublicAddressV6 is the optional IPv6 advertised endpoint. When
	// non-empty, the server registers AddressV6 alongside Address in
	// its disc.Entry so dual-stack peers can dial it over IPv6 (the
	// dialer's Happy Eyeballs in Phase 3 picks v6 first when both are
	// present). The local TCP listener is unchanged — wildcard binds
	// already accept both families; this field is purely a discovery
	// advertisement.
	PublicAddressV6  string        `json:"public_address_v6,omitempty"`
	LocalAddress     string        `json:"local_address"`
	HTTPAddress      string        `json:"health_endpoint_address"`
	LogLevel         string        `json:"log_level"`
	UpdateInterval   time.Duration `json:"update_interval"`
	MaxSessions      int           `json:"max_sessions"`
	Peers            []PeerConfig  `json:"peers,omitempty"`
	EnableRouteSetup bool          `json:"enable_route_setup,omitempty"`
}

// GenerateDefaultConfig generate default config for dmsg-server
func GenerateDefaultConfig(c *Config) {
	pk, sk := cipher.GenerateKeyPair()

	c.Path = DefaultConfigPath
	c.PubKey = pk
	c.SecKey = sk
	c.Discovery = defaultDiscoveryURL
	c.PublicAddress = defaultPublicAddress
	c.LocalAddress = defaultLocalAddress
	c.HTTPAddress = defaultHTTPAddress
	c.LogLevel = "info"
	c.MaxSessions = dmsg.DefaultMaxSessions
}

// NormalizedDeployments returns the deployment list this server
// should register with. The modern Dmsg.Deployments field wins;
// otherwise a one-element list is synthesized from the legacy
// Discovery + DiscoveryDmsg fields.
//
// AdvertisedAddress resolution: a non-empty per-deployment value wins;
// otherwise the top-level PublicAddress is used as the default. This
// keeps single-listener servers simple (set public_address once) while
// still supporting the multi-network case where the same listener is
// reachable via different addresses depending on the discovery's
// clients (LAN address to LAN discovery, NAT'd public IP to internet
// discovery). Empty string after both layers passes through, and
// dmsg.SetAdvertisedAddr falls back to the local listener address.
//
// Returns nil when neither shape is populated. Returns a fresh slice
// so callers can mutate without affecting Dmsg.Deployments.
func (c *Config) NormalizedDeployments() []Deployment {
	var src []Deployment
	switch {
	case len(c.Dmsg.Deployments) > 0:
		src = c.Dmsg.Deployments
	case c.Discovery != "" || c.DiscoveryDmsg != "":
		src = []Deployment{{Discovery: c.Discovery, DiscoveryDmsg: c.DiscoveryDmsg}}
	default:
		return nil
	}
	out := make([]Deployment, len(src))
	for i, d := range src {
		if d.AdvertisedAddress == "" {
			d.AdvertisedAddress = c.PublicAddress
		}
		// Mirror the v4 fold for the optional v6 endpoint: a non-
		// empty per-deployment AdvertisedAddressV6 wins; otherwise
		// the top-level PublicAddressV6 is used. Empty after both
		// layers leaves the deployment v4-only — disc.Server's
		// AddressV6 stays omitempty-elided.
		if d.AdvertisedAddressV6 == "" {
			d.AdvertisedAddressV6 = c.PublicAddressV6
		}
		out[i] = d
	}
	return out
}

// PKFromDmsgURL extracts the dmsg PK from a URL of the form
// `dmsg://<PK>:<port>[/path]`. Exported so dmsg-server's start path
// can use it on Deployment.DiscoveryDmsg without re-implementing the
// parser. Returns the zero PK when the URL is empty, malformed, or
// doesn't contain a valid PK in the host part.
func PKFromDmsgURL(s string) cipher.PubKey {
	if s == "" {
		return cipher.PubKey{}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return cipher.PubKey{}
	}
	host := u.Hostname()
	if host == "" {
		host = strings.SplitN(u.Host, ":", 2)[0]
	}
	var pk cipher.PubKey
	if err := pk.Set(host); err != nil {
		return cipher.PubKey{}
	}
	return pk
}

// Flush trying to save config file
func (c Config) Flush(log *logging.Logger) (err error) {
	defer func() {
		if err != nil {
			log.WithError(err).Error("Failed to flush config to file.")
		}
	}()

	raw, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}

	return os.WriteFile(c.Path, raw, 0600)
}
