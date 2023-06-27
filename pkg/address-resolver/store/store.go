package store

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

// Store is an alias for AddressStore.
type Store interface {
	AddressStore
}

// AddressStore stores PK to address mapping.
type AddressStore interface {
	Bind(ctx context.Context, netType network.Type, pk cipher.PubKey, visorData addrresolver.VisorData) error
	DelBind(ctx context.Context, netType network.Type, pk cipher.PubKey) error
	Resolve(_ context.Context, netType network.Type, pk cipher.PubKey) (addrresolver.VisorData, error)
	GetAll(ctx context.Context, netType network.Type) ([]string, error)
}

var (
	// ErrNoEntry means that there exists no entry for this PK.
	ErrNoEntry = errors.New("no entry for this PK")
	// ErrUnknownStoreType means that store type is unknown.
	ErrUnknownStoreType = errors.New("unknown store type")
	// ErrUnknownTransportType means that transport type is unknown.
	ErrUnknownTransportType = errors.New("unknown transport type")
)

// New constructs a new Store of requested type.
func New(ctx context.Context, config storeconfig.Config, logger *logging.Logger) (Store, error) {
	switch config.Type {
	case storeconfig.Memory:
		return newMemoryStore(), nil
	case storeconfig.Redis:
		return newRedisStore(ctx, config.URL, config.Password, config.PoolSize, logger)
	default:
		return nil, ErrUnknownStoreType
	}
}
