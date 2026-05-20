// Package ping — cmd/skywire-cli/commands/visor/ping/mux_bandwidth.go:
// CLI consumer for the StreamMuxBandwidth gRPC RPC.
//
// Operator-visible: `cli visor ping mux-bw <pk>` runs the
// multiplexed-route bandwidth test against a single peer.
//
// Default stdout: human-readable rows + a final summary with avg /
// peak throughput, RTT distribution stats (when --probe-rtt is
// set), and the termination reason — the queueing-delay
// measurement reads off the summary table directly, no jq needed.
//
// --json: emit NDJSON instead. Same envelope shape as ping
// tree-stream, so treeprobe-style harnesses can consume either.
//
// --output FILE: NDJSON tee independent of stdout mode.
//
// Run shapes (the operator's hypothesis matrix):
//
//	# Baseline — single route, no min_hops (picks direct if available)
//	cli visor ping mux-bw <pk> --duration 30s
//
//	# Single non-direct path — forces at least one intermediate
//	cli visor ping mux-bw <pk> --duration 30s --min-hops 2
//
//	# N parallel routes excluding direct — the operator's hypothesis
//	# call: should sum to MORE bandwidth than direct
//	cli visor ping mux-bw <pk> --duration 30s --routes 4 --min-hops 2
//
//	# Queueing-delay measurement: bandwidth + concurrent RTT probe
//	cli visor ping mux-bw <pk> --duration 30s --probe-rtt
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
	muxBwRoutes         int
	muxBwDuration       time.Duration
	muxBwPacketSizeKb   int
	muxBwMinHops        int
	muxBwSetupTimeout   time.Duration
	muxBwProbeRTT       bool
	muxBwProbeInterval  time.Duration
	muxBwSampleInterval time.Duration
	muxBwLocalRoute     bool
	muxBwJSON           bool
	muxBwQuiet          bool
	muxBwOutFile        string
)

func init() {
	muxBandwidthCmd.Flags().IntVarP(&muxBwRoutes, "routes", "r", 1,
		"number of parallel route connections (1 = baseline single route)")
	muxBandwidthCmd.Flags().DurationVarP(&muxBwDuration, "duration", "d", 30*time.Second,
		"how long to pump bytes (excludes route-setup time)")
	muxBandwidthCmd.Flags().IntVarP(&muxBwPacketSizeKb, "size", "s", 32,
		"per-write block size in KB")
	muxBandwidthCmd.Flags().IntVar(&muxBwMinHops, "min-hops", 0,
		"route-finder min hops constraint (>=2 excludes the direct transport)")
	muxBandwidthCmd.Flags().DurationVar(&muxBwSetupTimeout, "setup-timeout", 30*time.Second,
		"per-route setup timeout")
	muxBandwidthCmd.Flags().BoolVar(&muxBwProbeRTT, "probe-rtt", false,
		"also send small-packet RTT probes during the load (queueing-delay measurement)")
	muxBandwidthCmd.Flags().DurationVar(&muxBwProbeInterval, "probe-interval", 100*time.Millisecond,
		"interval between RTT probes when --probe-rtt is set")
	muxBandwidthCmd.Flags().DurationVar(&muxBwSampleInterval, "sample-interval", 1*time.Second,
		"interval between MuxBandwidthSample events")
	muxBandwidthCmd.Flags().BoolVar(&muxBwLocalRoute, "local-route", false,
		"use locally-cached TPD data for route calculation (faster setup; may be stale)")
	muxBandwidthCmd.Flags().BoolVar(&muxBwJSON, "json", false,
		"emit NDJSON on stdout (default: human-readable rows + summary)")
	muxBandwidthCmd.Flags().BoolVarP(&muxBwQuiet, "quiet", "q", false,
		"in human mode: suppress per-event rows; print only setup + summary")
	muxBandwidthCmd.Flags().StringVarP(&muxBwOutFile, "output", "O", "",
		"append NDJSON of every event to FILE (independent of stdout mode)")

	RootCmd.AddCommand(muxBandwidthCmd)
}

