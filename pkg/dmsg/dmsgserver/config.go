// Package dmsgserver pkg/dmsgserver/config.go
package dmsgserver

import (
	"encoding/json"
	"os"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/cipher"
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

// DiscoveryConfig represents a single dmsg-discovery this server should
// register with. AdvertisedAddress is the address this server should
// advertise to THIS discovery — for example a server may advertise a
// LAN address (192.168.x.x:8081) to a local discovery and a public
// address (1.2.3.4:8081) to an internet discovery. PK, when set, lets
// the server detect inbound DMSG sessions originated by this discovery
// and trigger an immediate registration push back over that session
// (see Config.NormalizedDiscoveries for backward-compat fallback).
type DiscoveryConfig struct {
	URL               string        `json:"url"`
	DmsgURL           string        `json:"dmsg_url,omitempty"`
	PK                cipher.PubKey `json:"public_key,omitempty"`
	AdvertisedAddress string        `json:"advertised_address,omitempty"`
}

// Config is structure of config file
type Config struct {
	Path string `json:"-"`

	PubKey cipher.PubKey `json:"public_key"`
	SecKey cipher.SecKey `json:"secret_key"`
	// Discoveries is the list of dmsg-discoveries this server registers
	// with. When empty, the legacy Discovery + PublicAddress fields are
	// folded into a single-element list (see NormalizedDiscoveries).
	Discoveries []DiscoveryConfig `json:"discoveries,omitempty"`
	// Discovery and PublicAddress are the legacy single-discovery
	// fields. Superseded by Discoveries; kept so existing config files
	// continue to work without edits.
	Discovery        string        `json:"discovery,omitempty"`
	PublicAddress    string        `json:"public_address,omitempty"`
	LocalAddress     string        `json:"local_address"`
	HTTPAddress      string        `json:"health_endpoint_address"`
	LogLevel         string        `json:"log_level"`
	UpdateInterval   time.Duration `json:"update_interval"`
	MaxSessions      int           `json:"max_sessions"`
	Peers            []PeerConfig  `json:"peers,omitempty"`
	EnableRouteSetup bool          `json:"enable_route_setup,omitempty"`
	// EnableDHT runs a Kademlia DHT full node on this DMSG server.
	// Every visor uses DMSG server PKs as DHT bootstrap peers, so
	// enabling this makes the DHT network functional without any
	// additional infrastructure.
	EnableDHT bool   `json:"enable_dht,omitempty"`
	RedisAddr string `json:"redis_addr,omitempty"` // Redis for DHT persistence (optional)
	// PersistPath is a bbolt file path for DHT persistence. Used only
	// when RedisAddr is empty; ignored otherwise. Empty means in-memory
	// only — DHT state is lost on every restart and the cluster's
	// disc-mirror data (which lives in Redis) is unreachable. For
	// Docker deployments point this at a path inside the existing
	// config volume mount (e.g. "/etc/skywire/dmsg-server/dht.db").
	// For Kubernetes use a writable PersistentVolume mount; the config
	// Secret/ConfigMap is typically read-only and bbolt cannot use it.
	PersistPath string `json:"persist_path,omitempty"`
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

// NormalizedDiscoveries returns the discovery list the server should
// actually register with. When the modern Discoveries field is set,
// it's returned as-is; otherwise a single-element list is synthesized
// from the legacy Discovery + PublicAddress fields so existing config
// files keep working unchanged. Returns nil when neither is set.
func (c *Config) NormalizedDiscoveries() []DiscoveryConfig {
	if len(c.Discoveries) > 0 {
		return c.Discoveries
	}
	if c.Discovery == "" {
		return nil
	}
	return []DiscoveryConfig{{URL: c.Discovery, AdvertisedAddress: c.PublicAddress}}
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
