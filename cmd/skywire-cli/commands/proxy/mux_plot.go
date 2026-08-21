// Package skysocksc cmd/skywire-cli/commands/proxy/mux_plot.go c4-vis-cli
package skysocksc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	plot "github.com/0magnet/plot-go"
	"github.com/guptarohit/asciigraph"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil/livetui"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

var (
	muxPlotApp      string
	muxPlotInterval time.Duration
	muxPlotWindow   int
	muxPlotHeight   int
	muxPlotSmooth   int
	muxPlotRecv     bool
	muxPlotTUI      bool
	muxPlotPipeline string

	// --pk mode: measure an ad-hoc mux route rather than poll a running app.
	muxPlotPK       string
	muxPlotRoutes   int
	muxPlotMinHops  int
	muxPlotDuration time.Duration
	muxPlotPolicy   string
	muxPlotSizeKb   int
	muxPlotOverride []string

	// muxPlotSubject is the caption/header label for the current run (the app
	// name in poll mode, or a peer descriptor in --pk mode).
	muxPlotSubject string
	// muxPlotEvents is a rolling log of policy leg-lifecycle / route events,
	// shown as markers beneath the chart in --pk mode.
	muxPlotEvents []string
)

func init() {
	muxPlotCmd.Flags().StringVarP(&muxPlotApp, "name", "n", "skysocks-client", "app name to plot (e.g. skysocks-client, vpn-client)")
	muxPlotCmd.Flags().DurationVarP(&muxPlotInterval, "interval", "i", time.Second, "sample/redraw interval")
	muxPlotCmd.Flags().IntVarP(&muxPlotWindow, "window", "w", 60, "samples of history to keep on screen")
	muxPlotCmd.Flags().IntVar(&muxPlotHeight, "height", 10, "rows per chart panel")
	muxPlotCmd.Flags().IntVar(&muxPlotSmooth, "smooth", 0, "shorthand for appending |sma:N to the pipeline (0 = none)")
	muxPlotCmd.Flags().StringVarP(&muxPlotPipeline, "pipeline", "p", "", "plot-go processing pipeline for the bandwidth series (default 'roc:<interval>'; e.g. 'roc:1|sma:5', 'roc:1|avg:5')")
	muxPlotCmd.Flags().BoolVar(&muxPlotRecv, "recv", false, "plot received bandwidth instead of sent")
	muxPlotCmd.Flags().BoolVar(&muxPlotTUI, "tui", false, "render in a bubbletea alt-screen (flicker-free, resize-aware) instead of ANSI redraw; poll mode only")
	// --pk mode: plot a controlled, self-measured mux route (StreamMuxBandwidth)
	// instead of a running app's route group — the reliable multi-leg demonstrator.
	muxPlotCmd.Flags().StringVar(&muxPlotPK, "pk", "", "measure an ad-hoc mux route to this peer PK instead of polling a running app")
	muxPlotCmd.Flags().IntVar(&muxPlotRoutes, "routes", 4, "[--pk] number of parallel routes to set up")
	muxPlotCmd.Flags().IntVar(&muxPlotMinHops, "min-hops", 2, "[--pk] route-finder min hops (>=2 excludes the direct transport)")
	muxPlotCmd.Flags().DurationVar(&muxPlotDuration, "duration", 5*time.Minute, "[--pk] how long to pump bytes")
	muxPlotCmd.Flags().StringVar(&muxPlotPolicy, "policy", "", "[--pk] routing-policy preset (e.g. preset:rotating-bw); empty = static mux")
	muxPlotCmd.Flags().IntVar(&muxPlotSizeKb, "size", 32, "[--pk] per-write block size in KB")
	muxPlotCmd.Flags().StringArrayVar(&muxPlotOverride, "override", nil, "[--pk] routing-policy CLI override key=value (repeatable): e.g. --override avoid_geo=RU,CN (geo-avoid), --override trusted_pks=<pk,pk> (trust-tiered), --override business_hours=9-17 (time-of-day)")
	addMuxSub(muxPlotCmd, "mux-plot")
}

