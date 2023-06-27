package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-redis/redis"
	"github.com/skycoin/skywire/internal/sdmetrics"
	"github.com/skycoin/skywire/pkg/service-discovery/store"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const testRedisAddrEnvName = "TEST_SERVICEDISC_REDIS_ADDR"

func redisAddr(t *testing.T) string {
	redisAddr, ok := os.LookupEnv(testRedisAddrEnvName)
	if !ok {
		t.Skipf("Skipping '%s': Env '%s' is not set.", t.Name(), testRedisAddrEnvName)
	}
	if redisAddr == "" {
		redisAddr = "redis://127.0.0.1:6379"
	}
	return redisAddr
}

func redisClient(t *testing.T) *redis.Client {
	opts, err := redis.ParseURL(redisAddr(t))
	require.NoError(t, err)
	redisC := redis.NewClient(opts)
	clearRedis(t, redisC)
	return redisC
}

func postgresClient() (*gorm.DB, *logging.Logger) {
	logger := logging.NewMasterLogger().PackageLogger("integration_test")
	dsn := "host=localhost port=8383 user=postgres password=postgres dbname=postgres sslmode=disable" //nolint
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("test failed")
	}
	return gormDB, logger
}

func clearRedis(t *testing.T, redisC *redis.Client) {
	require.NoError(t, redisC.FlushAll().Err())
}

func serveAPI(t *testing.T) *httptest.Server {
	dbConf := storeconfig.Config{
		Type: storeconfig.Redis,
		URL:  redisAddr(t),
	}

	dbGorm, logger := postgresClient()

	ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
	defer cancel()

	nonceDB, err := httpauth.NewNonceStore(ctx, dbConf, "auth::")
	require.NoError(t, err)

	discDB, err := store.NewStore(dbGorm, logger)
	require.NoError(t, err)

	m := sdmetrics.NewEmpty()
	api := New(logging.MustGetLogger("server"), discDB, nonceDB, "", false, m, "")
	return httptest.NewServer(api)
}

func makeRandClient(srv *httptest.Server, sType string) (servicedisc.SWAddr, *servicedisc.HTTPClient) {
	cPK, cSK := cipher.GenerateKeyPair()
	cConf := servicedisc.Config{
		Type:     sType,
		PK:       cPK,
		SK:       cSK,
		Port:     20,
		DiscAddr: "http://" + srv.Listener.Addr().String(),
	}
	masterLogger := logging.NewMasterLogger()
	addr := servicedisc.NewSWAddr(cPK, cConf.Port)
	client := servicedisc.NewClient(logging.MustGetLogger("client:"+cPK.String()[:6]), masterLogger, cConf, &http.Client{}, "")
	return addr, client
}

// TestNew requires Env 'TEST_SERVICEDISC_REDIS_ADDR' to be defined.
// 'TEST_SERVICEDISC_REDIS_ADDR' should be the address to the redis-server.
func TestNew(t *testing.T) {
	// Prepare redis.
	redisC := redisClient(t)
	defer func() {
		clearRedis(t, redisC)
		require.NoError(t, redisC.Close())
	}()

	// Serve API.
	srv := serveAPI(t)
	defer srv.Close()

	// Ensure that calls to 'GET /api/services' returns all service entries (when expected).
	// and test Delete and Register entry again
	t.Run("GET /api/services", func(t *testing.T) {
		ctx := context.TODO()
		var err error
		_, c := makeRandClient(srv, servicedisc.ServiceTypeVPN)

		// Register new entry to redis
		err = c.Register(ctx)
		require.NoError(t, err)

		// Get all services (entries)
		service, err := c.Services(ctx, 1, "", "")
		fmt.Println(service)
		require.NoError(t, err)
		require.Equal(t, 1, len(service))

		// Delete entry from service discovery
		err = c.DeleteEntry(ctx)
		require.NoError(t, err)

		// Check deleted entry on service discovery
		service, err = c.Services(ctx, 1, "", "")
		fmt.Println(service)
		listenErr := errors.New("no service of type vpn registered")
		require.Equal(t, listenErr, err)
		require.Nil(t, service)

		// Register again
		err = c.Register(ctx)
		require.NoError(t, err)
	})
}
