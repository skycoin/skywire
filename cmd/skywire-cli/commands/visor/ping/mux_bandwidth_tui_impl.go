// Package clivisorping cmd/skywire-cli/commands/visor/ping/mux_bandwidth_tui_impl.go c5-cli-visor
//
// The screen of `visor ping mux-bw-tui`; the command spec stays in
// mux_bandwidth_tui.go.
package ping

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

func runMuxBandwidthTUI(_ *cobra.Command, args []string) {
	targetPK := args[0]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mux-bw-tui: gRPC client connect: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "mux-bw-tui: StreamMuxBandwidth call: %v\n", err)
		os.Exit(1)
	}

	model := newMuxBwTUIModel(targetPK, req)
	err = runStreamView(streamView{
		top:  model.renderTop,
		body: model.renderEventsBody,
		hint: model.hint,
		feed: func(changed func()) { go muxBwConsumeStream(stream, model, changed) },
	})
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mux-bw-tui: TUI: %v\n", err)
		os.Exit(1)
	}

	// The screen is cleared on exit, so print the final dashboard where
	// it stays in the scrollback. Same pattern as ping tree.
	fmt.Print(model.renderHeader() + "\n")
	fmt.Print(model.renderRoutes() + "\n")
	fmt.Print(model.renderStats() + "\n")
	fmt.Print(model.renderThroughputBlock() + "\n")
	if muxBwProbeRTT {
		fmt.Print(model.renderRttBlock() + "\n")
	}
	fmt.Print(model.renderDone() + "\n")
}

// ---------------------------------------------------------------------------
// Stream consumer
// ---------------------------------------------------------------------------

func muxBwConsumeStream(stream rpcgrpc.PingService_StreamMuxBandwidthClient, m *muxBwTUIModel, changed func()) {
	for {
		ev, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			m.finish(err)
			changed()
			return
		}
		m.applyEvent(ev)
		changed()
	}
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

const (
	muxBwSparkWidth   = 60
	muxBwEventBufSize = 500
)

type muxBwRoute struct {
	established bool
	failed      bool
	setupErr    string
	setupNs     int64
	hopCount    int
}

type muxBwTimePoint struct {
	elapsedNs int64
	value     float64
}

type muxBwEvent struct {
	elapsedNs int64
	text      string
}

type muxBwTUIModel struct {
	targetPK string
	// req is held by pointer because rpcgrpc.MuxBandwidthRequest
	// embeds a protoimpl.MessageState that contains a sync.Mutex —
	// passing/storing by value triggers go vet's copylocks check.
	// The TUI never mutates the request, only reads its fields.
	req *rpcgrpc.MuxBandwidthRequest

	mu sync.RWMutex

	routes []muxBwRoute // index = route index

	// Throughput history (instant recv bps per sample-interval tick).
	throughput []muxBwTimePoint
	rttProbes  []muxBwTimePoint

	// Latest stats (from most recent Sample event).
	cumSent        uint64
	cumRecv        uint64
	avgSendBps     float64
	avgRecvBps     float64
	instantSendBps float64
	instantRecvBps float64
	peakRecvBps    float64
	peakSendBps    float64
	activeRoutes   int32

	probeCount int64
	probeSum   int64

	events []muxBwEvent

	runStart    time.Time
	streamEnded bool
	streamErr   error
	serverError string
	done        *rpcgrpc.MuxBandwidthDone
}

func newMuxBwTUIModel(targetPK string, req *rpcgrpc.MuxBandwidthRequest) *muxBwTUIModel {
	return &muxBwTUIModel{
		runStart: time.Now(),
		targetPK: targetPK,
		req:      req,
		routes:   make([]muxBwRoute, req.Routes),
	}
}

// finish records the end of the stream, with err nil on a clean close.
func (m *muxBwTUIModel) finish(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamEnded = true
	m.streamErr = err
}

// ---------------------------------------------------------------------------
// Event application
// ---------------------------------------------------------------------------

