// Package storeconfig pkg/cxo/storeconfig/storeconfig_test.go
package storeconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRedisPassword verifies the Redis password is read from its env var,
// covering both the set and unset cases.
func TestRedisPassword(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv(redisPasswordEnvName, "s3cret")
		assert.Equal(t, "s3cret", RedisPassword())
	})

	t.Run("unset", func(t *testing.T) {
		t.Setenv(redisPasswordEnvName, "")
		assert.Equal(t, "", RedisPassword())
	})
}

// TestPostgresCredential verifies the three Postgres credentials are read from
// their respective env vars and returned in order.
func TestPostgresCredential(t *testing.T) {
	t.Run("all set", func(t *testing.T) {
		t.Setenv(pgUser, "user")
		t.Setenv(pgPassword, "pass")
		t.Setenv(pgDatabase, "db")

		user, pass, db := PostgresCredential()
		assert.Equal(t, "user", user)
		assert.Equal(t, "pass", pass)
		assert.Equal(t, "db", db)
	})

	t.Run("all unset", func(t *testing.T) {
		t.Setenv(pgUser, "")
		t.Setenv(pgPassword, "")
		t.Setenv(pgDatabase, "")

		user, pass, db := PostgresCredential()
		assert.Equal(t, "", user)
		assert.Equal(t, "", pass)
		assert.Equal(t, "", db)
	})
}

// TestTypeConstants pins the iota ordering of the store Type values so an
// accidental reordering (which would change persisted/served config meaning) is
// caught.
func TestTypeConstants(t *testing.T) {
	assert.Equal(t, Type(0), Memory)
	assert.Equal(t, Type(1), Redis)
}