var muxPlotCmd = &cobra.Command{
	Use:   "plot",
	Short: "Live per-leg bandwidth + RTT chart for a mux'd route group (terminal)",
	Long: `Live terminal chart of each multiplexed route's bandwidth and RTT.

Two sources:

  (default) watch a running app's route group. Reads the same per-leg
  telemetry the routing-policy on_tick ABI sees (RouteGroupMuxInfo) over a
  smooth gRPC server-stream (StreamRouteGroupMuxInfo), so it updates
  continuously like --pk mode rather than choppily per --interval; --interval
  sets the server-side sample cadence. Shows exactly the legs the app's route
  group has right now. Falls back to the unary poll against an older visor.

  --pk <peer>  measure a controlled, self-driven mux route to a peer you
  control (StreamMuxBandwidth) — the reliable multi-leg demonstrator, since
  it does not depend on a live app's (churn-prone) route group. With
  --policy preset:<name> the same per-leg {bps,rtt} stream feeds the policy
  (on_tick), so you watch an adaptive preset decide on measurement live;
  leg promote/demote/drop/fail events print as markers beneath the chart.

Both redraw two stacked charts — per-leg bandwidth (Mbps) over per-leg RTT
(ms), one colored line per route. A warm-standby (parked) leg reads 0
bandwidth but keeps its RTT and is tagged (standby).

Examples:
  skywire cli proxy mux plot                     # default app, 1s
  skywire cli proxy mux plot -n vpn-client -i 500ms
  skywire cli proxy mux plot --smooth 5 --window 90
  # custom plot-go pipeline for the bandwidth series (0magnet/plot-go):
  skywire cli proxy mux plot -p 'roc:1|sma:5'      # rate-of-change then smooth
  skywire cli proxy mux plot -p 'roc:1|avg:3'      # rate then block-average every 3
  # controlled 4-leg route to a peer, rotating-bw preset, 5 min:
  skywire cli proxy mux plot --pk <peer-pk> --routes 4 --policy preset:rotating-bw
  # static 5-wide multi-hop mux, watch received bandwidth:
  skywire cli proxy mux plot --pk <peer-pk> --routes 5 --min-hops 2 --recv`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		if muxPlotWindow < 8 {
			muxPlotWindow = 8
		}
		// Validate the plot-go pipeline spec up front so a typo fails with a
		// clear message rather than silently falling back per leg.
		if _, err := plot.ParsePipeline(effectivePipelineSpec()); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--pipeline: %w", err))
		}
		if muxPlotPK != "" {
			runMuxPlotStream(cmd)
			return
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		muxPlotSubject = muxPlotApp
		poll := func() (any, error) { return rpcClient.RouteGroupMuxInfo(muxPlotApp) }

		// --tui stays on the unary-poll path: livetui drives its own
		// interval Refresh loop (poll-shaped), so the blocking-Recv stream
		// doesn't fit it. The smooth-stream win is for the default ANSI
		// redraw below.
		if muxPlotTUI {
			runMuxPlot(poll)
			return
		}

		// Default: consume the StreamRouteGroupMuxInfo server-stream so the
		// plot updates smoothly/continuously (like --pk's StreamMuxBandwidth)
		// instead of the choppy per-tick unary gob poll. If the stream RPC is
		// unavailable (older visor / no gRPC surface) it returns false and we
		// transparently fall back to the unary poll — same output, just choppy.
		if !runMuxPlotInfoStream(cmd, poll) {
			runMuxPlot(poll)
		}
	},
}

// legTrack is one route's rolling history plus its identity for the legend.
type legTrack struct {
	label string
	bw    []float64 // Mbps, len == window (ring, oldest first)
	rtt   []float64 // ms
	// bwPipe processes this leg's bandwidth series (0magnet/plot-go): it turns
	// the raw cumulative byte counter into a rate (roc) and optionally smooths it
	// (sma/avg), the same job the hand-rolled byte-delta+EWMA used to do — but
	// via the shared plot pipeline so the -p spec controls it. One Pipeline per
	// leg (the processors are stateful per stream).
	bwPipe     *plot.Pipeline
	lastBw     float64 // held while the pipeline primes/fills its window
	color      asciigraph.AnsiColor
	standbyNow bool
}

// effectivePipelineSpec is the plot-go pipeline for the bandwidth series: the
// user's -p verbatim, else a default that converts the cumulative byte counter
// to a per-interval rate (roc), with --smooth appended as sma if given.
func effectivePipelineSpec() string {
	if muxPlotPipeline != "" {
		return muxPlotPipeline
	}
	spec := fmt.Sprintf("roc:%g", muxPlotInterval.Seconds())
	if muxPlotSmooth > 1 {
		spec += fmt.Sprintf("|sma:%d", muxPlotSmooth)
	}
	return spec
}

