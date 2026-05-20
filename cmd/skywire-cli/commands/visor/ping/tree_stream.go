// Package ping — cmd/skywire-cli/commands/visor/ping/tree_stream.go:
// thin gRPC client for the StreamPingTree RPC. The visor does the
// BFS server-side and pushes PingTreeEvents over the stream; this
// command renders each event as one NDJSON line on stdout.
//
// Why separate from tree.go: the Bubble Tea TUI in tree.go has its
// own model + render loop that the next iteration will rewire onto
// this same stream. Until that lands, the new server-side gRPC path
// gets its own subcommand so the existing TUI keeps working
// unchanged for interactive users while harness consumers + coding
// agents drive the new RPC.
//
// Output: one JSON object per stdout line. Top-level fields:
//
//	{"ts":"<RFC3339Nano>", "type":"discovered|ping_result|level_done|run_done|status_update|server_error", "data":{...}}
//
// `data` is the marshaled oneof payload with proto's snake_case
// field names preserved. NDJSON consumers can `jq -c .` to filter,
// or stream-decode into the wire types via protojson.
package ping

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

var (
	streamMaxLevel    int
	streamHops        int
	streamTries       int
	streamSize        int
	streamConcurrency int
	streamOnlineOnly  bool
	streamMinVersion  string
	streamUseTpLat    bool
	streamDmsgOnly    bool
	streamDmsgPreChk  bool
	streamRetries     int
	streamDryRun      bool
	streamTimeout     time.Duration
	streamSetupTO     time.Duration
)

func init() {
	pingTreeStreamCmd.Flags().IntVarP(&streamMaxLevel, "max-level", "l", 0,
		"maximum BFS depth (0 = unlimited until expansion exhausts)")
	pingTreeStreamCmd.Flags().IntVar(&streamHops, "hops", 0,
		"ping ONLY entries at exactly N hops; other levels are discovered but not pinged")
	pingTreeStreamCmd.Flags().IntVarP(&streamTries, "tries", "t", 1,
		"per-transport ping count; PingResult carries aggregated stats")
	pingTreeStreamCmd.Flags().IntVarP(&streamSize, "size", "s", 2,
		"packet size in KB")
	pingTreeStreamCmd.Flags().IntVarP(&streamConcurrency, "concurrency", "c", 16,
		"max in-flight pings per BFS level")
	pingTreeStreamCmd.Flags().BoolVarP(&streamOnlineOnly, "online", "g", false,
		"only ping visors marked online in the uptime tracker")
	pingTreeStreamCmd.Flags().StringVarP(&streamMinVersion, "version", "v", "",
		"filter by minimum visor version (semver)")
	pingTreeStreamCmd.Flags().BoolVar(&streamUseTpLat, "use-transport-latency", true,
		"at level 1: skip the live ping when the transport already has a smoothed RTT in TransportSummary.LatencyMS")
	pingTreeStreamCmd.Flags().BoolVar(&streamDmsgOnly, "dmsg-only", false,
		"force the ping path to ride DMSG instead of the skywire router")
	pingTreeStreamCmd.Flags().BoolVar(&streamDmsgPreChk, "dmsg-precheck", false,
		"probe DMSG reachability before each route ping; discards unreachable visors early")
	pingTreeStreamCmd.Flags().IntVar(&streamRetries, "retries", 0,
		"retry attempts on failed pings")
	pingTreeStreamCmd.Flags().BoolVar(&streamDryRun, "dry-run", false,
		"discovery only; no PingResult events fire (every entry marked latency_source=skipped)")
	pingTreeStreamCmd.Flags().DurationVarP(&streamTimeout, "timeout", "o", 30*time.Second,
		"per-ping timeout (after route setup)")
	pingTreeStreamCmd.Flags().DurationVar(&streamSetupTO, "setup-timeout", 30*time.Second,
		"per-transport route-setup timeout")

	RootCmd.AddCommand(pingTreeStreamCmd)
}

var pingTreeStreamCmd = &cobra.Command{
	Use:   "tree-stream",
	Short: "Stream a server-side BFS ping-tree as NDJSON (for harness consumers + coding agents)",
	Long: `Stream a server-side BFS ping-tree as NDJSON to stdout.

The visor walks its own neighborhood breadth-first and streams one
event per discovered transport, per ping result, per level boundary,
and a final run summary. Each event is one JSON object per stdout
line (NDJSON).

Wire format (top-level fields per line):
  {"ts":"<RFC3339Nano>","type":"<event>","data":{...}}

Event types:
  discovered     — BFS added (transport, peer) to the level-N candidate set
  ping_result    — ping (or transport_summary cache hit) completed
  level_done     — every transport at level N has resolved
  run_done       — terminal event; carries totals + termination_reason
  status_update  — informational progress (in-flight count, phase)
  server_error   — unrecoverable condition; stream closes after this

Examples:

  # Hop-level latency measurement (synth's directive):
  skywire cli visor ping tree-stream --hops 1 --tries 5 | jq -c 'select(.type=="ping_result")'
  skywire cli visor ping tree-stream --hops 2 --tries 5 -l 2 | tee hops-2.ndjson
  skywire cli visor ping tree-stream --hops 3 --tries 5 -l 3 | tee hops-3.ndjson

  # Discovery only, no pings (visualize the reachable graph):
  skywire cli visor ping tree-stream --dry-run --max-level 2

  # Aggregate latency across all reachable levels:
  skywire cli visor ping tree-stream --tries 3 | jq -c 'select(.type=="ping_result") | {level:.data.level, avg_ms:(.data.ping_avg_ns/1e6)}'

The TUI variant of this command is still 'cli visor ping tree' —
that one keeps the Bubble Tea interactive surface and will be
rewired onto this same stream in a follow-up.`,
	Run: runPingTreeStream,
}

