// Package storeconfig pkg/cxo/storeconfig/storeconfig.go c2-net-cxo
package storeconfig

import (
	"os"
)

// Type is a config type.
type Type int

// Type may be either in-memory or Redis.
const (
	Memory Type = iota
	Redis
)

// Config defines a store configuration.
type Config struct {
	Type     Type
	URL      string `json:"url"`
	Password string `json:"password"`
	PoolSize int    `json:"pool_size"`
}

const redisPasswordEnvName = "REDIS_PASSWORD"

// RedisPassword returns Redis password which is read from an environment variable.
func RedisPassword() string {
	return os.Getenv(redisPasswordEnvName)
}
