// Package treeprobe consumes the NDJSON event stream emitted by
// `cli visor ping tree-stream`, aggregates ping/latency/jitter
// metrics per (level, parent_pk, remote_pk) tuple, joins against
// TPD's per-transport bandwidth + latency snapshot, and emits a
// CSV row per cell of the hop × routes measurement matrix.
//
// The package is a thin consumer — it does not invoke `cli ping
// tree-stream` itself; the driver (ci_scripts/tree-probe.sh) is
// responsible for spawning the CLI with the right --hops flags
// and piping the NDJSON into the treeprobe binary on stdin.
//
// Wire envelope (locked in PR #2732):
//
//	{"ts": "2026-05-20T01:30:00.123Z",
//	 "type": "ping_result",
//	 "data": { ...proto-snake_case fields, int64 as string... }}
//
// The outer envelope uses RFC3339Nano timestamps and one of 6
// discriminator strings. The inner data is protojson-marshaled
// from the corresponding PingTree* proto message with
// UseProtoNames=true (snake_case fields) and the protobuf int64
// → JSON string convention. Numeric duration/byte fields in this
// package's mirror types are typed as Int64String to surface that
// quirk explicitly to consumers.
package treeprobe

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// EventType is the discriminator string in the envelope's "type"
// field. Mirrors the classifyPayload switch in
// cmd/skywire-cli/commands/visor/ping/tree_stream.go.
type EventType string

const (
	TypeDiscovered   EventType = "discovered"
	TypePingResult   EventType = "ping_result"
	TypeLevelDone    EventType = "level_done"
	TypeRunDone      EventType = "run_done"
	TypeStatusUpdate EventType = "status_update"
	TypeServerError  EventType = "server_error"
)

// Envelope is the outer NDJSON line shape. Data is held as
// json.RawMessage so the consumer can defer decoding to the
// type-specific struct once it has Type in hand.
type Envelope struct {
	TS   string          `json:"ts"`
	Type EventType       `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Int64String is the JSON shape protobuf uses for int64 fields:
// always a JSON string. Wrapping it in a named type lets the
// parser surface the convention to consumers + lets us add
// convenience methods for nanosecond → time.Duration conversion
// without polluting every struct field.
type Int64String int64

// UnmarshalJSON decodes either a JSON string (protobuf default) OR a
// JSON number (tolerant for hand-written test fixtures). Returns an
// error on any non-integer value.
func (i *Int64String) UnmarshalJSON(b []byte) error {
	// Strip surrounding quotes if present.
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		b = b[1 : len(b)-1]
	}
	v, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return fmt.Errorf("treeprobe: parse Int64String %q: %w", string(b), err)
	}
	*i = Int64String(v)
	return nil
}

// MarshalJSON emits the int64-as-string form on the way out — useful
// for round-tripping in tests + keeps the on-the-wire shape stable.
func (i Int64String) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatInt(int64(i), 10))), nil
}

// Discovered fires when the BFS first observes a new (transport,
// remote_pk) pair, before any ping is attempted.
type Discovered struct {
	TpID     string `json:"tp_id"`
	TpType   string `json:"tp_type"`
	RemotePK string `json:"remote_pk"`
	ParentPK string `json:"parent_pk"`
	Level    int32  `json:"level"`
}

// PingResult is the canonical measurement event. One per peer in
// the tree, regardless of whether the latency was sampled live or
// pulled from the cached TransportSummary. LatencySource
// discriminates the provenance.
//
// Wire-NOT-deterministic ordering: results arrive interleaved by
// completion, not in BFS traversal order. The harness sorts/groups
// by (Level, ParentPK, RemotePK) on receive.
type PingResult struct {
	TpID     string `json:"tp_id"`
	TpType   string `json:"tp_type"`
	RemotePK string `json:"remote_pk"`
	ParentPK string `json:"parent_pk"`
	Level    int32  `json:"level"`
	// Proto field name is "canceled" (en-US per misspell linter,
	// renamed from initial "cancelled" during 2026-05-20 design
	// review).
	Canceled bool `json:"canceled"`

	SampleCount  int32       `json:"sample_count"`
	SetupLatency Int64String `json:"setup_latency_ns"`
	PingAvgNs    Int64String `json:"ping_avg_ns"`
	PingP50Ns    Int64String `json:"ping_p50_ns"`
	PingP99Ns    Int64String `json:"ping_p99_ns"`
	// JitterNs is population stddev of ping samples:
	// sqrt(sum((x-mean)^2) / N). Spec'd 2026-05-20 00:54Z.
	JitterNs Int64String `json:"jitter_ns"`

	Failed   bool   `json:"failed"`
	SetupErr string `json:"setup_err,omitempty"`
	PingErr  string `json:"ping_err,omitempty"`
	CalcErr  string `json:"calc_err,omitempty"`

	// LatencySource is one of:
	//   "live_ping"          — fresh measurement
	//   "transport_summary"  — smoothed RTT from TransportSummary.LatencyMS
	//                          (PR #2733, level-1 only)
	//   "skipped"            — cached fast-path explicitly disabled
	LatencySource string     `json:"latency_source"`
	Route         []RouteHop `json:"route,omitempty"`
}

// RouteHop is one entry in the route taken from probe origin to
// the target peer. PingResult.Route is empty for level-1 entries.
type RouteHop struct {
	TpID     string `json:"tp_id"`
	TpType   string `json:"tp_type"`
	RemotePK string `json:"remote_pk"`
}

// LevelDone fires when the BFS finishes all peers at a given
// level. SkippedCached is non-zero when transport_summary fast-path
// short-circuited some peers (Beta's PR #2733). Lets the harness
// compute use_transport_latency hit-rate per level.
type LevelDone struct {
	Level         int32 `json:"level"`
	Attempted     int32 `json:"attempted"`
	Succeeded     int32 `json:"succeeded"`
	Failed        int32 `json:"failed"`
	SkippedCached int32 `json:"skipped_cached"`
}

// RunDone fires once at the end of a successful (or canceled) run.
// Field names match PingTreeRunDone in pkg/visor/rpcgrpc/ping.pb.go;
// most fields are int32 counters, WallTimeNs is the protobuf int64
// → JSON string convention.
//
// TerminationReason is one of:
//
//	"max_level"      — hit cfg.MaxLevel
//	"no_neighbors"   — BFS exhausted before MaxLevel
//	"hops_target"    — reached cfg.Hops target level
//	"context_cancel" — client disconnect / server shutdown
//	"error"          — see ServerError preceding this event
type RunDone struct {
	TotalDiscovered    int32       `json:"total_discovered,omitempty"`
	TotalPinged        int32       `json:"total_pinged,omitempty"`
	TotalSucceeded     int32       `json:"total_succeeded,omitempty"`
	TotalFailed        int32       `json:"total_failed,omitempty"`
	TotalSkippedCached int32       `json:"total_skipped_cached,omitempty"`
	WallTimeNs         Int64String `json:"wall_time_ns"`
	PeakInFlight       int32       `json:"peak_in_flight,omitempty"`
	TerminationReason  string      `json:"termination_reason,omitempty"`
}

// StatusUpdate fires periodically (cadence server-chosen) to
// surface live in-flight + queue state. Dropped first under back-
// pressure on the bounded event channel.
type StatusUpdate struct {
	Phase    string `json:"phase"`
	InFlight int32  `json:"in_flight"`
	Pending  int32  `json:"pending"`
}

// ServerError is emitted when the BFS handler hits a fatal error
// it can't recover from. Always the last event before the stream
// terminates.
type ServerError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
