package store

import (
	"context"
	"io"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

//go:generate mockery --name Store --case underscore --inpackage

// Store represents a DB implementation.
type Store interface {
	io.Closer
	Service(ctx context.Context, sType string, addr servicedisc.SWAddr) (*servicedisc.Service, *servicedisc.HTTPError)
	Services(ctx context.Context, sType, version, country string) ([]servicedisc.Service, *servicedisc.HTTPError)
	UpdateService(ctx context.Context, se *servicedisc.Service) *servicedisc.HTTPError
	DeleteService(ctx context.Context, sType string, addr servicedisc.SWAddr) *servicedisc.HTTPError
	CountServiceTypes(ctx context.Context) (uint64, error)
	CountServices(ctx context.Context, serviceType string) (uint64, error)
}

// NewStore creates a new Redis store with TTL for service entries.
// The ttl parameter specifies how long service entries live before expiring.
// Services must send heartbeats before the TTL expires to stay registered.
func NewStore(ctx context.Context, client *redis.Client, logger *logging.Logger, ttl time.Duration) (Store, error) {
	return newRedisStore(ctx, client, logger, ttl)
}