// runPingTreeStream connects to the local visor's gRPC server,
// invokes StreamPingTree with the flag-derived request, and writes
// one NDJSON line per event to stdout until the stream closes
// (RunDone, ServerError, or ctx-cancel from Ctrl+C).
func runPingTreeStream(cmd *cobra.Command, _ []string) {
	ctx, cancel := signalContext()
	defer cancel()

	client, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: gRPC client connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close() //nolint:errcheck

	// CLI flag inputs are int; proto fields are int32. None of these
	// values realistically exceed int32 max (max-level ~10, hops ~10,
	// tries ~100, packet size in KB ~64, concurrency ~256) — nolint
	// is the right call rather than runtime clamping.
	req := &rpcgrpc.PingTreeRequest{
		MaxLevel:            int32(streamMaxLevel), //nolint:gosec
		Hops:                int32(streamHops),     //nolint:gosec
		Tries:               int32(streamTries),    //nolint:gosec
		PacketSizeKb:        int32(streamSize),     //nolint:gosec
		PingTimeoutNs:       streamTimeout.Nanoseconds(),
		SetupTimeoutNs:      streamSetupTO.Nanoseconds(),
		Concurrency:         int32(streamConcurrency), //nolint:gosec
		OnlineOnly:          streamOnlineOnly,
		MinVersion:          streamMinVersion,
		UseTransportLatency: streamUseTpLat,
		DmsgOnly:            streamDmsgOnly,
		DmsgPreCheck:        streamDmsgPreChk,
		Retries:             int32(streamRetries), //nolint:gosec
		DryRun:              streamDryRun,
	}

	stream, err := client.StreamPingTree(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: StreamPingTree call: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	// protojson preserves snake_case field names; conversion from
	// proto-Message to encoding/json marshaler goes through
	// protojson.Marshal → []byte → json.RawMessage so the outer
	// envelope can carry the data unmolested.
	marshaler := protojson.MarshalOptions{
		UseProtoNames:   true, // snake_case fields, matches proto declarations
		EmitUnpopulated: false,
	}

	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			// EOF is normal stream-end; everything else is a problem.
			if recvErr.Error() == "EOF" || recvErr == context.Canceled {
				return
			}
			fmt.Fprintf(os.Stderr, "error: stream recv: %v\n", recvErr)
			os.Exit(1)
		}
		if err := emitOne(enc, marshaler, ev); err != nil {
			fmt.Fprintf(os.Stderr, "error: emit: %v\n", err)
			os.Exit(1)
		}
	}
}

// emitOne writes one NDJSON line: envelope with type+ts+data.
func emitOne(enc *json.Encoder, m protojson.MarshalOptions, ev *rpcgrpc.PingTreeEvent) error {
	typ, payload := classifyPayload(ev)
	rawData, err := m.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", typ, err)
	}
	envelope := struct {
		TS   string          `json:"ts"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}{
		TS:   time.Unix(0, ev.TimestampNs).UTC().Format(time.RFC3339Nano),
		Type: typ,
		Data: json.RawMessage(rawData),
	}
	return enc.Encode(envelope)
}

// classifyPayload returns the event-type discriminator string and
// the proto-Message contents for the marshaler. Centralized here so
// the wire format stays consistent — adding a new oneof variant
// means updating exactly this function.
func classifyPayload(ev *rpcgrpc.PingTreeEvent) (string, proto.Message) {
	switch p := ev.Payload.(type) {
	case *rpcgrpc.PingTreeEvent_Discovered:
		return "discovered", p.Discovered
	case *rpcgrpc.PingTreeEvent_PingResult:
		return "ping_result", p.PingResult
	case *rpcgrpc.PingTreeEvent_LevelDone:
		return "level_done", p.LevelDone
	case *rpcgrpc.PingTreeEvent_RunDone:
		return "run_done", p.RunDone
	case *rpcgrpc.PingTreeEvent_StatusUpdate:
		return "status_update", p.StatusUpdate
	case *rpcgrpc.PingTreeEvent_ServerError:
		return "server_error", p.ServerError
	default:
		// Unknown payload — emit an empty data block under a stable
		// type so consumers don't crash on unexpected events.
		return "unknown", &rpcgrpc.PingTreeStatusUpdate{}
	}
}

// signalContext returns a context that cancels on SIGINT/SIGTERM.
// Mirrors the helper from other CLI commands in this directory
// (kept inline rather than reaching into another file's private
// helper).
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}
