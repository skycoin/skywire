// Package ping — cmd/skywire-cli/commands/visor/ping/mux_bandwidth_tui.go:
// Bubble Tea TUI consumer of the StreamMuxBandwidth gRPC RPC.
//
// Operator-visible: `cli visor ping mux-bw-tui <pk>` runs the same
// multiplexed-route bandwidth test as `mux-bw` but presents it as a
// live dashboard instead of NDJSON: per-route status row,
// throughput sparkline, RTT-probe sparkline (when --probe-rtt is
// set), and a scrollable events log.
//
// Identical flags to `mux-bw`; the only difference is rendering.
// Both subcommands ride the same server-side RPC handler, so a
// change at the server lands in both.
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

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

func init() {
	// All flags mirror `mux-bw` exactly — the variables are
	// shared across files in this package so the TUI command picks
	// up the same defaults / parser as the NDJSON command.
	muxBandwidthTUICmd.Flags().IntVarP(&muxBwRoutes, "routes", "r", 1,
		"number of parallel route connections (1 = baseline single route)")
	muxBandwidthTUICmd.Flags().DurationVarP(&muxBwDuration, "duration", "d", 30*time.Second,
		"how long to pump bytes (excludes route-setup time)")
	muxBandwidthTUICmd.Flags().IntVarP(&muxBwPacketSizeKb, "size", "s", 32,
		"per-write block size in KB")
	muxBandwidthTUICmd.Flags().IntVar(&muxBwMinHops, "min-hops", 0,
		"route-finder min hops constraint (>=2 excludes the direct transport)")
	muxBandwidthTUICmd.Flags().DurationVar(&muxBwSetupTimeout, "setup-timeout", 30*time.Second,
		"per-route setup timeout")
	muxBandwidthTUICmd.Flags().BoolVar(&muxBwProbeRTT, "probe-rtt", false,
		"also send small-packet RTT probes during the load (queueing-delay measurement)")
	muxBandwidthTUICmd.Flags().DurationVar(&muxBwProbeInterval, "probe-interval", 100*time.Millisecond,
		"interval between RTT probes when --probe-rtt is set")
	muxBandwidthTUICmd.Flags().DurationVar(&muxBwSampleInterval, "sample-interval", 1*time.Second,
		"interval between MuxBandwidthSample events")
	muxBandwidthTUICmd.Flags().BoolVar(&muxBwLocalRoute, "local-route", false,
		"use locally-cached TPD data for route calculation (faster setup; may be stale)")

	RootCmd.AddCommand(muxBandwidthTUICmd)
}

var muxBandwidthTUICmd = &cobra.Command{
	Use:   "mux-bw-tui <pk>",
	Short: "Interactive Bubble Tea TUI for the multiplexed-route bandwidth probe",
	Long: `Live dashboard for StreamMuxBandwidth — same RPC as
'cli visor ping mux-bw', different rendering.

The screen shows:
  * Per-route status row — one tile per route with state, hop count, setup time
  * Live stats line — instant + avg + peak throughput, bytes pumped, active routes
  * Throughput sparkline — last 60 sample-interval ticks
  * RTT sparkline (when --probe-rtt) — last 60 probes
  * Events log — scrollable viewport

For automation / harness consumption use the NDJSON sibling
'cli visor ping mux-bw' instead.

Controls inside the TUI:
  ↑/k, ↓/j     scroll events one line
  PgUp/PgDn    page up/down
  Home/End     top/bottom
  a            toggle auto-scroll
  q/Ctrl+C     quit (results print to stdout on exit)`,
	Args: cobra.ExactArgs(1),
	Run:  runMuxBandwidthTUI,
}

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

	model := newMuxBwTUIModel(ctx, cancel, targetPK, *req)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	go muxBwConsumeStream(stream, p)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "mux-bw-tui: TUI: %v\n", err)
		os.Exit(1)
	}

	// tea.WithAltScreen wipes the screen on exit; reprint the final
	// dashboard to stdout so the run lands in terminal scrollback
	// regardless of how it terminated. Same pattern as ping tree.
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

type muxBwEventMsg struct{ ev *rpcgrpc.MuxBandwidthEvent }
type muxBwStreamDoneMsg struct{}
type muxBwStreamErrMsg struct{ err error }

func muxBwConsumeStream(stream rpcgrpc.PingService_StreamMuxBandwidthClient, p *tea.Program) {
	for {
		ev, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				p.Send(muxBwStreamDoneMsg{})
				return
			}
			p.Send(muxBwStreamErrMsg{err: err})
			return
		}
		p.Send(muxBwEventMsg{ev: ev})
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
	viewport viewport.Model
	spinner  spinner.Model

	ready      bool
	quitting   bool
	width      int
	height     int
	autoScroll bool

	targetPK string
	req      rpcgrpc.MuxBandwidthRequest

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

	ctx    context.Context
	cancel context.CancelFunc
}

func newMuxBwTUIModel(ctx context.Context, cancel context.CancelFunc, targetPK string, req rpcgrpc.MuxBandwidthRequest) *muxBwTUIModel {
	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))

	return &muxBwTUIModel{
		spinner:    sp,
		autoScroll: true,
		ctx:        ctx,
		cancel:     cancel,
		runStart:   time.Now(),
		targetPK:   targetPK,
		req:        req,
		routes:     make([]muxBwRoute, req.Routes),
	}
}

func (m *muxBwTUIModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, muxBwTickCmd())
}