// newLegTrack builds a track with its own plot pipeline instance.
func newLegTrack(label string, color asciigraph.AnsiColor) *legTrack {
	pipe, err := plot.ParsePipeline(effectivePipelineSpec())
	if err != nil { // spec is validated at command start; fall back to identity pass-through
		pipe = plot.NewPipeline()
	}
	return &legTrack{
		label:  label,
		bw:     make([]float64, muxPlotWindow),
		rtt:    make([]float64, muxPlotWindow),
		bwPipe: pipe,
		color:  color,
	}
}

// pushBW feeds a cumulative byte counter through the leg's pipeline and returns
// the value for the ring: the pipeline's latest output (Mbps), or the previous
// value while the pipeline is still priming/filling a window (roc yields nothing
// on its first sample; sma yields nothing until its window fills).
func (t *legTrack) pushBW(cumBytes float64) float64 {
	outs := t.bwPipe.Process([]float64{cumBytes * 8 / 1e6}) // bytes -> megabits
	if n := len(outs); n > 0 {
		t.lastBw = outs[n-1]
	}
	return t.lastBw
}

var plotPalette = []asciigraph.AnsiColor{
	asciigraph.Cyan, asciigraph.Magenta, asciigraph.Green, asciigraph.Yellow,
	asciigraph.Blue, asciigraph.Red, asciigraph.LightGreen, asciigraph.White,
}

// runMuxPlot drives the poll → pipeline → redraw loop until ctrl+c.
// Two renderers share one data path: the default plotnet-style ANSI redraw,
// and (--tui) a bubbletea alt-screen via livetui — flicker-free, resize-aware,
// scrollback-preserving. The tracks map persists across livetui's interval
// Refresh calls via the closure; per-leg plot pipelines carry the rate state.
func runMuxPlot(poll func() (any, error)) {
	if muxPlotTUI {
		tracks := map[int]*legTrack{}
		err := livetui.Run(func(_ context.Context) (string, error) {
			rgs, pErr := pollMuxRGs(poll)
			if pErr == nil && len(rgs) > 0 {
				updateTracks(tracks, rgs[0])
			}
			return frame(tracks), nil
		}, livetui.Options{Title: "skywire mux · " + plotSubject(), Interval: muxPlotInterval})
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tracks := map[int]*legTrack{} // keyed by route index (stable for the rg's life)
	ticker := time.NewTicker(muxPlotInterval)
	defer ticker.Stop()

	for {
		rgs, err := pollMuxRGs(poll)
		if err == nil && len(rgs) > 0 {
			updateTracks(tracks, rgs[0])
			render(tracks)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "\rRouteGroupMuxInfo: %v", err)
		}
		select {
		case <-ctx.Done():
			fmt.Print("\n")
			return
		case <-ticker.C:
		}
	}
}

