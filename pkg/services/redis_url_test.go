package services

import (
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestRedisURLKeepsUnixSocket(t *testing.T) {
	cases := map[string]string{
		"":                                  "redis://localhost:6379",
		"redis:6379":                        "redis://redis:6379",
		"redis://redis:6379":                "redis://redis:6379",
		"rediss://r:6380":                   "rediss://r:6380",
		"unix:///run/redis/redis.sock":      "unix:///run/redis/redis.sock",
		"unix:///run/redis/redis.sock?db=0": "unix:///run/redis/redis.sock?db=0",
	}
	for in, want := range cases {
		require.Equal(t, want, (&Common{Redis: in}).RedisURL(), in)
	}
	opt, err := redis.ParseURL((&Common{Redis: "unix:///run/redis/redis.sock"}).RedisURL())
	require.NoError(t, err)
	require.Equal(t, "unix", opt.Network)
	require.Equal(t, "/run/redis/redis.sock", opt.Addr)
}
