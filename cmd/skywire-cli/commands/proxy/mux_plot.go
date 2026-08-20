// Package skysocksc cmd/skywire-cli/commands/proxy/mux_plot.go c4-vis-cli
package skysocksc

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
	"time"

	"github.com/guptarohit/asciigraph"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
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

	// --pk mode: measure an ad-hoc mux route rather than poll a running app.
	muxPlotPK       string
	muxPlotRoutes   int
	muxPlotMinHops  int
	muxPlotDuration time.Duration
	muxPlotPolicy   string
	muxPlotSizeKb   int

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
	muxPlotCmd.Flags().IntVar(&muxPlotSmooth, "smooth", 0, "EWMA smoothing window in samples (0 = raw)")
	muxPlotCmd.Flags().BoolVar(&muxPlotRecv, "recv", false, "plot received bandwidth instead of sent")
	// --pk mode: plot a controlled, self-measured mux route (StreamMuxBandwidth)
	// instead of a running app's route group — the reliable multi-leg demonstrator.
	muxPlotCmd.Flags().StringVar(&muxPlotPK, "pk", "", "measure an ad-hoc mux route to this peer PK instead of polling a running app")
	muxPlotCmd.Flags().IntVar(&muxPlotRoutes, "routes", 4, "[--pk] number of parallel routes to set up")
	muxPlotCmd.Flags().IntVar(&muxPlotMinHops, "min-hops", 2, "[--pk] route-finder min hops (>=2 excludes the direct transport)")
	muxPlotCmd.Flags().DurationVar(&muxPlotDuration, "duration", 5*time.Minute, "[--pk] how long to pump bytes")
	muxPlotCmd.Flags().StringVar(&muxPlotPolicy, "policy", "", "[--pk] routing-policy preset (e.g. preset:rotating-bw); empty = static mux")
	muxPlotCmd.Flags().IntVar(&muxPlotSizeKb, "size", 32, "[--pk] per-write block size in KB")
	addMuxSub(muxPlotCmd, "mux-plot")
}

var muxPlotCmd = &cobra.Command{
	Use:   "plot",
	Short: "Live per-leg bandwidth + RTT chart for a mux'd route group (terminal)",
	Long: `Live terminal chart of each multiplexed route's bandwidth and RTT.

Two sources:

  (default) poll a running app's route group. Reads the same per-leg
  telemetry the routing-policy on_tick ABI sees (RouteGroupMuxInfo) once
  per --interval. Shows exactly the legs the app's route group has right now.

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
  # controlled 4-leg route to a peer, rotating-bw preset, 5 min:
  skywire cli proxy mux plot --pk <peer-pk> --routes 4 --policy preset:rotating-bw
  # static 5-wide multi-hop mux, watch received bandwidth:
  skywire cli proxy mux plot --pk <peer-pk> --routes 5 --min-hops 2 --recv`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		if muxPlotWindow < 8 {
			muxPlotWindow = 8
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
		runMuxPlot(func() (any, error) { return rpcClient.RouteGroupMuxInfo(muxPlotApp) })
	},
}

// legTrack is one route's rolling history plus its identity for the legend.
type legTrack struct {
	label      string
	bw         []float64 // Mbps, len == window (ring, oldest first)
	rtt        []float64 // ms
	prevSent   uint64
	prevRecv   uint64
	color      asciigraph.AnsiColor
	ewmaBw     float64
	ewmaSeeded bool
	standbyNow bool
}

var plotPalette = []asciigraph.AnsiColor{
	asciigraph.Cyan, asciigraph.Magenta, asciigraph.Green, asciigraph.Yellow,
	asciigraph.Blue, asciigraph.Red, asciigraph.LightGreen, asciigraph.White,
}