// runMuxPlotStream drives the --pk mode: it opens the StreamMuxBandwidth RPC
// (the same controlled far-end pump the NDJSON harness uses), and renders each
// per-leg sample live. With --policy set, the identical {bps,rtt} stream is what
// the policy's on_tick decides on, so the chart shows the preset adapting.
func runMuxPlotStream(cmd *cobra.Command) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	muxPlotSubject = fmt.Sprintf("mux→%s r%d%s", shortPK(muxPlotPK), muxPlotRoutes, leadIf(" ", muxPlotPolicy))

	client, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("gRPC client connect: %w", err))
	}
	defer client.Close() //nolint:errcheck,gosec

	overrides, err := parseMuxPlotOverrides(muxPlotOverride)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--override: %w", err))
	}

	req := &rpcgrpc.MuxBandwidthRequest{
		TargetPk:         muxPlotPK,
		Routes:           int32(muxPlotRoutes), //nolint:gosec
		DurationNs:       muxPlotDuration.Nanoseconds(),
		PacketSizeKb:     int32(muxPlotSizeKb),  //nolint:gosec
		MinHops:          int32(muxPlotMinHops), //nolint:gosec
		SetupTimeoutNs:   (30 * time.Second).Nanoseconds(),
		SampleIntervalNs: muxPlotInterval.Nanoseconds(),
		RoutingPolicy:    muxPlotPolicy,
		CliOverrides:     overrides,
	}
	stream, err := client.StreamMuxBandwidth(ctx, req)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("StreamMuxBandwidth: %w", err))
	}

	tracks := map[int]*legTrack{}
	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF || recvErr == context.Canceled {
				fmt.Print("\n")
				return
			}
			fmt.Fprintf(os.Stderr, "\nstream recv: %v\n", recvErr)
			return
		}
		switch p := ev.Payload.(type) {
		case *rpcgrpc.MuxBandwidthEvent_Sample:
			updateTracksFromLegs(tracks, p.Sample.Legs)
			render(tracks)
		case *rpcgrpc.MuxBandwidthEvent_RouteEstablished:
			if r := p.RouteEstablished; r.Failed {
				pushMuxPlotEvent(fmt.Sprintf("R%d ✗ setup-failed %s", r.RouteIndex, r.SetupErr))
			} else {
				pushMuxPlotEvent(fmt.Sprintf("R%d ✓ established", r.RouteIndex))
			}
		case *rpcgrpc.MuxBandwidthEvent_LegLifecycle:
			l := p.LegLifecycle
			pushMuxPlotEvent(fmt.Sprintf("R%d ⟳ %s gate=%s", l.RouteIndex, strings.ToUpper(l.Event), l.GateState))
		case *rpcgrpc.MuxBandwidthEvent_RouteFailure:
			pushMuxPlotEvent(fmt.Sprintf("R%d ✗ pump-failed %s", p.RouteFailure.RouteIndex, p.RouteFailure.ErrorMessage))
		case *rpcgrpc.MuxBandwidthEvent_Done:
			render(tracks)
			fmt.Printf("\ndone: %s\n", p.Done.TerminationReason)
			return
		case *rpcgrpc.MuxBandwidthEvent_Error:
			fmt.Fprintf(os.Stderr, "\nerror: %s: %s\n", p.Error.Code, p.Error.Message)
			return
		}
		select {
		case <-ctx.Done():
			fmt.Print("\n")
			return
		default:
		}
	}
}

// parseMuxPlotOverrides turns the repeatable `--override key=value` flag into
// the CLIOverrides map the routing-policy engine reads (the conditional presets
// key off avoid_geo / trusted_pks / business_hours). Returns nil for no flags so
// an absent --override leaves every preset on its built-in defaults. The value
// may itself contain '=' (only the first splits key from value); an empty key
// is rejected.
func parseMuxPlotOverrides(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid override %q (want key=value)", p)
		}
		out[k] = v
	}
	return out, nil
}

// runMuxPlotInfoStream drives the default (watch-a-running-app) mode over
// the StreamRouteGroupMuxInfo server-stream — the smooth continuous
// counterpart of runMuxPlot's per-tick unary poll. It blocks on
// stream.Recv() and repaints each sample as it lands, so the chart tracks
// the app's route group the way --pk mode tracks its ad-hoc route.
//
// Returns true once it has driven the stream (to EOF / ctrl+c / a mid-run
// error). Returns FALSE without rendering when the stream is unavailable —
// the gRPC endpoint won't connect, the RPC is unimplemented on an older
// visor (first Recv errors before any sample), etc. — so the caller can
// fall back to the unary poll. --interval sets the server-side sample
// cadence (sample_interval_ns).
func runMuxPlotInfoStream(cmd *cobra.Command, _ func() (any, error)) bool {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		// gRPC surface unreachable — let the caller poll instead.
		return false
	}
	defer client.Close() //nolint:errcheck,gosec

	stream, err := client.StreamRouteGroupMuxInfo(ctx, &rpcgrpc.StreamRouteGroupMuxInfoRequest{
		AppName:          muxPlotApp,
		SampleIntervalNs: muxPlotInterval.Nanoseconds(),
	})
	if err != nil {
		return false
	}

	tracks := map[int]*legTrack{}
	gotSample := false
	for {
		sample, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF || errors.Is(recvErr, context.Canceled) {
				fmt.Print("\n")
				return true
			}
			// A failure before the FIRST sample most likely means the RPC
			// is unimplemented on an older visor (gRPC resolves the method
			// lazily, so an absent handler surfaces here, not at open) —
			// fall back to the unary poll rather than erroring out.
			if !gotSample {
				return false
			}
			fmt.Fprintf(os.Stderr, "\nstream recv: %v\n", recvErr)
			return true
		}
		gotSample = true
		// Mirror the poll path: render only the first (primary) route group,
		// exactly as runMuxPlot consumes rgs[0]. An empty sample (app has no
		// route group yet) leaves the last frame in place.
		if rg, ok := firstMuxRouteGroup(sample); ok {
			updateTracks(tracks, rg)
			render(tracks)
		}
		select {
		case <-ctx.Done():
			fmt.Print("\n")
			return true
		default:
		}
	}
}

