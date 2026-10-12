package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// A store whose entries never expire starts without its cleanup loop, which
// would panic on a zero interval.
func TestRedisStoreWithoutTTL(t *testing.T) {
	url := os.Getenv("SKYWIRE_TEST_REDIS")
	if url == "" {
		t.Skip("SKYWIRE_TEST_REDIS unset; no redis to test against")
	}
	opt, err := redis.ParseURL(url)
	require.NoError(t, err)
	client := redis.NewClient(opt)
	defer client.Close() //nolint:errcheck
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = NewStore(ctx, client, logging.MustGetLogger("test"), 0)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
}
