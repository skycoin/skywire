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
	TotalSent   uint64 `json:"total_sent"`
	TotalRecv   uint64 `json:"total_recv"`
	UpdatedAt   int64  `json:"updated_at"`
}

// BandwidthSummary stores bandwidth totals for a single period.
type BandwidthSummary struct {
	TotalSent uint64 `json:"total_sent"`
	TotalRecv uint64 `json:"total_recv"`
	PeriodKey string `json:"period_key"`
}

// VisorSummary holds a visor's aggregated bandwidth and online status.
type VisorSummary struct {
	PK               cipher.PubKey    `json:"pk"`
	Online           bool             `json:"online"`
	TransportCount   int              `json:"transport_count"`
	DailyBandwidth   *BandwidthSummary `json:"daily_bandwidth,omitempty"`
	WeeklyBandwidth  *BandwidthSummary `json:"weekly_bandwidth,omitempty"`
	MonthlyBandwidth *BandwidthSummary `json:"monthly_bandwidth,omitempty"`
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