func (m *muxBwTUIModel) applyEvent(ev *rpcgrpc.MuxBandwidthEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch p := ev.Payload.(type) {
	case *rpcgrpc.MuxBandwidthEvent_RouteEstablished:
		r := p.RouteEstablished
		if int(r.RouteIndex) >= 0 && int(r.RouteIndex) < len(m.routes) {
			m.routes[r.RouteIndex] = muxBwRoute{
				established: !r.Failed,
				failed:      r.Failed,
				setupErr:    r.SetupErr,
				setupNs:     r.SetupLatencyNs,
				hopCount:    len(r.Hops),
			}
		}
		txt := ""
		if r.Failed {
			txt = fmt.Sprintf("R%d FAILED %s (setup=%s)", r.RouteIndex, r.SetupErr, formatDuration(r.SetupLatencyNs))
		} else {
			txt = fmt.Sprintf("R%d established (hops=%d setup=%s)", r.RouteIndex, len(r.Hops), formatDuration(r.SetupLatencyNs))
		}
		m.appendEvent(0, "route_established", txt)

	case *rpcgrpc.MuxBandwidthEvent_Sample:
		s := p.Sample
		m.cumSent = s.BytesSent
		m.cumRecv = s.BytesReceived
		m.instantSendBps = s.InstantSendBps
		m.instantRecvBps = s.InstantRecvBps
		m.avgSendBps = s.AvgSendBps
		m.avgRecvBps = s.AvgRecvBps
		if s.InstantRecvBps > m.peakRecvBps {
			m.peakRecvBps = s.InstantRecvBps
		}
		if s.InstantSendBps > m.peakSendBps {
			m.peakSendBps = s.InstantSendBps
		}
		m.activeRoutes = s.ActiveRoutes
		m.throughput = appendCapped(m.throughput, muxBwTimePoint{
			elapsedNs: s.ElapsedNs,
			value:     s.InstantRecvBps,
		}, muxBwSparkWidth*2)
		m.appendEvent(s.ElapsedNs, "sample",
			fmt.Sprintf("recv=%s active=%d/%d", formatBps(s.InstantRecvBps), s.ActiveRoutes, m.req.Routes))

	case *rpcgrpc.MuxBandwidthEvent_RttProbe:
		r := p.RttProbe
		if r.LatencyNs > 0 {
			m.probeCount++
			m.probeSum += r.LatencyNs
			m.rttProbes = appendCapped(m.rttProbes, muxBwTimePoint{
				elapsedNs: r.ElapsedNs,
				value:     float64(r.LatencyNs),
			}, muxBwSparkWidth*2)
			m.appendEvent(r.ElapsedNs, "rtt_probe",
				fmt.Sprintf("seq=%d rtt=%s", r.Sequence, formatDuration(r.LatencyNs)))
		} else {
			m.appendEvent(r.ElapsedNs, "rtt_probe",
				fmt.Sprintf("seq=%d FAILED %s", r.Sequence, r.Error))
		}

	case *rpcgrpc.MuxBandwidthEvent_Done:
		m.done = p.Done
		m.appendEvent(p.Done.WallTimeNs, "done",
			fmt.Sprintf("avg_recv=%s peak_recv=%s pumped=%s reason=%s",
				formatBps(p.Done.AvgRecvBps),
				formatBps(p.Done.PeakRecvBps),
				formatBytes(p.Done.TotalBytesReceived),
				p.Done.TerminationReason))

	case *rpcgrpc.MuxBandwidthEvent_Error:
		e := p.Error
		m.serverError = fmt.Sprintf("%s: %s", e.Code, e.Message)
		m.appendEvent(0, "error", e.Message)

	case *rpcgrpc.MuxBandwidthEvent_RouteFailure:
		f := p.RouteFailure
		// Mark the route as no-longer-pumping in the per-route
		// state so the operator sees R3 transition out of
		// "established" status with an explicit reason rather
		// than silently dropping from active_routes.
		if int(f.RouteIndex) >= 0 && int(f.RouteIndex) < len(m.routes) {
			m.routes[f.RouteIndex].failed = true
			m.routes[f.RouteIndex].established = false
			m.routes[f.RouteIndex].setupErr = f.ErrorMessage
		}
		m.appendEvent(f.ElapsedNs, "route_failure",
			fmt.Sprintf("R%d pump-failed (sent=%s recv=%s before fail): %s",
				f.RouteIndex,
				formatBytes(f.BytesSentBeforeFailure),
				formatBytes(f.BytesReceivedBeforeFailure),
				f.ErrorMessage))
	}
}

func (m *muxBwTUIModel) appendEvent(elapsedNs int64, typ, body string) {
	m.events = append(m.events, muxBwEvent{
		elapsedNs: elapsedNs,
		text:      fmt.Sprintf("%-18s %s", typ, body),
	})
	if len(m.events) > muxBwEventBufSize {
		m.events = m.events[len(m.events)-muxBwEventBufSize:]
	}
}

func appendCapped[T any](s []T, v T, max int) []T {
	s = append(s, v)
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}

// ---------------------------------------------------------------------------
// Render
// ---------------------------------------------------------------------------

func (m *muxBwTUIModel) hint() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.streamEnded {
		return "Run complete — q to exit"
	}
	return "↑/↓ scroll | a auto-scroll | q quit"
}

