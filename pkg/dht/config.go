package dht

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
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

	// WhitelistedPKs are publisher keys whose data is always fully
	// replicated and never evicted (Tier 1).
	WhitelistedPKs []cipher.PubKey `json:"whitelisted_pks,omitempty"`

	// TrustedPKs are publisher keys that get full replication unless
	// flagged for abuse (Tier 2). If empty, all non-whitelisted keys
	// are treated as public (Tier 3).
	TrustedPKs []cipher.PubKey `json:"trusted_pks,omitempty"`

	// PublicPoolSize is the max number of Tier 3 (public) items.
	// When exceeded, LRU eviction removes the oldest public items.
	// Defaults to 5000.
	PublicPoolSize int `json:"public_pool_size,omitempty"`

	// RateLimitPerPK is the max number of items a single public PK
	// can store. Prevents a single key from flooding the store.
	// 0 = unlimited. Defaults to 50.
	RateLimitPerPK int `json:"rate_limit_per_pk,omitempty"`

	// PersistPath is the file path for bbolt persistence (visors).
	// Empty = in-memory only (data lost on restart).
	PersistPath string `json:"persist_path,omitempty"`

	// RedisAddr enables Redis persistence (deployment services).
	// Format: "host:port". Empty = no Redis.
	RedisAddr string `json:"redis_addr,omitempty"`
	// RedisPassword for authenticated Redis connections.
	RedisPassword string `json:"redis_password,omitempty"`
	// RedisDB is the Redis database number for DHT data.
	RedisDB int `json:"redis_db,omitempty"`
}

// NewBackendFromConfig creates the appropriate Backend from config.
// Returns memBackend{} if no persistence is configured.
func NewBackendFromConfig(cfg *Config) (Backend, error) {
	if cfg.RedisAddr != "" {
		return NewRedisBackend(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, 0)
	}
	if cfg.PersistPath != "" {
		return NewBoltBackend(cfg.PersistPath)
	}
	return memBackend{}, nil
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
	if c.PublicPoolSize <= 0 {
		c.PublicPoolSize = 5000
	}
	if c.RateLimitPerPK <= 0 {
		c.RateLimitPerPK = 50
	}
}