// firstMuxRouteGroup projects the first route group of a
// RouteGroupMuxInfoSample into the CLI-side muxRouteGroupInfo the render
// path (updateTracks) consumes — so the stream feeds exactly the same
// track-update code the unary poll does. Returns false for an empty sample.
func firstMuxRouteGroup(sample *rpcgrpc.RouteGroupMuxInfoSample) (muxRouteGroupInfo, bool) {
	if sample == nil || len(sample.RouteGroups) == 0 {
		return muxRouteGroupInfo{}, false
	}
	g := sample.RouteGroups[0]
	rg := muxRouteGroupInfo{
		MuxEnabled:  g.MuxEnabled,
		SACKEnabled: g.SackEnabled,
	}
	for _, leg := range g.Legs {
		rg.Legs = append(rg.Legs, muxLegInfo{
			Index:       int(leg.RouteIndex),
			TransportID: leg.TransportId,
			TpType:      leg.TransportKind,
			RemotePK:    leg.RemotePk,
			LatencyMS:   leg.LatencyMs,
			SentBytes:   leg.SentBytes,
			SentPackets: leg.SentPackets,
			RecvBytes:   leg.RecvBytes,
			RecvPackets: leg.RecvPackets,
			Retransmits: leg.Retransmits,
			Alive:       leg.Alive,
			Standby:     leg.Standby,
		})
	}
	return rg, true
}

// updateTracksFromLegs feeds one StreamMuxBandwidth sample's per-leg breakdown
// into the render tracks. Unlike the poll path it needs no byte-delta math —
// InstSendBps / LatencyMs / Standby come straight off the wire.
func updateTracksFromLegs(tracks map[int]*legTrack, legs []*rpcgrpc.MuxLegSample) {
	seen := map[int]bool{}
	for _, leg := range legs {
		idx := int(leg.RouteIndex)
		seen[idx] = true
		t, ok := tracks[idx]
		if !ok {
			t = newLegTrack(fmt.Sprintf("R%d·%s·%s", idx, orDash(leg.TransportKind), hopLabel(leg.IntermediatePk)),
				plotPalette[len(tracks)%len(plotPalette)])
			tracks[idx] = t
		}
		// Feed the cumulative byte counter through the pipeline (roc -> Mbps),
		// same as the poll path, so -p means the same thing on both sources.
		cum := float64(leg.BytesSent)
		if muxPlotRecv {
			cum = float64(leg.BytesRecv)
		}
		t.bw = append(t.bw[1:], t.pushBW(cum))
		t.rtt = append(t.rtt[1:], float64(leg.LatencyMs))
		t.standbyNow = leg.Standby
	}
	for idx, t := range tracks {
		if !seen[idx] {
			t.bw = append(t.bw[1:], 0)
			t.rtt = append(t.rtt[1:], t.rtt[len(t.rtt)-1])
		}
	}
}

// pushMuxPlotEvent appends a timestamped policy/route event to the rolling log
// rendered beneath the chart.
func pushMuxPlotEvent(s string) {
	muxPlotEvents = append(muxPlotEvents, time.Now().Format("15:04:05")+" "+s)
	if len(muxPlotEvents) > 64 {
		muxPlotEvents = muxPlotEvents[len(muxPlotEvents)-64:]
	}
}

// orDash renders an empty transport kind as "-".
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// hopLabel renders a leg's intermediate PK short, or "direct" when it has none.
func hopLabel(pk string) string {
	if pk == "" {
		return "direct"
	}
	return shortPK(pk)
}

// leadIf returns prefix+s when s is non-empty, else "".
func leadIf(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}

func pollMuxRGs(poll func() (any, error)) ([]muxRouteGroupInfo, error) {
	v, err := poll()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var rgs []muxRouteGroupInfo
	if err := json.Unmarshal(b, &rgs); err != nil {
		return nil, err
	}
	return rgs, nil
}