// renderTop renders the dashboard above the events log.
func (m *muxBwTUIModel) renderTop(string) string {
	parts := []string{
		m.renderHeader(),
		m.renderRoutes(),
		m.renderStats(),
		m.renderThroughputBlock(),
	}
	if muxBwProbeRTT {
		parts = append(parts, m.renderRttBlock())
	}
	parts = append(parts, sgr(39, true)("Events:"))
	return strings.Join(parts, "\n")
}

func (m *muxBwTUIModel) renderHeader() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	titleStyle := sgr(205, true)
	dimStyle := sgr(244, false)

	elapsed := time.Since(m.runStart).Truncate(time.Second)
	dur := time.Duration(m.req.DurationNs)
	right := fmt.Sprintf("elapsed: %s / %s", elapsed, dur)
	if m.serverError != "" {
		right = "server error: " + m.serverError
	} else if m.streamErr != nil {
		right = "stream error: " + m.streamErr.Error()
	}
	title := fmt.Sprintf("Mux Bandwidth → %s", m.targetPK)
	return titleStyle(title) + "  " + dimStyle(right)
}

func (m *muxBwTUIModel) renderRoutes() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	okStyle := sgr(82, false)
	failStyle := sgr(196, false)
	pendStyle := sgr(241, false)

	tiles := make([]string, 0, len(m.routes))
	for i, r := range m.routes {
		switch {
		case r.failed:
			tiles = append(tiles, failStyle(fmt.Sprintf("[R%d ✗ %s]", i, truncate(r.setupErr, 24))))
		case r.established:
			tiles = append(tiles, okStyle(fmt.Sprintf("[R%d ✓ hops=%d setup=%s]", i, r.hopCount, formatDuration(r.setupNs))))
		default:
			tiles = append(tiles, pendStyle(fmt.Sprintf("[R%d · pending]", i)))
		}
	}
	label := sgr(39, true)("Routes:  ")
	return label + strings.Join(tiles, " ")
}

func (m *muxBwTUIModel) renderStats() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	label := sgr(39, true)("Stats:   ")
	body := fmt.Sprintf("send=%s recv=%s   peak=%s   pumped: %s sent / %s recv   active=%d/%d",
		formatBps(m.instantSendBps),
		formatBps(m.instantRecvBps),
		formatBps(m.peakRecvBps),
		formatBytes(m.cumSent),
		formatBytes(m.cumRecv),
		m.activeRoutes, m.req.Routes,
	)
	return label + body
}

func (m *muxBwTUIModel) renderThroughputBlock() string {
	m.mu.RLock()
	values := make([]float64, len(m.throughput))
	for i, t := range m.throughput {
		values[i] = t.value
	}
	cur := m.instantRecvBps
	avg := m.avgRecvBps
	peak := m.peakRecvBps
	m.mu.RUnlock()

	header := sgr(39, true)(fmt.Sprintf("Throughput (recv, last %d samples):", muxBwSparkWidth))
	chart := sparkline(values, muxBwSparkWidth)
	stats := fmt.Sprintf("  cur=%s  avg=%s  peak=%s",
		formatBps(cur), formatBps(avg), formatBps(peak))
	return header + "\n" + chart + stats
}

func (m *muxBwTUIModel) renderRttBlock() string {
	m.mu.RLock()
	values := make([]float64, len(m.rttProbes))
	for i, t := range m.rttProbes {
		values[i] = t.value
	}
	var avg, p50, p99, jit float64
	n := len(values)
	if m.done != nil {
		avg = float64(m.done.ProbeAvgNs)
		p50 = float64(m.done.ProbeP50Ns)
		p99 = float64(m.done.ProbeP99Ns)
		jit = float64(m.done.ProbeJitterNs)
		n = int(m.done.ProbeCount)
	} else if len(values) > 0 {
		// Running approximation; Done will replace with the
		// authoritative server-computed distribution.
		sorted := append([]float64(nil), values...)
		sort.Float64s(sorted)
		p50 = sorted[len(sorted)/2]
		p99 = sorted[(len(sorted)*99)/100]
		var sum float64
		for _, v := range sorted {
			sum += v
		}
		avg = sum / float64(len(sorted))
		var sq float64
		for _, v := range sorted {
			sq += (v - avg) * (v - avg)
		}
		jit = math.Sqrt(sq / float64(len(sorted)))
	}
	m.mu.RUnlock()

	header := sgr(39, true)(fmt.Sprintf("RTT probes (loaded, last %d):", muxBwSparkWidth))
	chart := sparkline(values, muxBwSparkWidth)
	stats := fmt.Sprintf("  avg=%s  p50=%s  p99=%s  jit=%s  n=%d",
		formatDuration(int64(avg)),
		formatDuration(int64(p50)),
		formatDuration(int64(p99)),
		formatDuration(int64(jit)),
		n,
	)
	return header + "\n" + chart + stats
}

