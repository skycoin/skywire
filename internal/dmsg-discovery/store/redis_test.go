//go:build !no_ci
// +build !no_ci

// Package store internal/dmsg-discovery/store/redis_test.go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/disc"
)

const (
	redisURL      = "redis://localhost:6379"
	redisPassword = ""
)

func TestRedisStoreClientEntry(t *testing.T) {
	ctx := context.TODO()
	log := logging.MustGetLogger("test")
	redis, err := newRedis(ctx, redisURL, redisPassword, 0, log)
	require.NoError(t, err)
	require.NoError(t, redis.(*redisStore).client.FlushDB(ctx).Err())

	pk, sk := cipher.GenerateKeyPair()

	entry := &disc.Entry{
		Static:    pk,
		Timestamp: time.Now().Unix(),
		Client: &disc.Client{
			DelegatedServers: []cipher.PubKey{pk},
		},
		Version:  "0",
		Sequence: 1,
	}
	require.NoError(t, entry.Sign(sk))

	require.NoError(t, redis.SetEntry(ctx, entry, time.Duration(0)))

	res, err := redis.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, entry, res)

	entries, err := redis.AvailableServers(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestRedisStoreServerEntry(t *testing.T) {
	ctx := context.TODO()
	log := logging.MustGetLogger("test")
	redis, err := newRedis(ctx, redisURL, redisPassword, 0, log)
	require.NoError(t, err)
	require.NoError(t, redis.(*redisStore).client.FlushDB(ctx).Err())

	pk, sk := cipher.GenerateKeyPair()

	entry := &disc.Entry{
		Static:    pk,
		Timestamp: time.Now().Unix(),
		Server: &disc.Server{
			Address:           "localhost:8080",
			AvailableSessions: 3,
		},
		Version:  "0",
		Sequence: 1,
	}

	require.NoError(t, entry.Sign(sk))

	require.NoError(t, redis.SetEntry(ctx, entry, time.Duration(0)))

	res, err := redis.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, entry, res)

	entries, err := redis.AvailableServers(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	require.NoError(t, redis.SetEntry(ctx, entry, time.Duration(0)))

	entries, err = redis.AvailableServers(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}
func TestRedisCountEntries(t *testing.T) {
	ctx := context.TODO()
	log := logging.MustGetLogger("test")
	redis, err := newRedis(ctx, redisURL, redisPassword, 0, log)
	require.NoError(t, err)
	require.NoError(t, redis.(*redisStore).client.FlushDB(ctx).Err())

	pk, sk := cipher.GenerateKeyPair()

	serverEntry := &disc.Entry{
		Static:    pk,
		Timestamp: time.Now().Unix(),
		Server: &disc.Server{
			Address:           "localhost:8080",
			AvailableSessions: 3,
		},
		Version:  "0",
		Sequence: 1,
	}

	require.NoError(t, serverEntry.Sign(sk))

	require.NoError(t, redis.SetEntry(ctx, serverEntry, time.Duration(0)))

	res, err := redis.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, serverEntry, res)

	pk, sk = cipher.GenerateKeyPair()
	clientEntry := &disc.Entry{
		Static:    pk,
		Timestamp: time.Now().Unix(),
		Client: &disc.Client{
			DelegatedServers: []cipher.PubKey{pk},
		},
		Version:  "0",
		Sequence: 1,
	}

	require.NoError(t, clientEntry.Sign(sk))

	require.NoError(t, redis.SetEntry(ctx, clientEntry, time.Duration(0)))

	res, err = redis.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, clientEntry, res)

	numberOfServers, numberOfClients, err := redis.CountEntries(ctx)
	require.NoError(t, err)
	assert.Equal(t, numberOfServers, int64(1))
	assert.Equal(t, numberOfClients, int64(1))
}
