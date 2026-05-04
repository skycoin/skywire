// Package stats — pkg/visor/stats/schema.go: persisted record types.
package stats

import (
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// SchemaVersion is the current bbolt layout version. Bumped on any
// breaking change to bucket layout or record shape.
const SchemaVersion = 1

// TransportRecord is the persisted per-transport rollup state. Stored
// in the `transports` bucket keyed by the transport ID (UUID, 16 bytes).
//
// Current is overwritten on every sample with the live snapshot; Daily
// is append-only with one entry per UTC day. The day-start cumulative
// baseline is held in memory by the Tracker, not persisted — on
// restart the next sample seeds a fresh baseline and the partial day's
// loss is bounded by StatsSampleInterval.
type TransportRecord struct {
	ID        uuid.UUID       `json:"id"`
	Edges     []cipher.PubKey `json:"edges"`
	Type      string          `json:"type"`
	Label     string          `json:"label,omitempty"`
	FirstSeen time.Time       `json:"first_seen"`
	LastSeen  time.Time       `json:"last_seen"`
	Current   *LiveSnapshot   `json:"current,omitempty"`
	Daily     []DailyRollup   `json:"daily,omitempty"`
}

// LiveSnapshot is the most recent observation for a transport.
//
// Type carries the wire-protocol type ("stcpr"/"sudph"/"stcp"/"dmsg")
// so TPD's CXO aggregator can record per-transport uptime heartbeats
// without a redis lookup. Empty on snapshots produced before the
// field was added — TPD treats that as "skip uptime, but bandwidth
// and latency still apply" so old visors keep working.
type LiveSnapshot struct {
	SentBytes    uint64    `json:"sent_bytes"`
	RecvBytes    uint64    `json:"recv_bytes"`
	LatencyMinMS float64   `json:"latency_min_ms,omitempty"`
	LatencyMaxMS float64   `json:"latency_max_ms,omitempty"`
	LatencyAvgMS float64   `json:"latency_avg_ms,omitempty"`
	SampledAt    time.Time `json:"sampled_at"`
	Type         string    `json:"type,omitempty"`
}

// DailyRollup is the sealed per-day view. Bandwidth values are deltas
// computed from the day-start baseline; latency stats are the
// accumulation of all samples taken within the day.
type DailyRollup struct {
	Date         string  `json:"date"` // YYYY-MM-DD UTC
	SentBytes    uint64  `json:"sent_bytes"`
	RecvBytes    uint64  `json:"recv_bytes"`
	LatencyMinMS float64 `json:"latency_min_ms,omitempty"`
	LatencyMaxMS float64 `json:"latency_max_ms,omitempty"`
	LatencyAvgMS float64 `json:"latency_avg_ms,omitempty"`
	Samples      uint32  `json:"samples"`
}
