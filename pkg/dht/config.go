package dht

import (
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// Config configures a DHT node.
type Config struct {
	// BootstrapPKs are public keys of known DHT nodes to connect to on startup.
	BootstrapPKs []cipher.PubKey `json:"bootstrap_pks,omitempty"`

	// MaxItems is the maximum number of items stored locally (0 = unlimited for full nodes).
	MaxItems int `json:"max_items,omitempty"`

	// ItemTTL is how long items live without re-announcement.
	ItemTTL time.Duration `json:"item_ttl,omitempty"`

	// FullNode stores all items regardless of XOR distance (few needed).
	FullNode bool `json:"full_node,omitempty"`

	// RefreshInterval is how often bucket refresh and item expiry sweeps run.
	RefreshInterval time.Duration `json:"refresh_interval,omitempty"`
}

// SetDefaults fills in zero values with sensible defaults.
func (c *Config) SetDefaults() {
	if c.ItemTTL <= 0 {
		c.ItemTTL = DefaultItemTTL
	}
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = 30 * time.Second
	}
	if c.MaxItems <= 0 && !c.FullNode {
		c.MaxItems = 10000
	}
}