var muxBandwidthCmd = &cobra.Command{
	Use:   "mux-bw <pk>",
	Short: "Multiplexed-route bandwidth + queueing-delay probe (human output by default; --json for NDJSON)",
	Long: `Pump bytes from this visor to a peer visor across N parallel
routes, optionally probing RTT during the load to capture queueing
delay.

Default output: human-readable per-second sample rows + a final
summary with per-route setup times, totals, peak rates, the loaded
RTT distribution (when --probe-rtt is set), and the termination
reason. The queueing-delay measurement reads off that summary
without piping through jq.

--json: NDJSON envelope per event, same shape as tree-stream:
  {"ts":"<RFC3339Nano>","type":"route_established|sample|rtt_probe|done|error","data":{...}}

--output FILE: tee NDJSON to FILE regardless of stdout mode.

Run shapes:

  --routes 1                        # single route, route-finder picks freely
  --routes 1 --min-hops 2           # single non-direct path
  --routes N --min-hops 2           # N parallel routes excluding direct

Pair with --probe-rtt to capture loaded-RTT samples concurrent
with the bulk pump. The loaded RTT minus the unloaded baseline IS
the queueing delay.

Examples:

  # Baseline single direct route, 30s, with RTT probing:
  skywire cli visor ping mux-bw <peer-pk> --probe-rtt

  # Mux 4 routes excluding direct, 60s, with RTT probing:
  skywire cli visor ping mux-bw <peer-pk> --routes 4 --min-hops 2 --duration 60s --probe-rtt

  # Same run but capture NDJSON for offline analysis:
  skywire cli visor ping mux-bw <peer-pk> --routes 4 --output /tmp/mux.ndjson`,
	Args: cobra.ExactArgs(1),
	Run:  runMuxBandwidth,
}