func updateTracks(tracks map[int]*legTrack, rg muxRouteGroupInfo) {
	seen := map[int]bool{}
	for _, leg := range rg.Legs {
		seen[leg.Index] = true
		t, ok := tracks[leg.Index]
		if !ok {
			t = newLegTrack(fmt.Sprintf("R%d·%s·%s", leg.Index, leg.TpType, shortPK(leg.RemotePK)),
				plotPalette[len(tracks)%len(plotPalette)])
			tracks[leg.Index] = t
		}
		cum := float64(leg.SentBytes)
		if muxPlotRecv {
			cum = float64(leg.RecvBytes)
		}
		t.bw = append(t.bw[1:], t.pushBW(cum))
		t.rtt = append(t.rtt[1:], leg.LatencyMS)
		t.standbyNow = leg.Standby
	}
	// legs absent this poll: push a 0 sample so a dropped/rotated-out leg decays off-screen.
	for idx, t := range tracks {
		if !seen[idx] {
			t.bw = append(t.bw[1:], 0)
			t.rtt = append(t.rtt[1:], t.rtt[len(t.rtt)-1])
		}
	}
}

// render paints one frame to stdout with a clear+home (the plotnet-style ANSI
// redraw). frame() builds the same content as a string for the --tui renderer.
func render(tracks map[int]*legTrack) {
	f := frame(tracks)
	if f == "" {
		return
	}
	fmt.Print("\x1b[H\x1b[2J" + f) // clear + home, then the frame
}

// frame assembles one chart frame (bandwidth panel over RTT panel, legend,
// aggregate, event markers) as a string, without any screen-clear escapes — so
// it can be piped either through render's ANSI redraw or into the bubbletea
// alt-screen viewport (livetui) in --tui mode. Empty string when there are no
// legs to draw yet.
func frame(tracks map[int]*legTrack) string {
	idxs := make([]int, 0, len(tracks))
	for i := range tracks {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	if len(idxs) == 0 {
		return ""
	}
	var bwSeries, rttSeries [][]float64
	var colors []asciigraph.AnsiColor
	var legend, agg string
	total := 0.0
	for _, i := range idxs {
		t := tracks[i]
		bwSeries = append(bwSeries, t.bw)
		rttSeries = append(rttSeries, t.rtt)
		colors = append(colors, t.color)
		cur := t.bw[len(t.bw)-1]
		total += cur
		gate := ""
		if t.standbyNow {
			gate = " (standby)"
		}
		legend += fmt.Sprintf("%s%s\x1b[0m %s %4.1fMb %3.0fms%s   ",
			ansi(t.color), "●", t.label, cur, t.rtt[len(t.rtt)-1], gate)
	}
	dir := "sent"
	if muxPlotRecv {
		dir = "recv"
	}
	agg = fmt.Sprintf("aggregate %s: %.1f Mbps across %d legs", dir, total, len(idxs))

	subject := plotSubject()
	bw := asciigraph.PlotMany(bwSeries, asciigraph.Height(muxPlotHeight), asciigraph.Width(0),
		asciigraph.SeriesColors(colors...), asciigraph.Precision(1),
		asciigraph.Caption(fmt.Sprintf("bandwidth  Mbps %s  ·  %s", dir, subject)))
	rt := asciigraph.PlotMany(rttSeries, asciigraph.Height(muxPlotHeight/2+2), asciigraph.Width(0),
		asciigraph.SeriesColors(colors...), asciigraph.Precision(0),
		asciigraph.Caption("RTT  ms"))

	var b strings.Builder
	fmt.Fprintf(&b, "skywire mux · %s · %s\n\n", subject, time.Now().Format("15:04:05"))
	b.WriteString(bw)
	b.WriteString("\n\n\n")
	b.WriteString(rt)
	fmt.Fprintf(&b, "\n\n%s\n%s\n", legend, agg)
	if len(muxPlotEvents) > 0 {
		fmt.Fprintf(&b, "\nevents: %s\n", strings.Join(tailStrings(muxPlotEvents, 4), "   "))
	}
	return b.String()
}

// tailStrings returns the last n elements of s (or all if fewer).
func tailStrings(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ansi renders an asciigraph color as its SGR escape for the legend swatches.
func ansi(c asciigraph.AnsiColor) string { return fmt.Sprintf("\x1b[38;5;%dm", int(c)) }

// plotSubject is the caption/title label for the current run (peer descriptor in
// --pk mode, else the app name).
func plotSubject() string {
	if muxPlotSubject != "" {
		return muxPlotSubject
	}
	return muxPlotApp
}
