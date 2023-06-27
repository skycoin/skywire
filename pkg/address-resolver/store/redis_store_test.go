//go:build !no_ci
// +build !no_ci

package store

import (
	"context"
	"testing"

	"github.com/go-redis/redis"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
)

const url = "redis://localhost:6379"

func TestRedis(t *testing.T) {
	client, err := newRedisClient()
	require.NoError(t, err)
	require.NoError(t, client.FlushDB().Err())

	s, err := newStore()
	require.NoError(t, err)

	suite.Run(t, &AddressSuite{AddressStore: s})
}

func newRedisClient() (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opt)

	return client, nil
}

func newStore() (Store, error) {
	storeConfig := storeconfig.Config{
		Type:     storeconfig.Redis,
		URL:      url,
		Password: "",
	}
	log := logging.MustGetLogger("test")
	ctx := context.TODO()
	return New(ctx, storeConfig, log)
}
