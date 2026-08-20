// Package skysocksc cmd/skywire-cli/commands/proxy/mux_plot.go c4-vis-cli
package skysocksc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/guptarohit/asciigraph"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var (
	muxPlotApp      string
	muxPlotInterval time.Duration
	muxPlotWindow   int
	muxPlotHeight   int
	muxPlotSmooth   int
	muxPlotRecv     bool
)

func init() {
	muxPlotCmd.Flags().StringVarP(&muxPlotApp, "name", "n", "skysocks-client", "app name to plot (e.g. skysocks-client, vpn-client)")
	muxPlotCmd.Flags().DurationVarP(&muxPlotInterval, "interval", "i", time.Second, "sample/redraw interval")
	muxPlotCmd.Flags().IntVarP(&muxPlotWindow, "window", "w", 60, "samples of history to keep on screen")
	muxPlotCmd.Flags().IntVar(&muxPlotHeight, "height", 10, "rows per chart panel")
	muxPlotCmd.Flags().IntVar(&muxPlotSmooth, "smooth", 0, "EWMA smoothing window in samples (0 = raw)")
	muxPlotCmd.Flags().BoolVar(&muxPlotRecv, "recv", false, "plot received bandwidth instead of sent")
	addMuxSub(muxPlotCmd, "mux-plot")
}

var muxPlotCmd = &cobra.Command{
	Use:   "plot",
	Short: "Live per-leg bandwidth + RTT chart for a mux'd route group (terminal)",
	Long: `Live terminal chart of each multiplexed route's bandwidth and RTT.

Polls the same per-leg telemetry the routing-policy on_tick ABI sees
(RouteGroupMuxInfo) once per --interval and redraws two stacked charts —
per-leg bandwidth (Mbps) over per-leg RTT (ms) — one colored line per
route. A warm-standby (parked) leg reads 0 bandwidth but keeps its RTT.

Examples:
  skywire cli proxy mux plot                     # default app, 1s
  skywire cli proxy mux plot -n vpn-client -i 500ms
  skywire cli proxy mux plot --smooth 5 --window 90`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if muxPlotWindow < 8 {
			muxPlotWindow = 8
		}
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

	bw := asciigraph.PlotMany(bwSeries, asciigraph.Height(muxPlotHeight), asciigraph.Width(0),
		asciigraph.SeriesColors(colors...), asciigraph.Precision(1),
		asciigraph.Caption(fmt.Sprintf("bandwidth  Mbps %s  ·  %s", dir, muxPlotApp)))
	rt := asciigraph.PlotMany(rttSeries, asciigraph.Height(muxPlotHeight/2+2), asciigraph.Width(0),
		asciigraph.SeriesColors(colors...), asciigraph.Precision(0),
		asciigraph.Caption("RTT  ms"))

	fmt.Print("\x1b[H\x1b[2J") // clear + home
	fmt.Printf("skywire mux · %s · %s\n\n", muxPlotApp, time.Now().Format("15:04:05"))
	fmt.Println(bw)
	fmt.Print("\n\n")
	fmt.Println(rt)
	fmt.Printf("\n%s\n%s\n", legend, agg)
}

// ansi renders an asciigraph color as its SGR escape for the legend swatches.
func ansi(c asciigraph.AnsiColor) string { return fmt.Sprintf("\x1b[38;5;%dm", int(c)) }
