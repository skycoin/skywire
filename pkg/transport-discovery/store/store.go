package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

var (
	// ErrNotEnoughACKs means that we're still waiting for a visor to confirm the transport registration
	ErrNotEnoughACKs = errors.New("not enough ACKs")

	// ErrAlreadyRegistered indicates that transport ID is already in use.
	ErrAlreadyRegistered = errors.New("ID already registered")

	// ErrTransportNotFound indicates that requested transport is not registered.
	ErrTransportNotFound = errors.New("transport not found")
)

// Store stores Transport metadata and generated nonce values.
type Store interface {
	TransportStore
}

// TransportStore stores Transport metadata.
type TransportStore interface {
	RegisterTransport(context.Context, *transport.SignedEntry) error
	DeregisterTransport(context.Context, uuid.UUID) error
	GetTransportByID(context.Context, uuid.UUID) (*transport.Entry, error)
	GetTransportsByEdge(context.Context, cipher.PubKey) ([]*transport.Entry, error)
	GetNumberOfTransports(context.Context) (map[network.Type]int, error)
	GetAllTransports(context.Context) ([]*transport.Entry, error)
	Close()
}

// New constructs a new Store of requested type.
func New(logger *logging.Logger, gormDB *gorm.DB, memoryStore bool) (TransportStore, error) {
	if memoryStore {
		return newMemoryStore(), nil
	}
	return NewPostgresStore(logger, gormDB)
}