type muxBwTickMsg struct{}

func muxBwTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return muxBwTickMsg{} })
}

func (m *muxBwTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch v := msg.(type) {
	case tea.KeyMsg:
		switch v.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		case "a":
			m.autoScroll = !m.autoScroll
		case "up", "k":
			m.viewport.ScrollUp(1)
			m.autoScroll = false
		case "down", "j":
			m.viewport.ScrollDown(1)
			m.autoScroll = false
		case "pgup":
			m.viewport.HalfPageUp()
			m.autoScroll = false
		case "pgdown":
			m.viewport.HalfPageDown()
			m.autoScroll = false
		case "home", "g":
			m.viewport.GotoTop()
			m.autoScroll = false
		case "end", "G":
			m.viewport.GotoBottom()
			m.autoScroll = true
		}

	case tea.WindowSizeMsg:
		fixedRows := m.fixedRows()
		if !m.ready {
			m.viewport = viewport.New(v.Width, v.Height-fixedRows)
			m.viewport.SetContent(m.renderEventsBody())
			m.ready = true
		} else {
			m.viewport.Width = v.Width
			m.viewport.Height = v.Height - fixedRows
		}
		m.width, m.height = v.Width, v.Height

	case spinner.TickMsg:
		var spinnerCmd tea.Cmd
		m.spinner, spinnerCmd = m.spinner.Update(msg)
		cmds = append(cmds, spinnerCmd)

	case muxBwTickMsg:
		m.viewport.SetContent(m.renderEventsBody())
		if m.autoScroll {
			m.viewport.GotoBottom()
		}
		if !m.streamEnded {
			cmds = append(cmds, muxBwTickCmd())
		}

	case muxBwEventMsg:
		m.applyEvent(v.ev)
		m.viewport.SetContent(m.renderEventsBody())
		if m.autoScroll {
			m.viewport.GotoBottom()
		}

	case muxBwStreamDoneMsg:
		m.streamEnded = true
		m.viewport.SetContent(m.renderEventsBody())

	case muxBwStreamErrMsg:
		m.streamErr = v.err
		m.streamEnded = true
		m.viewport.SetContent(m.renderEventsBody())
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)
	return m, tea.Batch(cmds...)
}

// fixedRows is the number of non-viewport lines we render around
// the events viewport: header(1) + routes(1) + stats(1) + sep(1) +
// throughput-section(3) + (rtt-section(3) if probe-rtt) + sep(1) +
// events-header(1) + footer(1) = 12 or 9.
func (m *muxBwTUIModel) fixedRows() int {
	base := 9
	if muxBwProbeRTT {
		base += 3
	}
	return base
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

func (m *muxBwTUIModel) View() string {
	if m.quitting {
		return "Shutting down...\n"
	}
	if !m.ready {
		return "Initializing...\n"
	}

	footerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	hint := "↑/↓ scroll | a auto-scroll | q quit"
	if m.streamEnded {
		hint = "Run complete — q to exit"
	}
	scrollPct := m.viewport.ScrollPercent() * 100
	scrollTag := fmt.Sprintf("%.0f%%", scrollPct)
	if m.autoScroll {
		scrollTag += " [auto]"
	}
	footer := footerStyle.Render(fmt.Sprintf("%s | %s", hint, scrollTag))

	parts := []string{
		m.renderHeader(),
		m.renderRoutes(),
		m.renderStats(),
		m.renderThroughputBlock(),
	}
	if muxBwProbeRTT {
		parts = append(parts, m.renderRttBlock())
	}
	parts = append(parts,
		lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true).Render("Events:"),
		m.viewport.View(),
		footer,
	)
	return strings.Join(parts, "\n")
}

func (m *muxBwTUIModel) renderHeader() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	elapsed := time.Since(m.runStart).Truncate(time.Second)
	dur := time.Duration(m.req.DurationNs)
	right := fmt.Sprintf("elapsed: %s / %s", elapsed, dur)
	if m.serverError != "" {
		right = "server error: " + m.serverError
	} else if m.streamErr != nil {
		right = "stream error: " + m.streamErr.Error()
	}
	title := fmt.Sprintf("Mux Bandwidth → %s", m.targetPK)
	return titleStyle.Render(title) + "  " + dimStyle.Render(right)
}

func (m *muxBwTUIModel) renderRoutes() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	pendStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	tiles := make([]string, 0, len(m.routes))
	for i, r := range m.routes {
		switch {
		case r.failed:
			tiles = append(tiles, failStyle.Render(fmt.Sprintf("[R%d ✗ %s]", i, truncate(r.setupErr, 24))))
		case r.established:
			tiles = append(tiles, okStyle.Render(fmt.Sprintf("[R%d ✓ hops=%d setup=%s]", i, r.hopCount, formatDuration(r.setupNs))))
		default:
			tiles = append(tiles, pendStyle.Render(fmt.Sprintf("[R%d · pending]", i)))
		}
	}
	label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Render("Routes:  ")
	return label + strings.Join(tiles, " ")
}

func (m *muxBwTUIModel) renderStats() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Render("Stats:   ")
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

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).
		Render(fmt.Sprintf("Throughput (recv, last %d samples):", muxBwSparkWidth))
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

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).
		Render(fmt.Sprintf("RTT probes (loaded, last %d):", muxBwSparkWidth))
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
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	out := headStyle.Render("=== Run Summary ===") + "\n"
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
