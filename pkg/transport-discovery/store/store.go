package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

var (
	// ErrNotEnoughACKs means that we're still waiting for a visor to confirm the transport registration
	ErrNotEnoughACKs = errors.New("not enough ACKs")

	// ErrAlreadyRegistered indicates that transport ID is already in use.
	ErrAlreadyRegistered = errors.New("ID already registered")

	// ErrTransportNotFound indicates that requested transport is not registered.
	ErrTransportNotFound = errors.New("transport not found")

	// ErrBadEntry is returned when entry is malformed.
	ErrBadEntry = errors.New("bad entry format")

	// ErrUnknownStoreType means that store type is unknown.
	ErrUnknownStoreType = errors.New("unknown store type")
)

// BandwidthAggregation stores aggregated bandwidth for a time period.
type BandwidthAggregation struct {
	TransportID string `json:"transport_id"`
	Period      string `json:"period"`     // "daily", "weekly", "monthly"
	PeriodKey   string `json:"period_key"` // e.g., "2026-02-10", "2026-W06", "2026-02"
	Bandwidth   uint64 `json:"bandwidth"`  // total bytes (sent + recv)
	UpdatedAt   int64  `json:"updated_at"`
}

// DailyBandwidthEntry stores bandwidth total for a single day.
type DailyBandwidthEntry struct {
	Date      string `json:"date"` // "2006-01-02"
	Bandwidth uint64 `json:"bw"`   // total bytes (sent + recv)
}

// VisorSummary holds a visor's aggregated bandwidth and online status.
type VisorSummary struct {
	PK              cipher.PubKey         `json:"pk"`
	Online          bool                  `json:"on"`
	TransportCount  int                   `json:"tp_count"`
	DailyBandwidths []DailyBandwidthEntry `json:"bws"`
}

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
	GetNumberOfTransports(context.Context) (map[types.Type]int, error)
	GetAllTransports(context.Context, bool) ([]*transport.Entry, error)
	// Bandwidth query methods
	GetTransportBandwidth(ctx context.Context, tpID uuid.UUID, period string, limit int) ([]BandwidthAggregation, error)
	GetVisorBandwidth(ctx context.Context, pk cipher.PubKey, period string, limit int) ([]BandwidthAggregation, error)
	GetAllVisorSummaries(ctx context.Context) ([]VisorSummary, error)
	BackupAndCleanOldBandwidth(ctx context.Context, backupPath string) error
	Close()
}

// New constructs a new Store of requested type.
func New(ctx context.Context, config storeconfig.Config, ttl time.Duration, logger *logging.Logger) (TransportStore, error) {
	switch config.Type {
	case storeconfig.Memory:
		return newMemoryStore(), nil
	case storeconfig.Redis:
		return newRedisStore(ctx, config.URL, config.Password, config.PoolSize, ttl, logger)
	default:
		return nil, ErrUnknownStoreType
	}
}
