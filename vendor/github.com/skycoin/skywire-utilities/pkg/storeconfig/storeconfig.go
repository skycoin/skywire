// Package storeconfig pkg/storeconfig/storeconfig.go
package storeconfig

import "os"

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

const (
	pgUser     = "PG_USER"
	pgPassword = "PG_PASSWORD"
	pgDatabase = "PG_DATABASE"
)

// RedisPassword returns Redis password which is read from an environment variable.
func RedisPassword() string {
	return os.Getenv(redisPasswordEnvName)
}

// PostgresCredential return prostgres credential needed on services
func PostgresCredential() (string, string, string) {
	return os.Getenv(pgUser), os.Getenv(pgPassword), os.Getenv(pgDatabase)
}