func (m *muxBwTUIModel) renderEventsBody() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sb strings.Builder
	for _, e := range m.events {
		secs := float64(e.elapsedNs) / 1e9
		sb.WriteString(fmt.Sprintf("[+%6.3fs] %s\n", secs, e.text))
	}
	return sb.String()
}

func (m *muxBwTUIModel) renderDone() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.done == nil {
		return ""
	}
	headStyle := sgr(205, true)
	out := headStyle("=== Run Summary ===") + "\n"
	out += fmt.Sprintf(
		"routes:   requested=%d established=%d\n"+
			"timings:  wall=%s pump=%s setup_total=%s\n"+
			"sent:     %s avg=%s peak=%s\n"+
			"recv:     %s avg=%s peak=%s\n",
		m.done.RoutesRequested, m.done.RoutesEstablished,
		formatDuration(m.done.WallTimeNs),
		formatDuration(m.done.PumpTimeNs),
		formatDuration(m.done.SetupTotalNs),
		formatBytes(m.done.TotalBytesSent),
		formatBps(m.done.AvgSendBps),
		formatBps(m.done.PeakSendBps),
		formatBytes(m.done.TotalBytesReceived),
		formatBps(m.done.AvgRecvBps),
		formatBps(m.done.PeakRecvBps),
	)
	if m.done.ProbeCount > 0 {
		out += fmt.Sprintf("rtt:      n=%d avg=%s p50=%s p99=%s jit=%s\n",
			m.done.ProbeCount,
			formatDuration(m.done.ProbeAvgNs),
			formatDuration(m.done.ProbeP50Ns),
			formatDuration(m.done.ProbeP99Ns),
			formatDuration(m.done.ProbeJitterNs),
		)
	}
	out += fmt.Sprintf("reason:   %s\n", m.done.TerminationReason)
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var sparkRunes = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline maps the last `width` values to single-row Unicode block
// glyphs scaled to the max value in the window. Returns a fixed-width
// string; pads with spaces when fewer than `width` samples exist.
func sparkline(values []float64, width int) string {
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}

	start := 0
	if len(values) > width {
		start = len(values) - width
	}
	window := values[start:]

	var maxV float64
	for _, v := range window {
		if v > maxV {
			maxV = v
		}
	}
	if maxV == 0 {
		// All zeros — render the lowest glyph for the window width
		// so the operator sees the sparkline exists.
		return strings.Repeat(string(sparkRunes[0]), len(window)) +
			strings.Repeat(" ", width-len(window))
	}

	var sb strings.Builder
	for _, v := range window {
		idx := int(v / maxV * float64(len(sparkRunes)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkRunes) {
			idx = len(sparkRunes) - 1
		}
		sb.WriteRune(sparkRunes[idx])
	}
	for i := len(window); i < width; i++ {
		sb.WriteRune(' ')
	}
	return sb.String()
}

// formatBps renders a bits-per-second value in the largest sensible
// unit (Kbps / Mbps / Gbps). Three-sig-fig fixed-width output.
func formatBps(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%6.2fGbps", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%6.2fMbps", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%6.2fKbps", v/1e3)
	default:
		return fmt.Sprintf("%6.2f bps", v)
	}
}

// formatBytes renders a byte count in the largest sensible unit.
func formatBytes(v uint64) string {
	f := float64(v)
	switch {
	case f >= 1<<30:
		return fmt.Sprintf("%6.2fGiB", f/(1<<30))
	case f >= 1<<20:
		return fmt.Sprintf("%6.2fMiB", f/(1<<20))
	case f >= 1<<10:
		return fmt.Sprintf("%6.2fKiB", f/(1<<10))
	default:
		return fmt.Sprintf("%6.0f B", f)
	}
}

// formatDuration renders a nanosecond duration in human form.
func formatDuration(ns int64) string {
	switch {
	case ns >= int64(time.Second):
		return fmt.Sprintf("%6.2fs", float64(ns)/float64(time.Second))
	case ns >= int64(time.Millisecond):
		return fmt.Sprintf("%6.1fms", float64(ns)/float64(time.Millisecond))
	case ns >= int64(time.Microsecond):
		return fmt.Sprintf("%6.1fµs", float64(ns)/float64(time.Microsecond))
	default:
		return fmt.Sprintf("%dns", ns)
	}
}

// truncate trims s to maxLen runes with a trailing ellipsis. Used
// for error messages in tight tile rendering.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}
