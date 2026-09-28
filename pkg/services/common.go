// Package services pkg/services/common.go c2-vis-appsvc
package services

import (
	"strings"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
)

// Common is the configuration every discovery-type service shares
// (transport-discovery, address-resolver, route-finder,
// service-discovery, dmsg-discovery). Each service's Config embeds it, so
// the same keys mean the same thing in every block.
type Common struct {
	// PubKey and SecKey are the service's dmsg identity. A service
	// embedded in a visor uses the visor's instead.
	PubKey cipher.PubKey `json:"public_key,omitempty"`
	SecKey cipher.SecKey `json:"secret_key,omitempty"`

	// Addr is the plain-HTTP listen address.
	Addr string `json:"addr,omitempty"`
	// Mode selects the listeners: "http", "dmsg" or "dual"
	// (see pkg/services/svcmode).
	Mode string `json:"mode,omitempty"`
	// DmsgPort is the dmsghttp listener port (default 80).
	DmsgPort uint16 `json:"dmsg_port,omitempty"`

	// Redis is the redis URL of the service's store. With Testing and no
	// URL, the store is in memory.
	Redis string `json:"redis,omitempty"`
	// RedisPoolSize is the redis connection pool size (default 10).
	RedisPoolSize int `json:"redis_pool_size,omitempty"`
	// EntryTimeout is how long an entry lives without a refresh.
	EntryTimeout Duration `json:"entry_timeout,omitempty"`

	// LogLevel is the minimum log level; Tag names the service's logger.
	LogLevel string `json:"log_level,omitempty"`
	Tag      string `json:"tag,omitempty"`
	// MetricsAddr exposes Prometheus metrics; PprofAddr serves pprof.
	// Empty disables either.
	MetricsAddr string `json:"metrics_addr,omitempty"`
	PprofAddr   string `json:"pprof_addr,omitempty"`

	// Testing runs the service for a test network: the store is in memory
	// unless Redis is set, and checks that only make sense on the public
	// network are relaxed. Requests are authenticated as always.
	Testing bool `json:"testing,omitempty"`
	// SurveyWhitelist are the keys allowed to read the service's debug and
	// survey endpoints. Empty means the deployment's list (see SurveyKeys).
	SurveyWhitelist []cipher.PubKey `json:"survey_whitelist,omitempty"`

	// TestEnvironment selects the test deployment's defaults (dmsg
	// servers and discovery) over production's.
	TestEnvironment bool `json:"test_environment,omitempty"`
}

// DefaultRedisPoolSize is the pool size when RedisPoolSize is unset.
const DefaultRedisPoolSize = 10

// StoreType is the store the service uses: memory when Testing without a
// redis URL, redis otherwise.
func (c *Common) StoreType() storeconfig.Type {
	if c.Testing && c.Redis == "" {
		return storeconfig.Memory
	}
	return storeconfig.Redis
}

// PoolSize is RedisPoolSize or its default.
func (c *Common) PoolSize() int {
	if c.RedisPoolSize > 0 {
		return c.RedisPoolSize
	}
	return DefaultRedisPoolSize
}

// LogTag is Tag or def.
func (c *Common) LogTag(def string) string {
	if c.Tag != "" {
		return c.Tag
	}
	return def
}

// NonceStoreType is where auth nonces live. Requests over dmsg are
// authenticated by the stream's key and never consult nonces, so only a
// plain-HTTP surface needs them durable: redis when one is served and the
// store is redis, memory otherwise. Every service keeps a nonce store: its
// clients fetch a nonce before their first request.
func NonceStoreType(store storeconfig.Type, plainHTTP bool) storeconfig.Type {
	if plainHTTP && store == storeconfig.Redis {
		return storeconfig.Redis
	}
	return storeconfig.Memory
}

// SurveyKeys is SurveyWhitelist, or the whitelist of the deployment
// TestEnvironment selects.
func (c *Common) SurveyKeys() []cipher.PubKey {
	if len(c.SurveyWhitelist) > 0 {
		return c.SurveyWhitelist
	}
	if c.TestEnvironment {
		return deployment.Test.SurveyWhitelist
	}
	return deployment.Prod.SurveyWhitelist
}

// RedisURL is Redis with its scheme, or the local default.
func (c *Common) RedisURL() string {
	u := c.Redis
	if u == "" {
		return "redis://localhost:6379"
	}
	if !strings.HasPrefix(u, "redis://") && !strings.HasPrefix(u, "rediss://") {
		u = "redis://" + u
	}
	return u
}

// StoreConfig is the service's store: see StoreType, RedisURL and PoolSize.
func (c *Common) StoreConfig() storeconfig.Config {
	return storeconfig.Config{
		Type:     c.StoreType(),
		URL:      c.RedisURL(),
		Password: storeconfig.RedisPassword(),
		PoolSize: c.PoolSize(),
	}
}

// NonceStoreConfig is the auth nonce store: see NonceStoreType.
func (c *Common) NonceStoreConfig(plainHTTP bool) storeconfig.Config {
	sc := c.StoreConfig()
	sc.Type = NonceStoreType(sc.Type, plainHTTP)
	return sc
}
