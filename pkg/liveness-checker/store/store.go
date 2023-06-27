package store

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/internal/lc"
)

var (
	// ErrServiceNotFound indicates that requested service is not registered.
	ErrServiceNotFound = errors.New("Service not found")
)

// Store stores Service metadata.
type Store interface {
	ServiceStore
}

// ServiceStore stores Service metadata.
type ServiceStore interface {
	AddServiceSummary(context.Context, string, *lc.ServiceSummary) error
	GetServiceByName(context.Context, string) (*lc.ServiceSummary, error)
	GetServiceSummaries(context.Context) (map[string]*lc.ServiceSummary, error)
}

// New constructs a new Store of requested type.
func New(ctx context.Context, config storeconfig.Config, logger *logging.Logger) (Store, error) {
	switch config.Type {
	case storeconfig.Memory:
		return newMemoryStore(), nil
	case storeconfig.Redis:
		return newRedisStore(ctx, config.URL, config.Password, config.PoolSize, logger)
	default:
		return nil, errors.New("unknown store type")
	}
}