func runMuxBandwidth(_ *cobra.Command, args []string) {
	targetPK := args[0]

	ctx, cancel := muxBwSignalContext()
	defer cancel()

	client, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mux-bw: gRPC client connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close() //nolint:errcheck

	req := &rpcgrpc.MuxBandwidthRequest{
		TargetPk:         targetPK,
		Routes:           int32(muxBwRoutes), //nolint:gosec
		DurationNs:       muxBwDuration.Nanoseconds(),
		PacketSizeKb:     int32(muxBwPacketSizeKb), //nolint:gosec
		MinHops:          int32(muxBwMinHops),      //nolint:gosec
		SetupTimeoutNs:   muxBwSetupTimeout.Nanoseconds(),
		ProbeRtt:         muxBwProbeRTT,
		ProbeIntervalNs:  muxBwProbeInterval.Nanoseconds(),
		SampleIntervalNs: muxBwSampleInterval.Nanoseconds(),
		LocalRoute:       muxBwLocalRoute,
	}

	stream, err := client.StreamMuxBandwidth(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mux-bw: StreamMuxBandwidth call: %v\n", err)
		os.Exit(1)
	}

	stdoutEnc := json.NewEncoder(os.Stdout)
	marshaler := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: false,
	}

	var fileEnc *json.Encoder
	if muxBwOutFile != "" {
		f, fErr := os.OpenFile(muxBwOutFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
		if fErr != nil {
			fmt.Fprintf(os.Stderr, "mux-bw: open --output file: %v\n", fErr)
			os.Exit(1)
		}
		defer f.Close() //nolint:errcheck
		fileEnc = json.NewEncoder(f)
	}

	tracker := newMuxBwTracker()
	startedAt := time.Now()

	if !muxBwJSON && !muxBwQuiet {
		fmt.Fprintln(os.Stderr, muxBwHumanHeader(targetPK, req))
	}

	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF || recvErr == context.Canceled {
				break
			}
			fmt.Fprintf(os.Stderr, "mux-bw: stream recv: %v\n", recvErr)
			os.Exit(1)
		}

		tracker.record(ev)

		if fileEnc != nil {
			if err := emitMuxBwOne(fileEnc, marshaler, ev); err != nil {
				fmt.Fprintf(os.Stderr, "mux-bw: emit to file: %v\n", err)
				os.Exit(1)
			}
		}

		switch {
		case muxBwJSON:
			if err := emitMuxBwOne(stdoutEnc, marshaler, ev); err != nil {
				fmt.Fprintf(os.Stderr, "mux-bw: emit: %v\n", err)
				os.Exit(1)
			}
		case muxBwQuiet:
			// Suppress per-event rows.
		default:
			muxBwEmitHumanRow(os.Stdout, ev, startedAt)
		}
	}

	if !muxBwJSON {
		tracker.printSummary(os.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Human output
// ---------------------------------------------------------------------------

func muxBwHumanHeader(targetPK string, req *rpcgrpc.MuxBandwidthRequest) string {
	parts := []string{
		fmt.Sprintf("routes=%d", req.Routes),
		fmt.Sprintf("duration=%s", time.Duration(req.DurationNs)),
	}
	if req.MinHops > 0 {
		parts = append(parts, fmt.Sprintf("min-hops=%d", req.MinHops))
	}
	if req.ProbeRtt {
		parts = append(parts, "probe-rtt")
	}
	return fmt.Sprintf("mux-bw → %s  %s", targetPK, strings.Join(parts, " "))
}

func muxBwEmitHumanRow(w io.Writer, ev *rpcgrpc.MuxBandwidthEvent, start time.Time) {
	elapsed := time.Since(start).Seconds()
	switch p := ev.Payload.(type) {
	case *rpcgrpc.MuxBandwidthEvent_RouteEstablished:
		r := p.RouteEstablished
		if r.Failed {
			//nolint:errcheck // human-mode log write; errors here aren't actionable
			fmt.Fprintf(w, "[+%6.2fs] R%d ✗ FAILED %s (setup=%s)\n",
				elapsed, r.RouteIndex, r.SetupErr, fmtNs(r.SetupLatencyNs))
			return
		}
		// Compact hops summary: just count + final destination so
		// the row stays one line wide.
		path := ""
		if len(r.Hops) > 0 {
			path = " hops=" + muxBwRenderHopPath(r.Hops)
		}
		//nolint:errcheck // human-mode log write; errors here aren't actionable
		fmt.Fprintf(w, "[+%6.2fs] R%d ✓ established setup=%s%s\n",
			elapsed, r.RouteIndex, fmtNs(r.SetupLatencyNs), path)
	case *rpcgrpc.MuxBandwidthEvent_Sample:
		s := p.Sample
		//nolint:errcheck // human-mode log write; errors here aren't actionable
		fmt.Fprintf(w, "[+%6.2fs] sample  send=%s recv=%s  cum=%s/%s  active=%d\n",
			elapsed, fmtBps(s.InstantSendBps), fmtBps(s.InstantRecvBps),
			fmtBytes(s.BytesSent), fmtBytes(s.BytesReceived), s.ActiveRoutes)
	case *rpcgrpc.MuxBandwidthEvent_RttProbe:
		r := p.RttProbe
		if r.LatencyNs == 0 && r.Error != "" {
			fmt.Fprintf(w, "[+%6.2fs] probe   seq=%d FAILED %s\n", elapsed, r.Sequence, r.Error) //nolint:errcheck
		}
		// Successful probes are quiet on stdout to avoid drowning out
		// sample rows; they still show up in the final RTT
		// distribution + NDJSON file. Use --json or --output to see
		// every probe.
	case *rpcgrpc.MuxBandwidthEvent_Done:
		d := p.Done
		//nolint:errcheck // human-mode log write; errors here aren't actionable
		fmt.Fprintf(w, "[+%6.2fs] done    sent=%s recv=%s  avg=%s/%s  peak=%s/%s  (%s)\n",
			elapsed,
			fmtBytes(d.TotalBytesSent), fmtBytes(d.TotalBytesReceived),
			fmtBps(d.AvgSendBps), fmtBps(d.AvgRecvBps),
			fmtBps(d.PeakSendBps), fmtBps(d.PeakRecvBps),
			d.TerminationReason)
	case *rpcgrpc.MuxBandwidthEvent_Error:
		e := p.Error
		fmt.Fprintf(w, "[+%6.2fs] !!! error: %s: %s\n", elapsed, e.Code, e.Message) //nolint:errcheck
	}
}

// muxBwRenderHopPath returns "A→B→C" given a Hops slice. The hop's
// `From` is the previous node, `To` is the next — chaining them
// produces the full path. Used in the streaming row + the summary
// table so the operator sees which intermediates were chosen.
func muxBwRenderHopPath(hops []*rpcgrpc.RouteHop) string {
	if len(hops) == 0 {
		return ""
	}
	pieces := make([]string, 0, len(hops)+1)
	pieces = append(pieces, hops[0].From)
	for _, h := range hops {
		pieces = append(pieces, h.To)
	}
	return strings.Join(pieces, "→")
}

// ---------------------------------------------------------------------------
// Aggregation
// ---------------------------------------------------------------------------

type muxBwTracker struct {
	routeSet []*rpcgrpc.MuxRouteEstablished
	done     *rpcgrpc.MuxBandwidthDone
	srvErr   string
}

func newMuxBwTracker() *muxBwTracker {
	return &muxBwTracker{}
}

func (t *muxBwTracker) record(ev *rpcgrpc.MuxBandwidthEvent) {
	switch p := ev.Payload.(type) {
	case *rpcgrpc.MuxBandwidthEvent_RouteEstablished:
		t.routeSet = append(t.routeSet, p.RouteEstablished)
	case *rpcgrpc.MuxBandwidthEvent_Done:
		t.done = p.Done
	case *rpcgrpc.MuxBandwidthEvent_Error:
		t.srvErr = p.Error.Message
	}
}

func (t *muxBwTracker) printSummary(w io.Writer) {
	//nolint:errcheck // human-mode summary; errors here aren't actionable
	fmt.Fprintln(w, "\n=== Summary ===")

	if len(t.routeSet) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		//nolint:errcheck // human-mode summary; errors here aren't actionable
		fmt.Fprintln(tw, "route\tstate\tsetup_ms\thops\tpath")
		sort.Slice(t.routeSet, func(i, j int) bool {
			return t.routeSet[i].RouteIndex < t.routeSet[j].RouteIndex
		})
		for _, r := range t.routeSet {
			state := "✓"
			if r.Failed {
				state = "✗"
			}
			path := "-"
			if len(r.Hops) > 0 {
				path = muxBwRenderHopPath(r.Hops)
			}
			extra := ""
			if r.Failed {
				extra = " " + r.SetupErr
			}
			//nolint:errcheck // human-mode summary; errors here aren't actionable
			fmt.Fprintf(tw, "R%d\t%s\t%.1f\t%d\t%s%s\n",
				r.RouteIndex, state,
				float64(r.SetupLatencyNs)/1e6, len(r.Hops), path, extra)
		}
		tw.Flush() //nolint:errcheck,gosec
	}

	if t.done == nil {
		if t.srvErr != "" {
			fmt.Fprintf(w, "\nrun ended with server error: %s\n", t.srvErr) //nolint:errcheck
			return
		}
		fmt.Fprintln(w, "\n(no Done event — run interrupted)") //nolint:errcheck
		return
	}

	d := t.done
	fmt.Fprintln(w, "") //nolint:errcheck
	//nolint:errcheck // human-mode summary; errors here aren't actionable
	fmt.Fprintf(w, "routes:    requested=%d established=%d\n",
		d.RoutesRequested, d.RoutesEstablished)
	//nolint:errcheck // human-mode summary; errors here aren't actionable
	fmt.Fprintf(w, "duration:  wall=%s pump=%s setup_total=%s\n",
		fmtNs(d.WallTimeNs), fmtNs(d.PumpTimeNs), fmtNs(d.SetupTotalNs))
	//nolint:errcheck // human-mode summary; errors here aren't actionable
	fmt.Fprintf(w, "sent:      %s  avg=%s  peak=%s\n",
		fmtBytes(d.TotalBytesSent), fmtBps(d.AvgSendBps), fmtBps(d.PeakSendBps))
	//nolint:errcheck // human-mode summary; errors here aren't actionable
	fmt.Fprintf(w, "recv:      %s  avg=%s  peak=%s\n",
		fmtBytes(d.TotalBytesReceived), fmtBps(d.AvgRecvBps), fmtBps(d.PeakRecvBps))
	if d.ProbeCount > 0 {
		//nolint:errcheck // human-mode summary; errors here aren't actionable
		fmt.Fprintf(w, "rtt load:  n=%d  avg=%s  p50=%s  p99=%s  jitter=%s\n",
			d.ProbeCount,
			fmtNs(d.ProbeAvgNs), fmtNs(d.ProbeP50Ns),
			fmtNs(d.ProbeP99Ns), fmtNs(d.ProbeJitterNs))
	}
	fmt.Fprintf(w, "reason:    %s\n", d.TerminationReason) //nolint:errcheck
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

// fmtBps / fmtBytes / fmtNs are non-conflicting with the formatBps /
// formatBytes / formatDuration helpers in mux_bandwidth_tui.go;
// distinct names prevent confusion since the TUI version emits
// fixed-width strings for column alignment, while these emit
// compact natural-width strings for line-flowing log output.

func fmtBps(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.2fGbps", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.2fMbps", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.2fKbps", v/1e3)
	default:
		return fmt.Sprintf("%.0fbps", v)
	}
}

func fmtBytes(v uint64) string {
	f := float64(v)
	switch {
	case f >= 1<<30:
		return fmt.Sprintf("%.2fGiB", f/(1<<30))
	case f >= 1<<20:
		return fmt.Sprintf("%.2fMiB", f/(1<<20))
	case f >= 1<<10:
		return fmt.Sprintf("%.2fKiB", f/(1<<10))
	default:
		return fmt.Sprintf("%dB", v)
	}
}

func fmtNs(ns int64) string {
	switch {
	case ns >= int64(time.Second):
		return fmt.Sprintf("%.2fs", float64(ns)/float64(time.Second))
	case ns >= int64(time.Millisecond):
		return fmt.Sprintf("%.1fms", float64(ns)/float64(time.Millisecond))
	case ns >= int64(time.Microsecond):
		return fmt.Sprintf("%.1fµs", float64(ns)/float64(time.Microsecond))
	default:
		return fmt.Sprintf("%dns", ns)
	}
}

// ---------------------------------------------------------------------------
// NDJSON wire (--json + --output)
// ---------------------------------------------------------------------------

func emitMuxBwOne(enc *json.Encoder, m protojson.MarshalOptions, ev *rpcgrpc.MuxBandwidthEvent) error {
	typ, payload := classifyMuxBwEvent(ev)
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

func classifyMuxBwEvent(ev *rpcgrpc.MuxBandwidthEvent) (string, proto.Message) {
	switch p := ev.Payload.(type) {
	case *rpcgrpc.MuxBandwidthEvent_RouteEstablished:
		return "route_established", p.RouteEstablished
	case *rpcgrpc.MuxBandwidthEvent_Sample:
		return "sample", p.Sample
	case *rpcgrpc.MuxBandwidthEvent_RttProbe:
		return "rtt_probe", p.RttProbe
	case *rpcgrpc.MuxBandwidthEvent_Done:
		return "done", p.Done
	case *rpcgrpc.MuxBandwidthEvent_Error:
		return "error", p.Error
	}
	return "unknown", &rpcgrpc.MuxBandwidthError{}
}

func muxBwSignalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}
