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

// MetricsQuery contains query parameters for metrics endpoints.
type MetricsQuery struct {
	Days      int    // 0 = all available (max 35)
	Type      string // Filter by transport type (stcpr, sudph, dmsg, stcp)
	Live      string // Filter by liveness: "true", "false", "all" (default: "all")
	Edges     bool   // Include actual public keys in response
	Bandwidth bool   // Include bandwidth data (default: true)
	Latency   bool   // Include latency data (default: true)
}

// EdgeBandwidth contains bandwidth data from one edge's perspective.
type EdgeBandwidth struct {
	Sent uint64 `json:"sent"` // Cumulative bytes sent
	Recv uint64 `json:"recv"` // Cumulative bytes received
}

// EdgeLatency contains latency data from one edge's perspective.
type EdgeLatency struct {
	AvgMs float64 `json:"avg_ms"` // Average latency in milliseconds
	MinMs float64 `json:"min_ms"` // Minimum observed latency
	MaxMs float64 `json:"max_ms"` // Maximum observed latency
}

// EdgeMetricFull contains combined bandwidth and latency for one edge.
type EdgeMetricFull struct {
	Bandwidth *EdgeBandwidth `json:"bandwidth,omitempty"`
	Latency   *EdgeLatency   `json:"latency,omitempty"`
}

// DailyEdgeMetric contains per-day metrics with edge A and B data.
type DailyEdgeMetric struct {
	Date string          `json:"date"`
	A    *EdgeMetricFull `json:"a,omitempty"`
	B    *EdgeMetricFull `json:"b,omitempty"`
}

// TransportMetric contains per-transport metrics with daily breakdowns.
type TransportMetric struct {
	ID    string            `json:"id"`
	Type  string            `json:"type"`
	Live  bool              `json:"live"`
	Edges []string          `json:"edges,omitempty"` // Only included if query.Edges=true
	Daily []DailyEdgeMetric `json:"daily"`
}

// TypeMetricAggregate contains aggregate metrics for a transport type.
type TypeMetricAggregate struct {
	Bandwidth uint64  `json:"bandwidth,omitempty"`
	Latency   float64 `json:"latency,omitempty"`
}

// DailyAggregate contains network-wide aggregate for a single day.
type DailyAggregate struct {
	Date      string                          `json:"date"`
	Bandwidth uint64                          `json:"bandwidth,omitempty"`
	Latency   float64                         `json:"latency,omitempty"`
	ByType    map[string]*TypeMetricAggregate `json:"by_type,omitempty"`
}

// CumulativeAggregate contains cumulative totals across all days.
type CumulativeAggregate struct {
	Bandwidth uint64                          `json:"bandwidth,omitempty"`
	Latency   float64                         `json:"latency,omitempty"`
	ByType    map[string]*TypeMetricAggregate `json:"by_type,omitempty"`
}

// NetworkMetricResponse is the response for GET /metric.
type NetworkMetricResponse struct {
	Daily      []DailyAggregate     `json:"daily"`
	Cumulative *CumulativeAggregate `json:"cumulative"`
}

// VisorBandwidthAggregate contains bandwidth totals for a visor.
type VisorBandwidthAggregate struct {
	Sent  uint64 `json:"sent"`
	Recv  uint64 `json:"recv"`
	Total uint64 `json:"total"`
}

// DailyVisorAggregate contains per-day visor aggregate data.
type DailyVisorAggregate struct {
	Date      string                   `json:"date"`
	Bandwidth *VisorBandwidthAggregate `json:"bandwidth,omitempty"`
	Latency   float64                  `json:"latency,omitempty"`
}

// VisorCumulativeAggregate contains cumulative visor totals.
type VisorCumulativeAggregate struct {
	Bandwidth *VisorBandwidthAggregate `json:"bandwidth,omitempty"`
	Latency   float64                  `json:"latency,omitempty"`
}

// VisorMetricResponse is the response for a single visor in GET /metric/visor/{pks}.
type VisorMetricResponse struct {
	Daily      []DailyVisorAggregate     `json:"daily"`
	Cumulative *VisorCumulativeAggregate `json:"cumulative"`
}

// VisorSummary holds a visor's uptime data.
// This format matches the Uptime Tracker's response format.
type VisorSummary struct {
	PK      cipher.PubKey     `json:"pk"`
	Online  bool              `json:"on"`
	Version string            `json:"version,omitempty"`
	Daily   map[string]string `json:"daily,omitempty"`
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
	// Bandwidth query methods (legacy)
	GetTransportBandwidth(ctx context.Context, tpID uuid.UUID, period string, limit int) ([]BandwidthAggregation, error)
	GetVisorBandwidth(ctx context.Context, pk cipher.PubKey, period string, limit int) ([]BandwidthAggregation, error)
	GetAllVisorSummaries(ctx context.Context) ([]VisorSummary, error)
	BackupAndCleanOldBandwidth(ctx context.Context, backupPath string) error
	// New metrics methods
	GetNetworkMetrics(ctx context.Context, query MetricsQuery) (*NetworkMetricResponse, error)
	GetVisorAggregateMetrics(ctx context.Context, pks []cipher.PubKey, query MetricsQuery) (map[string]*VisorMetricResponse, error)
	GetAllTransportMetrics(ctx context.Context, query MetricsQuery) ([]TransportMetric, error)
	GetTransportMetricsByIDs(ctx context.Context, ids []uuid.UUID, query MetricsQuery) ([]TransportMetric, error)
	GetTransportMetricsByVisors(ctx context.Context, pks []cipher.PubKey, query MetricsQuery) ([]TransportMetric, error)
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
