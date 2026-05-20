// Package ping — cmd/skywire-cli/commands/visor/ping/tree_stream.go:
// thin gRPC client for the StreamPingTree RPC. The visor does the
// BFS server-side and pushes PingTreeEvents over the stream; this
// command renders each event either as a human-readable row
// (default) or as one NDJSON object per stdout line (--json).
//
// Default output is built for direct human consumption — including
// a final aggregation table (avg/p50/p99/jitter grouped by hop
// count) so the latency-vs-hops measurement Synth asked about
// doesn't need a follow-up jq pipeline.
//
// --json switches to the NDJSON wire format consumed by external
// harnesses + coding agents:
//
//	{"ts":"<RFC3339Nano>","type":"<event>","data":{...}}
//
// `data` is the marshaled oneof payload with proto's snake_case
// field names preserved. NDJSON consumers can `jq -c .` to filter
// or stream-decode into the wire types via protojson.
package ping

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
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
	streamJSON        bool
	streamQuiet       bool
	streamOutFile     string
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
	pingTreeStreamCmd.Flags().BoolVar(&streamJSON, "json", false,
		"emit NDJSON on stdout (default: human-readable rows + per-hop summary)")
	pingTreeStreamCmd.Flags().BoolVarP(&streamQuiet, "quiet", "q", false,
		"in human mode: suppress per-event rows and print only the final summary")
	pingTreeStreamCmd.Flags().StringVarP(&streamOutFile, "output", "O", "",
		"append NDJSON of every event to FILE (independent of stdout mode)")

	RootCmd.AddCommand(pingTreeStreamCmd)
}

var pingTreeStreamCmd = &cobra.Command{
	Use:   "tree-stream",
	Short: "Stream a server-side BFS ping-tree as human-readable rows + summary (or NDJSON with --json)",
	Long: `Stream a server-side BFS ping-tree.

The visor walks its own neighborhood breadth-first and streams one
event per discovered transport, per ping result, per level boundary,
plus a final run summary.

Default stdout: human-readable rows + a final aggregation table
showing succeeded / failed counts and avg/p50/p99/jitter grouped by
hop count. The Synth-directive latency-vs-hops measurement reads
directly from this table — no jq pipeline required.

--json: emit NDJSON instead. One JSON object per stdout line:
  {"ts":"<RFC3339Nano>","type":"<event>","data":{...}}

--output FILE: write the NDJSON to FILE regardless of stdout mode,
so harness consumers can capture machine-readable data while the
operator watches the human stream.

Event types (visible in --json or in --output FILE):
  discovered     — BFS added (transport, peer) to the level-N candidate set
  ping_result    — ping (or transport_summary cache hit) completed
  level_done     — every transport at level N has resolved
  run_done       — terminal event; carries totals + termination_reason
  status_update  — informational progress (in-flight count, phase)
  server_error   — unrecoverable condition; stream closes after this

Examples:

  # Latency + jitter as a function of hops (Synth's directive):
  skywire cli visor ping tree-stream --hops 1 --tries 5
  skywire cli visor ping tree-stream --hops 2 --tries 5 -l 2
  skywire cli visor ping tree-stream --hops 3 --tries 5 -l 3

  # Same as above but keep only the summary table:
  skywire cli visor ping tree-stream --hops 2 --tries 5 -l 2 --quiet

  # Capture NDJSON for offline analysis while watching live rows:
  skywire cli visor ping tree-stream --tries 5 --output /tmp/tree.ndjson

  # Discovery only, no pings (visualize the reachable graph):
  skywire cli visor ping tree-stream --dry-run --max-level 2

The TUI variant is 'cli visor ping tree' for interactive use.`,
	Run: runPingTreeStream,
}

