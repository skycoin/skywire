package store

import (
	"context"
	"io"
	"time"

	"gorm.io/gorm"

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

// NewStore creates a new postgres store implementation.
func NewStore(db *gorm.DB, logger *logging.Logger) (Store, error) {
	return newPostgresStore(db, logger)
}

// NewMemoryBackedStore creates a new memory-backed store
func NewMemoryBackedStore(db *gorm.DB, logger *logging.Logger, reloadPeriod time.Duration) (Store, error) {
	return NewMemoryStore(db, logger, reloadPeriod)
}
