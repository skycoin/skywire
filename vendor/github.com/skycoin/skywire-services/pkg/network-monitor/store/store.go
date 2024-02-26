package store

import (
	"context"
	"errors"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"

	"github.com/skycoin/skywire-services/internal/nm"
)

var (
	// ErrVisorSumNotFound indicates that requested visor summary is not registered.
	ErrVisorSumNotFound = errors.New("Visor summary not found")
)

// Store stores Transport metadata and generated nonce values.
type Store interface {
	TransportStore
}

// TransportStore stores Transport metadata.
type TransportStore interface {
	AddVisorSummary(context.Context, cipher.PubKey, *nm.VisorSummary) error
	GetVisorByPk(string) (*nm.VisorSummary, error)
	GetAllSummaries() (map[string]nm.Summary, error)
}

// New constructs a new Store of requested type.
func New(config storeconfig.Config) (Store, error) {
	switch config.Type {
	case storeconfig.Memory:
		return newMemoryStore(), nil
	case storeconfig.Redis:
		return newRedisStore(config.URL, config.Password, config.PoolSize)
	default:
		return nil, errors.New("unknown store type")
	}
}
