// Package uptimestats pkg/uptimestats/types.go c2-net-discovery
// holds the response shapes of the /uptimes endpoints that service,
// transport and dmsg discovery serve.
package uptimestats

import (
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Version selects which response shape the endpoint returns.
//
//	V1 (default): pk + online only
//	V2:           adds version + daily uptime percentages
//	V3:           adds per-minute timeline bitmaps (heavy — use sparingly)
type Version string

// Response-shape versions.
const (
	V1 Version = ""
	V2 Version = "v2"
	V3 Version = "v3"
)

// VisorSummary mirrors the shared JSON shape returned by every
// discovery service's /uptimes endpoint. Fields beyond PK + Online
// are populated only when the corresponding Version is requested.
type VisorSummary struct {
	PK       cipher.PubKey     `json:"pk"`
	Online   bool              `json:"on"`
	Version  string            `json:"version,omitempty"`
	Daily    map[string]string `json:"daily,omitempty"`    // YYYY-MM-DD → "0".."100"
	Timeline map[string]string `json:"timeline,omitempty"` // YYYY-MM-DD → bitmap
}

// TransportSummary is the per-transport uptime shape returned by
// transport-discovery's /uptimes/transports endpoint. Edge A/B are
// hex-encoded visor public keys, Type is the transport type
// (stcp/stcpr/sudph/dmsg).
type TransportSummary struct {
	ID       uuid.UUID         `json:"id"`
	Online   bool              `json:"on"`
	Type     string            `json:"type,omitempty"`
	EdgeA    string            `json:"edge_a,omitempty"`
	EdgeB    string            `json:"edge_b,omitempty"`
	Daily    map[string]string `json:"daily,omitempty"`
	Timeline map[string]string `json:"timeline,omitempty"`
}

// NetworkUptime is the shape returned by
// transport-discovery's /metrics/uptime (network-wide aggregate).
//
// Example (live endpoint):
//
//	{"total_transports":12815,"online":5443,
//	 "by_type":{"stcpr":{"total":5735,"online":1135},
//	            "sudph":{"total":7080,"online":4308}}}
type NetworkUptime struct {
	TotalTransports int                      `json:"total_transports"`
	Online          int                      `json:"online"`
	ByType          map[string]TypeBreakdown `json:"by_type,omitempty"`
	Daily           map[string]string        `json:"daily,omitempty"`
}

// TypeBreakdown is the nested per-transport-type total/online pair
// that /metrics/uptime returns under by_type.
type TypeBreakdown struct {
	Total  int `json:"total"`
	Online int `json:"online"`
}
