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

// TestTypeConstants pins the iota ordering of the store Type values so an
// accidental reordering (which would change persisted/served config meaning) is
// caught.
func TestTypeConstants(t *testing.T) {
	assert.Equal(t, Type(0), Memory)
	assert.Equal(t, Type(1), Redis)
}