// runMuxPlot drives the poll → ring-buffer → redraw loop until ctrl+c.
func runMuxPlot(poll func() (any, error)) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tracks := map[int]*legTrack{} // keyed by route index (stable for the rg's life)
	var prevAt time.Time
	ticker := time.NewTicker(muxPlotInterval)
	defer ticker.Stop()

	for {
		now := time.Now()
		rgs, err := pollMuxRGs(poll)
		if err == nil && len(rgs) > 0 {
			updateTracks(tracks, rgs[0], now, prevAt)
			prevAt = now
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

	req := &rpcgrpc.MuxBandwidthRequest{
		TargetPk:         muxPlotPK,
		Routes:           int32(muxPlotRoutes), //nolint:gosec
		DurationNs:       muxPlotDuration.Nanoseconds(),
		PacketSizeKb:     int32(muxPlotSizeKb),  //nolint:gosec
		MinHops:          int32(muxPlotMinHops), //nolint:gosec
		SetupTimeoutNs:   (30 * time.Second).Nanoseconds(),
		SampleIntervalNs: muxPlotInterval.Nanoseconds(),
		RoutingPolicy:    muxPlotPolicy,
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
			t = &legTrack{
				label: fmt.Sprintf("R%d·%s·%s", idx, orDash(leg.TransportKind), hopLabel(leg.IntermediatePk)),
				bw:    make([]float64, muxPlotWindow),
				rtt:   make([]float64, muxPlotWindow),
				color: plotPalette[len(tracks)%len(plotPalette)],
			}
			tracks[idx] = t
		}
		bps := leg.InstSendBps
		if muxPlotRecv {
			bps = leg.InstRecvBps
		}
		mbps := bps / 1e6
		if muxPlotSmooth > 1 {
			a := 2.0 / float64(muxPlotSmooth+1)
			if t.ewmaSeeded {
				t.ewmaBw = a*mbps + (1-a)*t.ewmaBw
			} else {
				t.ewmaBw, t.ewmaSeeded = mbps, true
			}
			mbps = t.ewmaBw
		}
		t.bw = append(t.bw[1:], mbps)
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

func updateTracks(tracks map[int]*legTrack, rg muxRouteGroupInfo, now, prevAt time.Time) {
	elapsed := now.Sub(prevAt).Seconds()
	haveRate := !prevAt.IsZero() && elapsed > 0
	seen := map[int]bool{}
	for _, leg := range rg.Legs {
		seen[leg.Index] = true
		t, ok := tracks[leg.Index]
		if !ok {
			t = &legTrack{
				label: fmt.Sprintf("R%d·%s·%s", leg.Index, leg.TpType, shortPK(leg.RemotePK)),
				bw:    make([]float64, muxPlotWindow),
				rtt:   make([]float64, muxPlotWindow),
				color: plotPalette[len(tracks)%len(plotPalette)],
			}
			tracks[leg.Index] = t
		}
		mbps := 0.0
		if haveRate {
			cur, prev := leg.SentBytes, t.prevSent
			if muxPlotRecv {
				cur, prev = leg.RecvBytes, t.prevRecv
			}
			if cur >= prev {
				mbps = float64(cur-prev) * 8 / 1e6 / elapsed
			}
		}
		if muxPlotSmooth > 1 {
			a := 2.0 / float64(muxPlotSmooth+1)
			if t.ewmaSeeded {
				t.ewmaBw = a*mbps + (1-a)*t.ewmaBw
			} else {
				t.ewmaBw, t.ewmaSeeded = mbps, true
			}
			mbps = t.ewmaBw
		}
		t.bw = append(t.bw[1:], mbps)
		t.rtt = append(t.rtt[1:], leg.LatencyMS)
		t.prevSent, t.prevRecv = leg.SentBytes, leg.RecvBytes
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

func render(tracks map[int]*legTrack) {
	idxs := make([]int, 0, len(tracks))
	for i := range tracks {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	if len(idxs) == 0 {
		return
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

	subject := muxPlotSubject
	if subject == "" {
		subject = muxPlotApp
	}
	bw := asciigraph.PlotMany(bwSeries, asciigraph.Height(muxPlotHeight), asciigraph.Width(0),
		asciigraph.SeriesColors(colors...), asciigraph.Precision(1),
		asciigraph.Caption(fmt.Sprintf("bandwidth  Mbps %s  ·  %s", dir, subject)))
	rt := asciigraph.PlotMany(rttSeries, asciigraph.Height(muxPlotHeight/2+2), asciigraph.Width(0),
		asciigraph.SeriesColors(colors...), asciigraph.Precision(0),
		asciigraph.Caption("RTT  ms"))

	fmt.Print("\x1b[H\x1b[2J") // clear + home
	fmt.Printf("skywire mux · %s · %s\n\n", subject, time.Now().Format("15:04:05"))
	fmt.Println(bw)
	fmt.Print("\n\n")
	fmt.Println(rt)
	fmt.Printf("\n%s\n%s\n", legend, agg)
	if len(muxPlotEvents) > 0 {
		fmt.Printf("\nevents: %s\n", strings.Join(tailStrings(muxPlotEvents, 4), "   "))
	}
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