// runPingTreeStream connects to the local visor's gRPC server,
// invokes StreamPingTree with the flag-derived request, and dispatches
// each event to (a) human rows on stdout, (b) NDJSON on stdout, and
// (c) NDJSON to --output FILE — modes (a)/(b) are mutually exclusive
// via --json; (c) is always-on when --output is set.
func runPingTreeStream(_ *cobra.Command, _ []string) {
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

	// File tee (always NDJSON when --output is set).
	var fileEnc *json.Encoder
	if streamOutFile != "" {
		f, fErr := os.OpenFile(streamOutFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
		if fErr != nil {
			fmt.Fprintf(os.Stderr, "error: open --output file: %v\n", fErr)
			os.Exit(1)
		}
		defer f.Close() //nolint:errcheck
		fileEnc = json.NewEncoder(f)
	}

	stdoutEnc := json.NewEncoder(os.Stdout)
	marshaler := protojson.MarshalOptions{
		UseProtoNames:   true, // snake_case fields, matches proto declarations
		EmitUnpopulated: false,
	}

	stats := newPingTreeStats()
	startedAt := time.Now()

	if !streamJSON && !streamQuiet {
		fmt.Fprintln(os.Stderr, treeStreamHumanHeader(req))
	}

	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			// EOF is normal stream-end; everything else is a problem.
			if recvErr == io.EOF || recvErr == context.Canceled {
				break
			}
			fmt.Fprintf(os.Stderr, "error: stream recv: %v\n", recvErr)
			os.Exit(1)
		}

		// Always update stats so the summary is correct regardless of
		// stdout mode.
		stats.record(ev)

		// File tee always emits NDJSON.
		if fileEnc != nil {
			if err := emitOne(fileEnc, marshaler, ev); err != nil {
				fmt.Fprintf(os.Stderr, "error: emit to file: %v\n", err)
				os.Exit(1)
			}
		}

		// Stdout mode dispatch.
		switch {
		case streamJSON:
			if err := emitOne(stdoutEnc, marshaler, ev); err != nil {
				fmt.Fprintf(os.Stderr, "error: emit: %v\n", err)
				os.Exit(1)
			}
		case streamQuiet:
			// Suppress per-event rows; summary still prints at end.
		default:
			emitHumanRow(os.Stdout, ev, startedAt)
		}
	}

	// Final summary in human modes (default + --quiet). In --json
	// mode the consumer parses run_done out of the NDJSON stream.
	if !streamJSON {
		stats.printSummary(os.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Human output
// ---------------------------------------------------------------------------

// treeStreamHumanHeader is the one-line banner printed to stderr at
// the start of a human-mode run. Goes to stderr so it doesn't leak
// into the summary table when stdout is redirected to a file.
func treeStreamHumanHeader(req *rpcgrpc.PingTreeRequest) string {
	var parts []string
	if req.Hops > 0 {
		parts = append(parts, fmt.Sprintf("hops=%d", req.Hops))
	}
	if req.MaxLevel > 0 {
		parts = append(parts, fmt.Sprintf("max-level=%d", req.MaxLevel))
	}
	if req.Tries > 0 {
		parts = append(parts, fmt.Sprintf("tries=%d", req.Tries))
	}
	if req.DryRun {
		parts = append(parts, "dry-run")
	}
	return fmt.Sprintf("ping tree-stream → %s", strings.Join(parts, " "))
}

// emitHumanRow prints one event as a single line on the given writer.
// Discovered events are intentionally skipped — they fire in bulk at
// BFS-expansion time and would drown out the ping_result rows the
// operator actually wants to see. They're still in the --output file
// for offline analysis.
func emitHumanRow(w io.Writer, ev *rpcgrpc.PingTreeEvent, start time.Time) {
	elapsed := time.Since(start).Seconds()
	switch p := ev.Payload.(type) {
	case *rpcgrpc.PingTreeEvent_PingResult:
		r := p.PingResult
		glyph := "✓"
		if r.Canceled {
			glyph = "⊘"
		} else if r.Failed {
			glyph = "✗"
		}
		srcTag := "[live ]"
		if r.LatencySource == "transport_summary" {
			srcTag = "[cache]"
		} else if r.LatencySource == "skipped" {
			srcTag = "[skip ]"
		}
		statsBlock := ""
		if r.Failed {
			msg := r.PingErr
			if msg == "" {
				msg = r.SetupErr
			}
			if msg == "" {
				msg = r.CalcErr
			}
			statsBlock = msg
		} else if r.SampleCount > 1 {
			statsBlock = fmt.Sprintf("avg=%.1fms p50=%.1fms p99=%.1fms jit=%.1fms n=%d",
				float64(r.PingAvgNs)/1e6,
				float64(r.PingP50Ns)/1e6,
				float64(r.PingP99Ns)/1e6,
				float64(r.JitterNs)/1e6,
				r.SampleCount)
		} else {
			statsBlock = fmt.Sprintf("avg=%.1fms n=%d",
				float64(r.PingAvgNs)/1e6, r.SampleCount)
		}
		//nolint:errcheck // human-mode log write; errors here aren't actionable
		fmt.Fprintf(w, "[+%6.2fs] %s L%d hops=%d %s  %s  %s  %s\n",
			elapsed, glyph, r.Level, hopsFromLevel(r.Level), r.RemotePk, r.TpType, srcTag, statsBlock)
	case *rpcgrpc.PingTreeEvent_LevelDone:
		l := p.LevelDone
		//nolint:errcheck // human-mode log write; errors here aren't actionable
		fmt.Fprintf(w, "[+%6.2fs] --- level %d done: attempted=%d succeeded=%d failed=%d skipped_cached=%d ---\n",
			elapsed, l.Level, l.Attempted, l.Succeeded, l.Failed, l.SkippedCached)
	case *rpcgrpc.PingTreeEvent_RunDone:
		r := p.RunDone
		//nolint:errcheck // human-mode log write; errors here aren't actionable
		fmt.Fprintf(w, "[+%6.2fs] === run done: discovered=%d pinged=%d succ=%d fail=%d wall=%dms (%s) ===\n",
			elapsed, r.TotalDiscovered, r.TotalPinged, r.TotalSucceeded, r.TotalFailed,
			r.WallTimeNs/1e6, r.TerminationReason)
	case *rpcgrpc.PingTreeEvent_ServerError:
		e := p.ServerError
		fmt.Fprintf(w, "[+%6.2fs] !!! server error: %s: %s\n", elapsed, e.Code, e.Message) //nolint:errcheck
	}
}

// hopsFromLevel converts a BFS level (1-indexed) into a hop count.
// BFS level 1 is "direct neighbor" — one transport edge between
// local and remote, which is 1 hop.
func hopsFromLevel(level int32) int32 {
	return level
}

// ---------------------------------------------------------------------------
// Aggregation (per-hops summary)
// ---------------------------------------------------------------------------

// hopAgg accumulates the per-hop-count stats the run-end summary
// table reports. Only successful pings (not failed / not canceled /
// not skipped) contribute to the latency-distribution averages —
// failures only count toward attempted / failed.
type hopAgg struct {
	attempted     int
	succeeded     int
	failed        int
	skippedCached int
	avgSum        float64
	p50Sum        float64
	p99Sum        float64
	jitterSum     float64
	avgN          int
}

type pingTreeStats struct {
	perHops map[int32]*hopAgg
	runDone *rpcgrpc.PingTreeRunDone
}

func newPingTreeStats() *pingTreeStats {
	return &pingTreeStats{perHops: make(map[int32]*hopAgg)}
}

func (s *pingTreeStats) record(ev *rpcgrpc.PingTreeEvent) {
	switch p := ev.Payload.(type) {
	case *rpcgrpc.PingTreeEvent_PingResult:
		r := p.PingResult
		h := s.bucket(r.Level)
		h.attempted++
		switch {
		case r.Failed || r.Canceled:
			h.failed++
		case r.LatencySource == "skipped":
			h.skippedCached++
		default:
			h.succeeded++
			if r.LatencySource == "transport_summary" {
				h.skippedCached++
			}
			h.avgSum += float64(r.PingAvgNs) / 1e6
			h.p50Sum += float64(r.PingP50Ns) / 1e6
			h.p99Sum += float64(r.PingP99Ns) / 1e6
			h.jitterSum += float64(r.JitterNs) / 1e6
			h.avgN++
		}
	case *rpcgrpc.PingTreeEvent_RunDone:
		s.runDone = p.RunDone
	}
}

func (s *pingTreeStats) bucket(level int32) *hopAgg {
	if h, ok := s.perHops[level]; ok {
		return h
	}
	h := &hopAgg{}
	s.perHops[level] = h
	return h
}

// printSummary writes the per-hop-count aggregation table — the
// data Synth's directive asked for. Columns are right-aligned via
// text/tabwriter for clean alignment across viewport widths.
func (s *pingTreeStats) printSummary(w io.Writer) {
	if len(s.perHops) == 0 {
		fmt.Fprintln(w, "\n(no ping results)") //nolint:errcheck
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\n=== Per-hop summary (avg across entries) ===")                                 //nolint:errcheck
	fmt.Fprintln(tw, "hops\tattempted\tsucceeded\tfailed\tcached\tavg_ms\tp50_ms\tp99_ms\tjitter_ms") //nolint:errcheck

	keys := make([]int32, 0, len(s.perHops))
	for k := range s.perHops {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	for _, k := range keys {
		h := s.perHops[k]
		var avgStr, p50Str, p99Str, jitStr string
		if h.avgN > 0 {
			avgStr = fmt.Sprintf("%.2f", h.avgSum/float64(h.avgN))
			p50Str = fmt.Sprintf("%.2f", h.p50Sum/float64(h.avgN))
			p99Str = fmt.Sprintf("%.2f", h.p99Sum/float64(h.avgN))
			jitStr = fmt.Sprintf("%.2f", h.jitterSum/float64(h.avgN))
		} else {
			avgStr, p50Str, p99Str, jitStr = "-", "-", "-", "-"
		}
		//nolint:errcheck // human-mode summary; errors here aren't actionable
		fmt.Fprintf(tw, "%d\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%s\n",
			hopsFromLevel(k), h.attempted, h.succeeded, h.failed, h.skippedCached,
			avgStr, p50Str, p99Str, jitStr)
	}
	tw.Flush() //nolint:errcheck,gosec

	if s.runDone != nil {
		//nolint:errcheck // human-mode summary; errors here aren't actionable
		fmt.Fprintf(w, "\ntotals: discovered=%d pinged=%d succeeded=%d failed=%d wall=%dms reason=%s\n",
			s.runDone.TotalDiscovered, s.runDone.TotalPinged,
			s.runDone.TotalSucceeded, s.runDone.TotalFailed,
			s.runDone.WallTimeNs/1e6, s.runDone.TerminationReason)
	}
}

// ---------------------------------------------------------------------------
// NDJSON wire (--json + --output)
// ---------------------------------------------------------------------------

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
